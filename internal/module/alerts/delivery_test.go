package alerts

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
)

func TestWebhookRequestFormats(t *testing.T) {
	type received struct {
		method, token, query, contentType, body string
	}
	requests := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- received{r.Method, r.Header.Get("X-Token"), r.URL.RawQuery, r.Header.Get("Content-Type"), string(body)}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	for _, tt := range []struct {
		method, format, query, contentType, body string
	}{
		{http.MethodGet, "json", "existing=1&message=Disk+usage&count=2", "", ""},
		{http.MethodPost, "json", "existing=1", "application/json", `{"count":2,"message":"Disk usage"}`},
		{http.MethodPut, "form", "existing=1", "application/x-www-form-urlencoded", "count=2&message=Disk+usage"},
		{http.MethodPatch, "json", "existing=1", "application/json", `{"count":2,"message":"Disk usage"}`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			settings, err := (channelDTO{
				URL: server.URL + "?existing=1", Method: tt.method, Format: tt.format,
				Params: `{"message":"{{message}}","count":2}`, Headers: `{"X-Token":"secret-fixture"}`,
			}).settings("webhook", channelSettings{})
			if err != nil {
				t.Fatal(err)
			}
			m := &Module{}
			if err := m.sendWebhook(t.Context(), settings, "Alert", "Disk usage"); err != nil {
				t.Fatal(err)
			}
			got := <-requests
			gotQuery, _ := url.ParseQuery(got.query)
			wantQuery, _ := url.ParseQuery(tt.query)
			if got.method != tt.method || got.token != "secret-fixture" || gotQuery.Encode() != wantQuery.Encode() ||
				got.contentType != tt.contentType || got.body != tt.body {
				t.Errorf("unexpected request: %+v", got)
			}
		})
	}
}

func TestWebhookSecretsNeverAppearInChannelOutput(t *testing.T) {
	item := channel{Kind: "webhook", Name: "Webhook"}
	settings, err := (channelDTO{
		URL: "https://example.com/private-path?auth=secret-query", Method: http.MethodPatch,
		Headers: `{"Authorization":"secret-header"}`, Params: `{"token":"secret-param"}`,
	}).settings(item.Kind, channelSettings{})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := (channelDTO{Method: http.MethodPost}).settings(item.Kind, settings)
	if err != nil {
		t.Fatal(err)
	}
	if updated.URL != settings.URL || updated.Headers["Authorization"] != "secret-header" || updated.Params["token"] != "secret-param" {
		t.Fatal("editing with blank secret fields discarded saved secrets")
	}
	data, err := json.Marshal(item.dto(updated))
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"private-path", "secret-query", "secret-header", "secret-param"} {
		if strings.Contains(string(data), hidden) {
			t.Errorf("output exposed %q", hidden)
		}
	}
	dto := item.dto(updated)
	if !dto.HasURL || !dto.HasHeaders || !dto.HasParams || dto.VerifyTLS == nil || !*dto.VerifyTLS {
		t.Errorf("incorrect indicators or TLS default: %+v", dto)
	}
	cleared, err := (channelDTO{Headers: "{}", Params: "{}"}).settings(item.Kind, updated)
	if err != nil {
		t.Fatal(err)
	}
	clearedDTO := item.dto(cleared)
	if clearedDTO.HasHeaders || !clearedDTO.HasParams || len(cleared.Params) != 0 {
		t.Error("explicit empty JSON failed to clear headers and set empty params")
	}
}

func TestChannelSavesEncryptedConfigurationAndLogsTest(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "token-fixture" {
			t.Error("saved Webhook header was lost")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	m := &Module{db: db, cfg: config.Config{AESKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	router := gin.New()
	m.channelRoutes(router.Group(""))
	request := func(method, path, body string, status int) json.RawMessage {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if rec.Code != status {
			t.Fatalf("%s %s returned %d: %s", method, path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "token-fixture") || strings.Contains(rec.Body.String(), webhook.URL) {
			t.Fatal("channel response leaked stored credentials")
		}
		var response struct{ Data json.RawMessage }
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}

	create := func(name string) channelDTO {
		t.Helper()
		data := request(http.MethodPost, "/channels", fmt.Sprintf(`{"kind":"webhook","name":%q,"isDefault":true,"url":%q,"headers":%q}`, name, webhook.URL, `{"X-Token":"token-fixture"}`), http.StatusOK)
		var item channelDTO
		if err := json.Unmarshal(data, &item); err != nil || item.ID == "" || !item.IsDefault {
			t.Fatalf("channel was not created as default: %s", data)
		}
		return item
	}
	first, second := create("Primary"), create("Secondary")
	var row channel
	if err := db.First(&row, "id = ?", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.IsDefault || strings.Contains(row.Secret, "token-fixture") || strings.Contains(row.Secret, webhook.URL) {
		t.Fatal("default was not replaced or saved credentials were not encrypted")
	}

	request(http.MethodPut, "/channels/"+first.ID, `{"kind":"webhook","name":"Renamed"}`, http.StatusOK)
	request(http.MethodGet, "/channels", "", http.StatusOK)
	result := request(http.MethodPost, "/channels/"+first.ID+"/test", "", http.StatusOK)
	if string(result) != `{"success":true}` {
		t.Fatalf("channel test failed: %s", result)
	}
	var log deliveryLog
	if err := db.First(&log).Error; err != nil || !log.Success || log.ChannelName != "Renamed" || log.RuleID != "" {
		t.Fatalf("channel test did not record outcome: %+v, %v", log, err)
	}
	linked := rule{ID: "linked-fixture", NextCheckAt: time.Now().UTC()}
	if err := db.Create(&linked).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ruleChannel{RuleID: linked.ID, ChannelID: second.ID}).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodDelete, "/channels/"+second.ID, "", http.StatusConflict)
	if err := db.Delete(&linked).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodDelete, "/channels/"+second.ID, "", http.StatusOK)
}

func TestWebhookValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		data channelDTO
	}{
		{"embedded credentials", channelDTO{URL: "https://user:password@example.com"}},
		{"unsupported scheme", channelDTO{URL: "file:///etc/passwd"}},
		{"unsupported method", channelDTO{Method: http.MethodDelete}},
		{"invalid json parameters", channelDTO{Params: "[]"}},
		{"header injection", channelDTO{Headers: `{"X-Token":"value\r\nInjected: yes"}`}},
		{"non-string header", channelDTO{Headers: `{"X-Token":null}`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.data.URL == "" {
				tt.data.URL = "https://example.com"
			}
			if _, err := tt.data.settings("webhook", channelSettings{}); err == nil {
				t.Fatal("accepted invalid channel")
			}
		})
	}
}

func TestWebhookBlocksManagementAndCrossOriginRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("cross-origin or protected webhook received a request")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer start.Close()
	parsed, _ := url.Parse(destination.URL)
	port, _ := strconv.Atoi(parsed.Port())
	for _, tt := range []struct {
		target string
		module *Module
	}{{start.URL, &Module{}}, {destination.URL, &Module{cfg: config.Config{Port: port}}}} {
		settings, err := (channelDTO{URL: tt.target, Headers: `{"Authorization":"secret-header"}`}).settings("webhook", channelSettings{})
		if err != nil {
			t.Fatal(err)
		}
		if err := tt.module.sendWebhook(t.Context(), settings, "Alert", "Disk usage"); err == nil {
			t.Fatalf("webhook unexpectedly succeeded: %s", tt.target)
		} else if strings.Contains(err.Error(), destination.URL) || strings.Contains(err.Error(), "secret-header") {
			t.Errorf("webhook error revealed notification configuration: %v", err)
		}
	}
}

func serveSMTP(t *testing.T, listener net.Listener) <-chan string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		scanner := bufio.NewScanner(conn)
		_, _ = fmt.Fprint(conn, "220 localhost fixture\r\n")
		var session strings.Builder
		defer func() { result <- session.String() }()
		var data bool
		for scanner.Scan() {
			line := scanner.Text()
			session.WriteString(line + "\n")
			if data {
				if line == "." {
					data = false
					_, _ = fmt.Fprint(conn, "250 queued\r\n")
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				_, _ = fmt.Fprint(conn, "250 localhost fixture\r\n")
			case line == "DATA":
				data = true
				_, _ = fmt.Fprint(conn, "354 enter message\r\n")
			case line == "QUIT":
				_, _ = fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				_, _ = fmt.Fprint(conn, "500 unexpected command\r\n")
			}
		}
	}()
	return result
}

func TestDeliveryRejectsMetadataAddressesBeforeDial(t *testing.T) {
	m := &Module{}
	for _, address := range []string{"169.254.169.254:80", "100.100.100.200:80", "[fd00:ec2::254]:80", "[fe80::1]:80"} {
		conn, err := m.dialDelivery(context.Background(), "tcp", address)
		if conn != nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "云元数据") {
			t.Errorf("address %s was not rejected by the destination guard: %v", address, err)
		}
	}
}

func TestWebhookVerifiesTLSByDefault(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := &Module{}
	settings, err := (channelDTO{URL: server.URL}).settings("webhook", channelSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.sendWebhook(t.Context(), settings, "Alert", "Disk usage"); err == nil {
		t.Fatal("webhook accepted an untrusted certificate by default")
	}
	verify := false
	settings, err = (channelDTO{VerifyTLS: &verify}).settings("webhook", settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.sendWebhook(t.Context(), settings, "Alert", "Disk usage"); err != nil {
		t.Fatal(err)
	}
}

func TestSMTPValidation(t *testing.T) {
	input := channelDTO{
		SMTPHost: "smtp.example.com", SMTPPort: 587, From: "Sender <sender@example.com>",
		Password: "credential-fixture", Recipients: []string{`One <"quoted name"@example.com>`, "two@example.com"},
	}
	settings, err := input.settings("email", channelSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.SMTPMode != "starttls" || settings.From != "sender@example.com" || len(settings.Recipients) != 2 {
		t.Errorf("incorrect SMTP settings: %+v", settings.Recipients)
	}
	if settings.Recipients[0] != `"quoted name"@example.com` {
		t.Error("SMTP mailbox lost its required quoting")
	}
	item := channel{Kind: "email"}
	dto := item.dto(settings)
	if dto.Password != "" || !dto.HasPassword {
		t.Error("incorrect password indicator")
	}
	input.Password = ""
	if edited, err := input.settings("email", settings); err != nil || edited.Password != settings.Password {
		t.Errorf("blank password failed to preserve saved password: %v", err)
	}
	input.SMTPMode = "plain"
	if _, err := input.settings("email", settings); err == nil {
		t.Error("authenticated SMTP accepted over plaintext")
	}
	input.ClearPassword = true
	if cleared, err := input.settings("email", settings); err != nil || cleared.Password != "" {
		t.Errorf("could not explicitly switch to a relay without authentication: %v", err)
	}
	input.SMTPMode = "tls"
	input.Password = "replacement-fixture"
	if _, err := input.settings("email", settings); err == nil {
		t.Error("clearing and setting a password in the same request was accepted")
	}
	input.ClearPassword = false
	input.Recipients = append(input.Recipients, "bad recipient")
	if _, err := input.settings("email", settings); err == nil {
		t.Error("invalid recipient accepted")
	}
}

func TestSMTPDeliversToAllRecipients(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := serveSMTP(t, listener)
	m := &Module{}
	settings, err := (channelDTO{
		SMTPHost: "127.0.0.1", SMTPPort: listener.Addr().(*net.TCPAddr).Port, SMTPMode: "plain",
		From: "sender@example.com", Recipients: []string{"one@example.com", "two@example.com"},
	}).settings("email", channelSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.sendEmail(t.Context(), settings, "磁盘告警", "磁盘即将耗尽"); err != nil {
		t.Fatal(err)
	}
	data := <-session
	for _, want := range []string{"MAIL FROM:<sender@example.com>", "RCPT TO:<one@example.com>", "RCPT TO:<two@example.com>"} {
		if !strings.Contains(data, want) {
			t.Errorf("SMTP session did not include %q", want)
		}
	}
	_, rawMessage, _ := strings.Cut(data, "DATA\n")
	mailMessage, err := mail.ReadMessage(strings.NewReader(rawMessage))
	if err != nil {
		t.Fatal(err)
	}
	if got := mailMessage.Header.Get("To"); got != "one@example.com, two@example.com" {
		t.Errorf("incorrect To header: %s", got)
	}
}
