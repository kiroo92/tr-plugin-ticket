package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
	_ "modernc.org/sqlite"
)

type settings struct {
	Enabled bool
	Token   string
	ChatID  string
	SiteURL string
}

type Plugin struct {
	db            *sql.DB
	configuration atomic.Pointer[settings]
	client        *http.Client
	telegramURL   string
	root          context.Context
	cancel        context.CancelFunc
	lifecycle     sync.Mutex
	closing       bool
	work          sync.WaitGroup
	taskMu        sync.Mutex
}

func New() *Plugin {
	return &Plugin{client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, telegramURL: "https://api.telegram.org"}
}

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{10,}$`)
var chatPattern = regexp.MustCompile(`^-?[0-9]{1,20}$`)

func readSettings(config sdk.Config) (*settings, error) {
	value := &settings{Enabled: config.Bool("telegram_enabled"), Token: config.Secret("bot_token"), ChatID: strings.TrimSpace(config.String("chat_id")), SiteURL: strings.TrimRight(strings.TrimSpace(config.String("site_url")), "/")}
	if value.SiteURL != "" {
		parsed, err := url.Parse(value.SiteURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return nil, errors.New("网站地址须为不含路径或账号的 HTTPS 地址")
		}
	}
	if value.Enabled && (!tokenPattern.MatchString(value.Token) || !chatPattern.MatchString(value.ChatID) || value.SiteURL == "") {
		return nil, errors.New("启用 Telegram 通知需要有效的 Bot Token、数字 Chat ID 和网站 HTTPS 地址")
	}
	return value, nil
}

func (p *Plugin) Init(ctx context.Context, env *sdk.Env) error {
	if env == nil || strings.TrimSpace(env.DataDir) == "" {
		return errors.New("宿主未提供持久化 DataDir，请使用支持该能力的 TokenRouter v0.7.3 或更新版本")
	}
	if !filepath.IsAbs(env.DataDir) {
		return errors.New("DataDir 必须是宿主提供的绝对路径")
	}
	info, err := os.Stat(env.DataDir)
	if err != nil || !info.IsDir() {
		return errors.New("DataDir 不存在或不是可用目录")
	}
	config, err := readSettings(env.Config)
	if err != nil {
		return err
	}
	databasePath := filepath.Join(env.DataDir, "plugin.sqlite")
	if info, err := os.Lstat(databasePath); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("数据库路径必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查数据库: %w", err)
	}
	uriPath := filepath.ToSlash(databasePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	databaseURI := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{}
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(1)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	databaseURI.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", databaseURI.String())
	if err != nil {
		return fmt.Errorf("打开工单数据库: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	p.db = database
	if err = p.initializeStore(ctx); err != nil {
		_ = database.Close()
		p.db = nil
		return fmt.Errorf("初始化工单数据库: %w", err)
	}
	if err = os.Chmod(databasePath, 0600); err != nil {
		_ = database.Close()
		p.db = nil
		return fmt.Errorf("设置数据库权限: %w", err)
	}
	p.configuration.Store(config)
	p.root, p.cancel = context.WithCancel(context.Background())
	return nil
}

func (p *Plugin) ConfigChanged(_ context.Context, config sdk.Config) error {
	value, err := readSettings(config)
	if err != nil {
		return err
	}
	p.configuration.Store(value)
	return nil
}

func (p *Plugin) Health(ctx context.Context) error { return p.db.PingContext(ctx) }

func (p *Plugin) Shutdown(ctx context.Context) error {
	p.lifecycle.Lock()
	p.closing = true
	if p.cancel != nil {
		p.cancel()
	}
	p.lifecycle.Unlock()
	done := make(chan struct{})
	go func() { p.work.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

func (p *Plugin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", p.list)
	mux.HandleFunc("GET /tickets/{id}", p.detail)
	mux.HandleFunc("POST /tickets", p.change)
	mux.HandleFunc("POST /tickets/{id}/{action}", p.change)
	mux.HandleFunc("GET /admin/tickets", p.list)
	mux.HandleFunc("GET /admin/tickets/{id}", p.detail)
	mux.HandleFunc("POST /admin/tickets/{id}/{action}", p.change)
	mux.HandleFunc("GET /admin/notifications", p.notificationStatus)
	mux.HandleFunc("POST /admin/notifications/test", p.testNotification)
	mux.HandleFunc("POST /admin/notifications/retry", p.retryNotifications)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.lifecycle.Lock()
		if p.closing {
			p.lifecycle.Unlock()
			respondError(w, 503, "PLUGIN_STOPPING", "插件正在停止，请稍后重试")
			return
		}
		p.work.Add(1)
		p.lifecycle.Unlock()
		defer p.work.Done()
		w.Header().Set("Cache-Control", "no-store")
		caller, ok := sdk.CallerFrom(r.Context())
		if !ok || caller.UserID <= 0 {
			respondError(w, 401, "UNAUTHORIZED", "请登录后操作")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/admin/") && caller.Role != "admin" {
			respondError(w, 403, "FORBIDDEN", "需要管理员权限")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func respondError(w http.ResponseWriter, status int, code, message string) {
	sdk.WriteJSON(w, status, map[string]string{"code": code, "message": message})
}
