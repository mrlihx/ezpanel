package certificate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	db            *gorm.DB
	cfg           config.Config
	tasks         *task.Manager
	auth          *auth.Service
	accountMu     sync.Mutex
	prepareHTTP   func(context.Context, string, []string) (string, error)
	deployRenewal func(context.Context, string) error
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, a *auth.Service, prepareHTTP func(context.Context, string, []string) (string, error)) *Module {
	return &Module{db: db, cfg: cfg, tasks: tasks, auth: a, prepareHTTP: prepareHTTP}
}
func (m *Module) Name() string { return "certificate" }
func (m *Module) SetRenewalDeployer(deploy func(context.Context, string) error) {
	m.deployRenewal = deploy
}
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/certificates")
	r.GET("", m.list)
	r.POST("", m.importCertificate)
	r.GET("/orders", m.orders)
	r.POST("/orders", m.createOrder)
	r.GET("/orders/:id", m.order)
	r.POST("/orders/:id/complete", m.completeOrder)
	r.POST("/orders/:id/deploy", m.retryDeployment)
	r.GET("/:id", m.detail)
	r.DELETE("/:id", m.delete)
	r.GET("/:id/download", m.download)
}
func fail(c *gin.Context, err error) {
	code := response.System
	if errors.Is(err, gorm.ErrRecordNotFound) {
		code = response.NotFound
	}
	if errors.Is(err, task.ErrBusy) {
		code = response.Conflict
	}
	response.Fail(c, code, response.Summary(err.Error()))
}
func view(row store.Certificate) gin.H {
	var domains []string
	_ = json.Unmarshal([]byte(row.Domains), &domains)
	return gin.H{"id": row.ID, "domains": domains, "issuer": row.Issuer, "notBefore": row.NotBefore, "notAfter": row.NotAfter, "source": row.Source, "algorithm": certificateAlgorithm(row), "validationMethod": row.ValidationMethod, "email": row.Email, "createdAt": row.CreatedAt}
}

func keyAlgorithm(key any) string {
	switch key := key.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("rsa%d", key.N.BitLen())
	case *ecdsa.PublicKey:
		return fmt.Sprintf("ecdsa%d", key.Curve.Params().BitSize)
	default:
		return ""
	}
}
func certificateAlgorithm(row store.Certificate) string {
	if row.Algorithm != "" {
		return row.Algorithm
	}
	// 旧证书在升级前没有算法字段，从已有公开证书读取，无需接触私钥。
	block, _ := pem.Decode([]byte(row.Certificate))
	if block != nil {
		if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
			return keyAlgorithm(leaf.PublicKey)
		}
	}
	return ""
}

// Store校验证书输入后加密保存私钥；面板与站点共用此证书夹。
func Store(db *gorm.DB, cfg config.Config, cert, key, source, method, email, replaceID string) (store.Certificate, error) {
	var row store.Certificate
	if len(cert) > 1<<20 || len(key) > 64<<10 {
		return row, errors.New("证书或私钥过大")
	}
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil || len(pair.Certificate) == 0 {
		return row, errors.New("证书与私钥不匹配或格式无效")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return row, err
	}
	now := time.Now().UTC()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return row, errors.New("证书尚未生效或已过期")
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	digest := hex.EncodeToString(fingerprint[:])
	existingErr := db.First(&row, "fingerprint = ?", digest).Error
	if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return row, existingErr
	}
	if existingErr == nil && replaceID == "" {
		return row, nil
	}
	if replaceID != "" {
		if existingErr == nil && row.ID != replaceID {
			return row, errors.New("签发证书已登记在其他记录中")
		}
		if err = db.First(&row, "id = ?", replaceID).Error; err != nil {
			return row, err
		}
	} else {
		row.ID = store.ID()
		row.CreatedAt = now
	}
	names := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	data, _ := json.Marshal(names)
	encrypted, err := store.Encrypt(cfg.AESKey, key)
	if err != nil {
		return row, err
	}
	row.Domains = string(data)
	row.Certificate = cert
	row.KeyEnc = encrypted
	row.Fingerprint = digest
	row.Issuer = leaf.Issuer.CommonName
	row.NotBefore = leaf.NotBefore
	row.NotAfter = leaf.NotAfter
	row.Source = source
	row.Algorithm = keyAlgorithm(leaf.PublicKey)
	row.ValidationMethod = method
	row.Email = email
	row.UpdatedAt = now
	err = db.Save(&row).Error
	return row, err
}
func ReadPEM(db *gorm.DB, cfg config.Config, id string) (cert, key string, err error) {
	var row store.Certificate
	if err = db.First(&row, "id = ?", id).Error; err != nil {
		return "", "", err
	}
	key, err = store.Decrypt(cfg.AESKey, row.KeyEnc)
	return row.Certificate, key, err
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	q := m.db.Model(&store.Certificate{})
	if keyword := c.Query("keyword"); keyword != "" {
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(keyword)
		q = q.Where("domains LIKE ? ESCAPE '\\'", "%"+escaped+"%")
	}
	if source := c.Query("source"); source != "" {
		if source != "import" && source != "letsencrypt" {
			response.Fail(c, response.Invalid, "证书来源无效")
			return
		}
		q = q.Where("source = ?", source)
	}
	var total int64
	var rows []store.Certificate
	if err := q.Count(&total).Error; err != nil {
		fail(c, err)
		return
	}
	if err := q.Order("created_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	out := []gin.H{}
	for _, row := range rows {
		out = append(out, view(row))
	}
	response.OK(c, gin.H{"list": out, "total": total})
}
func (m *Module) importCertificate(c *gin.Context) {
	var in struct {
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
	}
	if !response.Bind(c, &in) {
		return
	}
	row, err := Store(m.db, m.cfg, in.Certificate, in.PrivateKey, "import", "", "", "")
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, view(row))
}
func (m *Module) row(c *gin.Context) (store.Certificate, bool) {
	var row store.Certificate
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return row, false
	}
	return row, true
}
func (m *Module) detail(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	out := view(row)
	out["certificate"] = row.Certificate
	out["keyConfigured"] = row.KeyEnc != ""
	if row.KeyEnc != "" {
		if key, err := store.Decrypt(m.cfg.AESKey, row.KeyEnc); err == nil {
			out["privateKey"] = key
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, out)
}
func (m *Module) delete(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	release, err := m.tasks.Acquire("certificate:" + row.ID)
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	var count int64
	if err = m.db.Model(&store.Website{}).Where("certificate_id = ?", row.ID).Count(&count).Error; err != nil {
		fail(c, err)
		return
	}
	if count > 0 || m.cfg.Current().PanelCertificateID == row.ID {
		response.Fail(c, response.Conflict, "证书已部署，请先更换或关闭对应的SSL配置")
		return
	}
	var pending int64
	if err = m.db.Model(&store.CertificateOrder{}).Where("renewal_id = ? AND status IN ?", row.ID, []string{"creating", "pending", "issuing"}).Count(&pending).Error; err != nil {
		fail(c, err)
		return
	}
	if pending > 0 {
		response.Fail(c, response.Conflict, "证书正在申请续签，请先完成申请")
		return
	}
	if err = m.db.Delete(&row).Error; err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) download(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	name := "certificate"
	var site store.Website
	query := m.db.Where("certificate_id = ?", row.ID).Order("id")
	if id := c.Query("siteId"); id != "" {
		query = m.db.Where("id = ?", id)
	}
	err := query.First(&site).Error
	if err == nil {
		name = site.Name
	} else if !errors.Is(err, gorm.ErrRecordNotFound) || c.Query("siteId") != "" {
		fail(c, err)
		return
	} else {
		var domains []string
		_ = json.Unmarshal([]byte(row.Domains), &domains)
		if len(domains) > 0 {
			name = domains[0]
		}
	}
	name = strings.NewReplacer("/", "_", "\\", "_", "\r", "_", "\n", "_").Replace(name)
	key, err := store.Decrypt(m.cfg.AESKey, row.KeyEnc)
	if err != nil {
		fail(c, err)
		return
	}
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for _, file := range []struct{ name, content string }{{"fullchain.pem", row.Certificate}, {"private.key", key}} {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(0600)
		writer, e := archive.CreateHeader(header)
		if e != nil {
			fail(c, e)
			return
		}
		if _, e = io.WriteString(writer, file.content); e != nil {
			fail(c, e)
			return
		}
	}
	if err = archive.Close(); err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("%s_ssl_%06d.zip", name, 100000+rand.IntN(900000))}))
	c.Data(http.StatusOK, "application/zip", data.Bytes())
}
