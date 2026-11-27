package plugin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

const webhookSecretKey = "WEBHOOK_SECRET"

func (m *Module) WebhookRoutes(router *gin.Engine) {
	router.POST(auth.PluginWebhookPath, m.webhook)
}

func (m *Module) webhook(c *gin.Context) {
	id, job := c.Param("id"), c.Param("job")
	timestamp := c.GetHeader("X-Ezpanel-Timestamp")
	when, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().Unix()
	signature, signatureErr := hex.DecodeString(c.GetHeader("X-Ezpanel-Signature"))
	if err != nil || when < now-300 || when > now+300 || signatureErr != nil || len(signature) != sha256.Size || !idPattern.MatchString(id) || !idPattern.MatchString(job) {
		c.Status(http.StatusNotFound)
		return
	}
	var row store.Plugin
	var manifest Manifest
	var values map[string]string
	db := m.db.WithContext(c.Request.Context())
	if db.First(&row, "id = ? AND enabled = ?", id, true).Error != nil || json.Unmarshal([]byte(row.Manifest), &manifest) != nil ||
		!slices.ContainsFunc(manifest.Jobs, func(item Job) bool { return item.ID == job && item.Webhook }) ||
		json.Unmarshal([]byte(row.Config), &values) != nil {
		c.Status(http.StatusNotFound)
		return
	}
	secret, err := store.Decrypt(m.cfg.AESKey, values[webhookSecretKey])
	if err != nil || len(secret) < 32 {
		c.Status(http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, c.Request.URL.Path+"\n"+timestamp+"\n")
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), signature) {
		c.Status(http.StatusNotFound)
		return
	}
	var in struct{ Params map[string]string }
	if err := json.Unmarshal(raw, &in); err != nil {
		response.Fail(c, response.Invalid, "Webhook 请求参数无效")
		return
	}
	receipt := hex.EncodeToString(signature)
	result := db.Exec("INSERT INTO plugin_webhook_receipts (signature, expires_at) VALUES (?, ?) ON CONFLICT DO NOTHING", receipt, when+300)
	if result.Error != nil {
		response.Fail(c, response.System, "无法记录 Webhook 请求")
		return
	}
	if result.RowsAffected == 0 {
		response.Fail(c, response.Conflict, "Webhook 请求已处理")
		return
	}
	taskID, err := m.submitJob(id, job, "webhook:"+id, m.auth.ClientIP(c), "", "webhook", in.Params)
	if err != nil {
		_ = db.Exec("DELETE FROM plugin_webhook_receipts WHERE signature = ?", receipt).Error
		response.Fail(c, response.Conflict, err.Error())
		return
	}
	response.OK(c, gin.H{"taskId": taskID})
}
