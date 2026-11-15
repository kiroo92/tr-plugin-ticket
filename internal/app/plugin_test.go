package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/plugintest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

var user = sdk.Caller{UserID: 11, Username: "alice", Role: "user"}
var other = sdk.Caller{UserID: 12, Username: "bob", Role: "user"}
var admin = sdk.Caller{UserID: 1, Username: "admin", Role: "admin"}

const notificationConfig = `{"telegram_enabled":true,"bot_token":"12345:abcdefghijklmnop","chat_id":"-100123","site_url":"https://example.com"}`

func start(t *testing.T, h *plugintest.Harness) *Plugin {
	t.Helper()
	p := New()
	if err := p.Init(context.Background(), h.Env); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return p
}
func TestShutdownCancelsDelivery(t *testing.T) {
	h := plugintest.New(t)
	withNotifications(t, h)
	p := start(t, h)
	create(t, h, p, user, "shutdown-request-001")
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p.telegramURL = server.URL
	result := make(chan error, 1)
	go func() { result <- p.RunTask(context.Background(), "deliver-notifications", time.Now()) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("delivery did not stop")
	}
	if err := p.db.Ping(); err == nil {
		t.Fatal("database not closed")
	}
}
func TestDisablingStopsRemainingDeliveries(t *testing.T) {
	h := plugintest.New(t)
	withNotifications(t, h)
	p := start(t, h)
	create(t, h, p, user, "disable-notice-0001")
	create(t, h, p, user, "disable-notice-0002")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		config, _ := sdk.NewConfig([]byte("{}"))
		if err := p.ConfigChanged(context.Background(), config); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte("{\"ok\":true}"))
	}))
	defer server.Close()
	p.telegramURL = server.URL
	if err := p.RunTask(context.Background(), "deliver-notifications", time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || scalar(t, p, "SELECT count(*) FROM notification_outbox WHERE state='pending'") != 1 {
		t.Fatal("continued after disabling")
	}
}
func withNotifications(t *testing.T, h *plugintest.Harness) {
	t.Helper()
	config, err := sdk.NewConfig([]byte(notificationConfig))
	if err != nil {
		t.Fatal(err)
	}
	h.Env.Config = config
}
func request(t *testing.T, h *plugintest.Harness, p *Plugin, caller sdk.Caller, method, path string, body any, code int) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	response := h.Request(p.Handler(), httptest.NewRequest(method, path, bytes.NewReader(data)), caller)
	if response.Code != code {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, code, response.Body.String())
	}
	return response
}
func create(t *testing.T, h *plugintest.Harness, p *Plugin, caller sdk.Caller, key string) int64 {
	t.Helper()
	r := request(t, h, p, caller, "POST", "/tickets", operation{RequestID: key, Title: "API 失败 100%", Category: "API 调用", Priority: "普通", Body: "private error details"}, 200)
	var result struct{ ID int64 }
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}
func getDetail(t *testing.T, h *plugintest.Harness, p *Plugin, caller sdk.Caller, path string) (Ticket, []Message) {
	t.Helper()
	r := request(t, h, p, caller, "GET", path, nil, 200)
	var result struct {
		Ticket   Ticket
		Messages []Message
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Ticket, result.Messages
}
func scalar(t *testing.T, p *Plugin, query string) int {
	t.Helper()
	var value int
	if err := p.db.QueryRow(query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestInitAndManifest(t *testing.T) {
	data, err := os.ReadFile("../../plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.MinHostVersion != "0.7.3" {
		t.Fatal(m.MinHostVersion)
	}
	for _, name := range []string{"empty", "relative", "missing", "file", "future", "corrupt"} {
		t.Run(name, func(t *testing.T) {
			h := plugintest.New(t)
			switch name {
			case "empty":
				h.Env.DataDir = ""
			case "relative":
				h.Env.DataDir = "."
			case "missing":
				h.Env.DataDir = filepath.Join(h.Env.DataDir, "missing")
			case "file":
				path := filepath.Join(h.Env.DataDir, "file")
				if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				h.Env.DataDir = path
			case "future":
				p := start(t, h)
				if _, err := p.db.Exec("PRAGMA user_version=2"); err != nil {
					t.Fatal(err)
				}
				if err := p.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(h.Env.DataDir, "plugin.sqlite"), []byte("not a database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := New()
			if err := p.Init(context.Background(), h.Env); err == nil {
				_ = p.Shutdown(context.Background())
				t.Fatal("accepted invalid storage")
			}
		})
	}
}
func TestWorkflowAccessAndIdempotency(t *testing.T) {
	h := plugintest.New(t)
	withNotifications(t, h)
	p := start(t, h)
	request(t, h, p, sdk.Caller{}, "GET", "/tickets", nil, 401)
	request(t, h, p, user, "GET", "/admin/tickets", nil, 403)
	id := create(t, h, p, user, "create-request-0001")
	path := fmt.Sprintf("/tickets/%d", id)
	adminPath := "/admin" + path
	if create(t, h, p, user, "create-request-0001") != id || scalar(t, p, "SELECT count(*) FROM tickets") != 1 {
		t.Fatal("not idempotent")
	}
	request(t, h, p, user, "POST", "/tickets", operation{RequestID: "create-request-0001", Title: "different", Category: "API 调用", Priority: "普通", Body: "body"}, 409)
	request(t, h, p, other, "GET", path, nil, 404)
	request(t, h, p, other, "POST", path+"/reply", operation{RequestID: "forbidden-request-01", Body: "steal"}, 404)
	request(t, h, p, user, "POST", path+"/reply", map[string]any{"request_id": "forged-request-0001", "body": "x", "user_id": 12}, 400)
	ticket, messages := getDetail(t, h, p, user, path)
	if !ticket.AwaitingAdmin || len(messages) != 1 {
		t.Fatal(ticket, messages)
	}
	request(t, h, p, admin, "POST", adminPath+"/reply", operation{RequestID: "staff-request-00001", Body: "正在核查"}, 200)
	ticket, messages = getDetail(t, h, p, user, path)
	if ticket.AwaitingAdmin || messages[1].Role != "staff" {
		t.Fatal(ticket, messages)
	}
	request(t, h, p, user, "POST", path+"/reply", operation{RequestID: "reply-request-00001", Body: "更多信息"}, 200)
	ticket, _ = getDetail(t, h, p, admin, adminPath)
	if !ticket.AwaitingAdmin {
		t.Fatal("missing pending flag")
	}
	request(t, h, p, admin, "POST", adminPath+"/resolve", operation{RequestID: "resolve-request-001"}, 200)
	request(t, h, p, user, "POST", path+"/reply", operation{RequestID: "closed-request-0001", Body: "test"}, 409)
	ticket, _ = getDetail(t, h, p, user, path)
	if ticket.Status != "resolved" || ticket.AwaitingAdmin {
		t.Fatal(ticket)
	}
	request(t, h, p, user, "POST", path+"/reopen", operation{RequestID: "reopen-request-0001"}, 200)
	ticket, _ = getDetail(t, h, p, user, path)
	if ticket.Status != "open" || !ticket.AwaitingAdmin {
		t.Fatal(ticket)
	}
	if scalar(t, p, "SELECT count(*) FROM notification_outbox") != 3 {
		t.Fatal("wrong notifications")
	}
	r := request(t, h, p, admin, "GET", "/admin/tickets?status=pending&q=100%25", nil, 200)
	if !strings.Contains(r.Body.String(), `"pending":1`) || !strings.Contains(r.Body.String(), `"id":1`) {
		t.Fatal(r.Body.String())
	}
	r = request(t, h, p, other, "GET", "/tickets?q=private", nil, 200)
	if !strings.Contains(r.Body.String(), `"rows":[]`) {
		t.Fatal(r.Body.String())
	}
	request(t, h, p, user, "GET", "/tickets?status=pending", nil, 400)
	request(t, h, p, user, "GET", "/tickets?offset=-1", nil, 400)
	request(t, h, p, user, "POST", path+"/reply", operation{RequestID: "long-request-000001", Body: strings.Repeat("中", 4001)}, 400)
}
func TestPersistenceIsolationAndAtomicity(t *testing.T) {
	h := plugintest.New(t)
	withNotifications(t, h)
	p := start(t, h)
	id := create(t, h, p, user, "persist-request-001")
	if _, err := p.db.Exec("CREATE TRIGGER reject_notice BEFORE INSERT ON notification_outbox BEGIN SELECT RAISE(ABORT, 'offline'); END"); err != nil {
		t.Fatal(err)
	}
	request(t, h, p, user, "POST", "/tickets", operation{RequestID: "rollback-request-01", Title: "rollback", Category: "其他问题", Priority: "普通", Body: "body"}, 503)
	if scalar(t, p, "SELECT count(*) FROM tickets") != 1 || scalar(t, p, "SELECT count(*) FROM operations") != 1 {
		t.Fatal("partial commit")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened := start(t, h)
	ticket, messages := getDetail(t, h, reopened, user, fmt.Sprintf("/tickets/%d", id))
	if ticket.ID != id || len(messages) != 1 || scalar(t, reopened, "SELECT count(*) FROM notification_outbox") != 1 {
		t.Fatal("lost data")
	}
	if create(t, h, reopened, user, "persist-request-001") != id {
		t.Fatal("lost idempotency")
	}
	entries, err := os.ReadDir(h.Env.StateDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("business data in StateDir", err)
	}
	h2 := plugintest.New(t)
	isolated := start(t, h2)
	if h.Env.DataDir == h2.Env.DataDir || scalar(t, isolated, "SELECT count(*) FROM tickets") != 0 {
		t.Fatal("harnesses not isolated")
	}
	if _, err := os.Stat(filepath.Join(h.Env.DataDir, "plugin.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.Exec("PRAGMA foreign_keys"); err != nil {
		t.Fatal(err)
	}
	reopened.db.SetMaxIdleConns(0)
	if scalar(t, reopened, "PRAGMA foreign_keys") != 1 {
		t.Fatal("foreign keys lost on reconnect")
	}
}
func TestConcurrentReplayAndPagination(t *testing.T) {
	h := plugintest.New(t)
	p := start(t, h)
	var group sync.WaitGroup
	responses := make(chan int, 8)
	for range 8 {
		group.Go(func() {
			body, _ := json.Marshal(operation{RequestID: "concurrent-key-0001", Title: "parallel", Category: "其他问题", Priority: "普通", Body: "body"})
			r := h.Request(p.Handler(), httptest.NewRequest("POST", "/tickets", bytes.NewReader(body)), user)
			responses <- r.Code
		})
	}
	group.Wait()
	close(responses)
	for code := range responses {
		if code != 200 {
			t.Fatal(code)
		}
	}
	if scalar(t, p, "SELECT count(*) FROM tickets") != 1 {
		t.Fatal("duplicate tickets")
	}
	for index := range 31 {
		create(t, h, p, user, fmt.Sprintf("pagination-key-%04d", index))
	}
	r := request(t, h, p, user, "GET", "/tickets", nil, 200)
	var page struct {
		Rows []Ticket
		Next int `json:"next_offset"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 30 || page.Next != 30 {
		t.Fatal(page)
	}
	r = request(t, h, p, user, "GET", "/tickets?offset=30", nil, 200)
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || page.Next != -1 {
		t.Fatal(page)
	}
	for index := range 52 {
		request(t, h, p, user, "POST", "/tickets/1/reply", operation{RequestID: fmt.Sprintf("message-page-%04d", index), Body: "reply"}, 200)
	}
	r = request(t, h, p, user, "GET", "/tickets/1", nil, 200)
	var detail struct {
		Messages []Message
		Next     int64 `json:"next_before"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 50 || detail.Next == 0 || detail.Messages[0].ID != detail.Next {
		t.Fatal(detail)
	}
	r = request(t, h, p, user, "GET", fmt.Sprintf("/tickets/1?before=%d", detail.Next), nil, 200)
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 3 || detail.Next != 0 {
		t.Fatal(detail)
	}
}
func TestTelegramDeliveryAndRecovery(t *testing.T) {
	h := plugintest.New(t)
	withNotifications(t, h)
	p := start(t, h)
	create(t, h, p, user, "notify-request-0001")
	var calls atomic.Int32
	var mode atomic.Value
	mode.Store("rate")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		text, _ := payload["text"].(string)
		if strings.Contains(text, "private") || strings.Contains(text, "alice") || strings.Contains(text, "abcdefghijkl") {
			t.Error("private data sent", text)
		}
		if payload["chat_id"] != "-100123" {
			t.Error(payload)
		}
		switch mode.Load().(string) {
		case "rate":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":120}}`))
		case "denied":
			w.WriteHeader(403)
			_, _ = w.Write([]byte("sensitive token"))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		}
	}))
	defer server.Close()
	p.telegramURL = server.URL
	if err := p.RunTask(context.Background(), "deliver-notifications", time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || scalar(t, p, "SELECT attempts FROM notification_outbox") != 1 || scalar(t, p, "SELECT count(*) FROM notification_outbox WHERE state='pending' AND next_at>unixepoch()+60") != 1 {
		t.Fatal("rate retry not persisted")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	p = start(t, h)
	p.telegramURL = server.URL
	if _, err := p.db.Exec("UPDATE notification_outbox SET next_at=0"); err != nil {
		t.Fatal(err)
	}
	mode.Store("success")
	if err := p.RunTask(context.Background(), "deliver-notifications", time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || scalar(t, p, "SELECT count(*) FROM notification_outbox WHERE state='sent'") != 1 {
		t.Fatal("restart did not recover")
	}
	request(t, h, p, admin, "POST", "/admin/notifications/test", map[string]string{"request_id": "test-notice-000001"}, 202)
	request(t, h, p, admin, "POST", "/admin/notifications/test", map[string]string{"request_id": "test-notice-000001"}, 202)
	if scalar(t, p, "SELECT count(*) FROM notification_outbox") != 2 {
		t.Fatal("duplicate test notice")
	}
	mode.Store("denied")
	if err := p.RunTask(context.Background(), "deliver-notifications", time.Now()); err != nil {
		t.Fatal(err)
	}
	r := request(t, h, p, admin, "GET", "/admin/notifications", nil, 200)
	if strings.Contains(r.Body.String(), "sensitive") || strings.Contains(r.Body.String(), "abcdefghijkl") || !strings.Contains(r.Body.String(), "TELEGRAM_HTTP_403") {
		t.Fatal(r.Body.String())
	}
	request(t, h, p, user, "POST", "/admin/notifications/retry", nil, 403)
	request(t, h, p, admin, "POST", "/admin/notifications/retry", nil, 200)
	if scalar(t, p, "SELECT count(*) FROM notification_outbox WHERE state='failed'") != 0 {
		t.Fatal("retry failed")
	}
	disabled, _ := sdk.NewConfig([]byte(`{}`))
	if err := p.ConfigChanged(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	if err := p.RunTask(context.Background(), "deliver-notifications", time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("disabled notification sent")
	}
}
func TestTelegramErrorsAndConfiguration(t *testing.T) {
	for _, body := range []string{`{"telegram_enabled":true}`, `{"site_url":"https://example.com/path"}`, `{"site_url":"https://user:pass@example.com"}`, `{"site_url":"http://example.com"}`} {
		config, _ := sdk.NewConfig([]byte(body))
		if _, err := readSettings(config); err == nil {
			t.Fatal("accepted", body)
		}
	}
	for _, test := range []struct {
		code      int
		body      string
		permanent bool
		errorCode string
	}{{500, "oops", false, "TELEGRAM_SERVER_ERROR"}, {200, "invalid", false, "TELEGRAM_INVALID_RESPONSE"}, {200, `{"ok":false,"error_code":400}`, true, "TELEGRAM_REJECTED_400"}, {302, "", true, "TELEGRAM_HTTP_302"}} {
		t.Run(fmt.Sprint(test.code, test.errorCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			p := New()
			p.telegramURL = server.URL
			permanent, _, code := p.sendTelegram(context.Background(), "123:abcdefghijk", "1", "text", "https://example.com")
			if permanent != test.permanent || code != test.errorCode {
				t.Fatal(permanent, code)
			}
		})
	}
	h := plugintest.New(t)
	p := start(t, h)
	if err := p.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	request(t, h, p, user, "GET", "/tickets", nil, 503)
	if err := p.Health(context.Background()); err == nil {
		t.Fatal("database still open")
	}
}
