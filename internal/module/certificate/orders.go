package certificate

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/acme"
	"gorm.io/gorm"
)

type challenge struct {
	Domain     string `json:"domain"`
	Type       string `json:"type"`
	RecordName string `json:"recordName"`
	Value      string `json:"value"`
}

var acmeDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var acmeHTTPToken = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func orderView(row store.CertificateOrder) gin.H {
	var domains []string
	var challenges []challenge
	_ = json.Unmarshal([]byte(row.Domains), &domains)
	_ = json.Unmarshal([]byte(row.Challenges), &challenges)
	if challenges == nil {
		challenges = []challenge{}
	}
	return gin.H{"id": row.ID, "domains": domains, "email": row.Email, "method": row.Method, "algorithm": row.Algorithm, "autoDeploy": row.AutoDeploy, "siteId": row.SiteID, "status": row.Status, "error": row.Error, "certificateId": row.CertificateID, "taskId": row.TaskID, "challenges": challenges, "createdAt": row.CreatedAt}
}
func (m *Module) orders(c *gin.Context) {
	q := m.db.Model(&store.CertificateOrder{})
	if site := c.Query("siteId"); site != "" {
		q = q.Where("site_id = ?", site)
	}
	// 面板重启后任务已中断，订单同步显示失败，允许重新申请。
	if err := m.reconcileOrders(); err != nil {
		fail(c, err)
		return
	}
	var rows []store.CertificateOrder
	if err := q.Order("created_at DESC,id DESC").Limit(100).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	out := []gin.H{}
	for _, row := range rows {
		out = append(out, orderView(row))
	}
	response.OK(c, gin.H{"list": out})
}
func (m *Module) order(c *gin.Context) {
	var row store.CertificateOrder
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return
	}
	response.OK(c, orderView(row))
}
func validateOrder(domains []string, email, method, site string, terms bool) ([]string, string, error) {
	if !terms {
		return nil, "", errors.New("申请前请同意Let's Encrypt服务条款")
	}
	address, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || address.Address != strings.TrimSpace(email) || len(email) > 254 {
		return nil, "", errors.New("请输入有效联系邮箱")
	}
	if method != "http-01" && method != "dns-01" {
		return nil, "", errors.New("验证方式无效")
	}
	if method == "http-01" && site == "" {
		return nil, "", errors.New("HTTP验证需要选择已有网站，且域名在该站点监听公网80端口")
	}
	if len(domains) < 1 || len(domains) > 100 {
		return nil, "", errors.New("每张证书支持1至100个域名")
	}
	seen := map[string]bool{}
	names := []string{}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		host := strings.TrimPrefix(domain, "*.")
		if strings.HasPrefix(domain, "*.") && method != "dns-01" {
			return nil, "", errors.New("泛域名证书请使用DNS验证")
		}
		if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
			return nil, "", errors.New("请输入有效的公网域名，暂不支持IP证书")
		}
		for _, label := range strings.Split(host, ".") {
			if !acmeDomainLabel.MatchString(label) {
				return nil, "", errors.New("域名格式无效，国际化域名请使用Punycode格式")
			}
		}
		if !seen[domain] {
			seen[domain] = true
			names = append(names, domain)
		}
	}
	return names, address.Address, nil
}
func (m *Module) createOrder(c *gin.Context) {
	var in struct {
		Domains     []string `json:"domains"`
		Email       string   `json:"email"`
		Method      string   `json:"method"`
		Algorithm   string   `json:"algorithm"`
		SiteID      string   `json:"siteId"`
		RenewalID   string   `json:"renewalId"`
		AcceptTerms bool     `json:"acceptTerms"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Algorithm == "" {
		in.Algorithm = "rsa2048"
	}
	switch in.Algorithm {
	case "rsa2048", "rsa4096", "ecdsa256", "ecdsa384":
	default:
		response.Fail(c, response.Invalid, "证书算法无效")
		return
	}
	domains, email, err := validateOrder(in.Domains, in.Email, in.Method, in.SiteID, in.AcceptTerms)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	if in.RenewalID != "" {
		var previous store.Certificate
		if err = m.db.First(&previous, "id = ? AND source = ?", in.RenewalID, "letsencrypt").Error; err != nil {
			fail(c, err)
			return
		}
		if in.Algorithm != certificateAlgorithm(previous) {
			response.Fail(c, response.Invalid, "续签请保留原证书算法")
			return
		}
		var pending int64
		if err = m.db.Model(&store.CertificateOrder{}).Where("renewal_id = ? AND status IN ?", previous.ID, []string{"creating", "pending", "issuing"}).Count(&pending).Error; err != nil {
			fail(c, err)
			return
		}
		if pending > 0 {
			response.Fail(c, response.Conflict, "已有续签申请正在进行，请先完成现有申请")
			return
		}
		// 续签保持原域名，避免同一记录变更覆盖范围后破坏已部署站点。
		var prior []string
		_ = json.Unmarshal([]byte(previous.Domains), &prior)
		if len(prior) != len(domains) {
			response.Fail(c, response.Invalid, "续签不能改变证书域名，请新建申请")
			return
		}
		allowed := map[string]bool{}
		for _, name := range prior {
			allowed[name] = true
		}
		for _, name := range domains {
			if !allowed[name] {
				response.Fail(c, response.Invalid, "续签不能改变证书域名，请新建申请")
				return
			}
		}
	}
	encoded, _ := json.Marshal(domains)
	now := time.Now().UTC()
	row := store.CertificateOrder{ID: store.ID(), Domains: string(encoded), Email: email, Method: in.Method, Algorithm: in.Algorithm, SiteID: in.SiteID, RenewalID: in.RenewalID, Status: "creating", Challenges: "[]", CreatedAt: now, UpdatedAt: now}
	if err = m.db.Create(&row).Error; err != nil {
		fail(c, err)
		return
	}
	m.submitOrder(c, row, false)
}
func (m *Module) completeOrder(c *gin.Context) {
	var row store.CertificateOrder
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return
	}
	if row.Status != "pending" || row.Method != "dns-01" {
		response.Fail(c, response.Invalid, "仅等待DNS记录的申请可以继续验证")
		return
	}
	m.submitOrder(c, row, true)
}
func (m *Module) submitOrder(c *gin.Context, row store.CertificateOrder, complete bool) {
	lock := "certificate-order:" + row.ID
	if row.RenewalID != "" {
		lock = "certificate:" + row.RenewalID
	}
	id, err := m.tasks.Submit(task.Meta{Module: "certificate", Action: "issue", Target: row.Email, ResourceID: row.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: lock}, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		var err error
		if row.AutoDeploy && row.CertificateID != "" {
			err = m.deployIssued(ctx, row, log)
		} else {
			err = m.issue(ctx, row.ID, complete, log)
		}
		if err != nil {
			summary := response.Summary(err.Error())
			_ = m.db.Model(&store.CertificateOrder{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "failed", "error": summary, "updated_at": time.Now().UTC()}).Error
			log.Line(summary)
			return nil, errors.New(summary)
		}
		return gin.H{"orderId": row.ID}, nil
	})
	if err != nil {
		if !complete {
			_ = m.db.Model(&row).Updates(map[string]any{"status": "failed", "error": response.Summary(err.Error())}).Error
		}
		fail(c, err)
		return
	}
	if err = m.db.Model(&store.CertificateOrder{}).Where("id = ?", row.ID).Update("task_id", id).Error; err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id, "orderId": row.ID})
}
func (m *Module) client(ctx context.Context, email string) (*acme.Client, error) {
	m.accountMu.Lock()
	defer m.accountMu.Unlock()
	var account store.ACMEAccount
	err := m.db.First(&account, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		encrypted, e := store.Encrypt(m.cfg.AESKey, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
		if e != nil {
			return nil, e
		}
		account = store.ACMEAccount{Email: email, KeyEnc: encrypted}
		if e = m.db.Create(&account).Error; e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	}
	secret, err := store.Decrypt(m.cfg.AESKey, account.KeyEnc)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(secret))
	if block == nil {
		return nil, errors.New("ACME账号私钥格式无效")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("ACME账号私钥不可用")
	}
	client := &acme.Client{Key: signer, KID: acme.KeyID(account.URI), DirectoryURL: acme.LetsEncryptURL, HTTPClient: &http.Client{Timeout: 45 * time.Second}, UserAgent: "ezpanel"}
	if account.URI == "" {
		registered, e := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + email}}, acme.AcceptTOS)
		if e != nil {
			return nil, e
		}
		account.URI = registered.URI
		if e = m.db.Save(&account).Error; e != nil {
			return nil, e
		}
		client.KID = acme.KeyID(account.URI)
	}
	return client, nil
}
func (m *Module) issue(ctx context.Context, id string, complete bool, log *task.Log) error {
	var row store.CertificateOrder
	if err := m.db.First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	if err := m.db.Model(&store.CertificateOrder{}).Where("id = ?", id).Update("task_id", log.TaskID()).Error; err != nil {
		return err
	}
	var domains []string
	_ = json.Unmarshal([]byte(row.Domains), &domains)
	client, err := m.client(ctx, row.Email)
	if err != nil {
		return err
	}
	root := ""
	if row.Method == "http-01" {
		if m.prepareHTTP == nil {
			return errors.New("HTTP验证未接入网站服务，请使用DNS验证")
		}
		root, err = m.prepareHTTP(ctx, row.SiteID, domains)
		if err != nil {
			return err
		}
	}
	var order *acme.Order
	if row.OrderURI == "" {
		log.Line("开始Let's Encrypt申请")
		order, err = client.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
		if err != nil {
			return err
		}
		var key crypto.Signer
		var e error
		switch row.Algorithm {
		case "rsa2048":
			key, e = rsa.GenerateKey(rand.Reader, 2048)
		case "rsa4096":
			key, e = rsa.GenerateKey(rand.Reader, 4096)
		case "ecdsa384":
			key, e = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		default:
			key, e = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		}
		if e != nil {
			return e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return e
		}
		row.KeyEnc, e = store.Encrypt(m.cfg.AESKey, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
		if e != nil {
			return e
		}
		row.OrderURI = order.URI
		row.AccountURI = string(client.KID)
		if err = m.db.Model(&store.CertificateOrder{}).Where("id = ?", id).Updates(map[string]any{"order_uri": row.OrderURI, "account_uri": row.AccountURI, "key_enc": row.KeyEnc}).Error; err != nil {
			return err
		}
	} else {
		order, err = client.GetOrder(ctx, row.OrderURI)
		if err != nil {
			return err
		}
	}
	type authChallenge struct {
		url      string
		selected *acme.Challenge
	}
	selected := []authChallenge{}
	records := []challenge{}
	paths := []string{}
	defer func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}()
	for _, address := range order.AuthzURLs {
		authorization, err := client.GetAuthorization(ctx, address)
		if err != nil {
			return err
		}
		if authorization.Status == acme.StatusValid {
			continue
		}
		if authorization.Status != acme.StatusPending {
			return fmt.Errorf("域名%s授权状态为%s，请新建申请", authorization.Identifier.Value, authorization.Status)
		}
		var selectedChallenge *acme.Challenge
		for _, candidate := range authorization.Challenges {
			if candidate.Type == row.Method {
				selectedChallenge = candidate
				break
			}
		}
		if selectedChallenge == nil {
			return fmt.Errorf("域名%s不支持所选验证方式", authorization.Identifier.Value)
		}
		token := selectedChallenge.Token
		domain := authorization.Identifier.Value
		if row.Method == "dns-01" {
			value, e := client.DNS01ChallengeRecord(token)
			if e != nil {
				return e
			}
			records = append(records, challenge{Domain: domain, Type: "dns-01", RecordName: "_acme-challenge." + strings.TrimPrefix(domain, "*."), Value: value})
		} else {
			if !acmeHTTPToken.MatchString(token) {
				return errors.New("CA返回了无效验证文件名")
			}
			value, e := client.HTTP01ChallengeResponse(token)
			if e != nil {
				return e
			}
			path := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(client.HTTP01ChallengePath(token), "/")))
			if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
				return e
			}
			if e = os.Chmod(filepath.Dir(path), 0755); e != nil {
				return e
			}
			if e = os.WriteFile(path, []byte(value), 0644); e != nil {
				return e
			}
			if e = os.Chmod(path, 0644); e != nil {
				return e
			}
			paths = append(paths, path)
			records = append(records, challenge{Domain: domain, Type: "http-01", RecordName: "http://" + domain + client.HTTP01ChallengePath(token), Value: value})
		}
		selected = append(selected, authChallenge{address, selectedChallenge})
	}
	data, _ := json.Marshal(records)
	status := "issuing"
	if row.Method == "dns-01" && !complete {
		status = "pending"
	}
	if err = m.db.Model(&store.CertificateOrder{}).Where("id = ?", id).Updates(map[string]any{"challenges": string(data), "status": status, "error": "", "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	if status == "pending" {
		log.Line("请在DNS服务商添加界面所示TXT记录，生效后点击继续验证")
		return nil
	}
	// 泛域名与根域可能使用同名的两条TXT记录，逐个等待授权完成。
	for _, authorization := range selected {
		if _, err = client.Accept(ctx, authorization.selected); err != nil {
			return err
		}
		if _, err = client.WaitAuthorization(ctx, authorization.url); err != nil {
			return err
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return err
	}
	private, err := store.Decrypt(m.cfg.AESKey, row.KeyEnc)
	if err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(private))
	if block == nil {
		return errors.New("申请私钥无法读取")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: domains}, key)
	if err != nil {
		return err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return err
	}
	var cert strings.Builder
	for _, der := range chain {
		cert.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	status = "valid"
	if row.AutoDeploy {
		status = "issuing"
	}
	// 证书版本与订单状态一起入库，以便部署失败时重试同一张证书。
	if err = m.db.Transaction(func(tx *gorm.DB) error {
		saved, err := Store(tx, m.cfg, cert.String(), private, "letsencrypt", row.Method, row.Email, row.RenewalID)
		if err != nil {
			return err
		}
		row.CertificateID = saved.ID
		return tx.Model(&store.CertificateOrder{}).Where("id = ?", id).Updates(map[string]any{"certificate_id": row.CertificateID, "status": status, "error": "", "updated_at": time.Now().UTC()}).Error
	}); err != nil {
		return err
	}
	if row.AutoDeploy {
		return m.deployIssued(ctx, row, log)
	}
	log.Line("证书签发完成，已保存到证书夹；可选择部署到面板或站点")
	return nil
}
