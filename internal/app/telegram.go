package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

type notice struct {
	ID        int64  `json:"id"`
	TicketID  int64  `json:"ticket_id"`
	State     string `json:"state"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
	CreatedAt int64  `json:"created_at"`
}

func (p *Plugin) notificationStatus(w http.ResponseWriter, r *http.Request) {
	config := p.configuration.Load()
	rows, err := p.db.QueryContext(r.Context(), "SELECT id,coalesce(ticket_id,0),state,attempts,last_error,created_at FROM notification_outbox ORDER BY id DESC LIMIT 20")
	if err != nil {
		p.databaseError(w)
		return
	}
	recent := []notice{}
	for rows.Next() {
		var item notice
		if err = rows.Scan(&item.ID, &item.TicketID, &item.State, &item.Attempts, &item.LastError, &item.CreatedAt); err != nil {
			rows.Close()
			p.databaseError(w)
			return
		}
		recent = append(recent, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		p.databaseError(w)
		return
	}
	var pending, failed int
	if err = p.db.QueryRowContext(r.Context(), "SELECT coalesce(sum(state='pending'),0),coalesce(sum(state='failed'),0) FROM notification_outbox").Scan(&pending, &failed); err != nil {
		p.databaseError(w)
		return
	}
	sdk.WriteJSON(w, 200, map[string]any{"enabled": config.Enabled, "configured": config.Token != "" && config.ChatID != "" && config.SiteURL != "", "pending": pending, "failed": failed, "recent": recent})
}

func (p *Plugin) testNotification(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestID string `json:"request_id"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if !requestPattern.MatchString(input.RequestID) {
		respondError(w, 400, "INVALID_REQUEST_ID", "需要有效的请求标识")
		return
	}
	config := p.configuration.Load()
	if !config.Enabled {
		respondError(w, 409, "NOTIFICATIONS_DISABLED", "请先在宿主插件配置启用 Telegram 通知")
		return
	}
	caller, _ := sdk.CallerFrom(r.Context())
	_, err := p.db.ExecContext(r.Context(), "INSERT INTO notification_outbox(chat_id,text,link,next_at,created_at,request_key) VALUES(?,?,?,?,?,?) ON CONFLICT(request_key) DO NOTHING", config.ChatID, "支持工单 · 测试通知\n通知通道可用，请登录管理端处理工单。", config.SiteURL+"/admin/ext/tokenflux.ticket", time.Now().Unix(), time.Now().Unix(), formatID(caller.UserID)+":"+input.RequestID)
	if err != nil {
		p.databaseError(w)
		return
	}
	sdk.WriteJSON(w, 202, map[string]bool{"queued": true})
}

func (p *Plugin) retryNotifications(w http.ResponseWriter, r *http.Request) {
	if !p.configuration.Load().Enabled {
		respondError(w, 409, "NOTIFICATIONS_DISABLED", "请先启用通知")
		return
	}
	if _, err := p.db.ExecContext(r.Context(), "UPDATE notification_outbox SET state='pending',attempts=0,next_at=? WHERE state='failed'", time.Now().Unix()); err != nil {
		p.databaseError(w)
		return
	}
	sdk.WriteJSON(w, 200, map[string]bool{"queued": true})
}

func (p *Plugin) RunTask(ctx context.Context, name string, _ time.Time) error {
	if name != "deliver-notifications" {
		return fmt.Errorf("unknown task %q", name)
	}
	p.lifecycle.Lock()
	if p.closing {
		p.lifecycle.Unlock()
		return nil
	}
	p.work.Add(1)
	p.lifecycle.Unlock()
	defer p.work.Done()
	if !p.taskMu.TryLock() {
		return nil
	}
	defer p.taskMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.root, cancel)
	defer stop()
	defer cancel()
	for range 5 {
		config := p.configuration.Load()
		if !config.Enabled {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var id int64
		var chatID, text, link string
		var attempts int
		err := p.db.QueryRowContext(ctx, "SELECT id,chat_id,text,link,attempts FROM notification_outbox WHERE state='pending' AND next_at<=? ORDER BY id LIMIT 1", time.Now().Unix()).Scan(&id, &chatID, &text, &link, &attempts)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = p.db.ExecContext(ctx, "UPDATE notification_outbox SET attempts=attempts+1,next_at=? WHERE id=?", time.Now().Add(time.Minute).Unix(), id); err != nil {
			return err
		}
		permanent, delay, code := p.sendTelegram(ctx, config.Token, chatID, text, link)
		if code == "" {
			if _, err = p.db.ExecContext(ctx, "UPDATE notification_outbox SET state='sent',sent_at=?,last_error='' WHERE id=?", time.Now().Unix(), id); err != nil {
				return err
			}
			continue
		}
		state := "pending"
		if permanent || attempts+1 >= 10 {
			state = "failed"
		}
		if delay == 0 {
			delay = time.Duration(1<<min(attempts, 8)) * 30 * time.Second
		}
		if _, err = p.db.ExecContext(ctx, "UPDATE notification_outbox SET state=?,next_at=?,last_error=? WHERE id=?", state, time.Now().Add(delay).Unix(), code, id); err != nil {
			return err
		}
		if code == "TELEGRAM_RATE_LIMIT" {
			return nil
		}
	}
	return nil
}

func (p *Plugin) sendTelegram(ctx context.Context, token, chatID, text, link string) (bool, time.Duration, string) {
	payload := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}, "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "查看工单", "url": link}}}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return true, 0, "INVALID_NOTIFICATION"
	}
	request, err := http.NewRequestWithContext(ctx, "POST", p.telegramURL+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return true, 0, "INVALID_TELEGRAM_CONFIGURATION"
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return false, 0, "TELEGRAM_NETWORK_ERROR"
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return false, 0, "TELEGRAM_INVALID_RESPONSE"
	}
	var result struct {
		OK         bool `json:"ok"`
		ErrorCode  int  `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	parseErr := json.Unmarshal(data, &result)
	if response.StatusCode == 429 || result.ErrorCode == 429 {
		return false, time.Duration(max(1, min(result.Parameters.RetryAfter, 86400))) * time.Second, "TELEGRAM_RATE_LIMIT"
	}
	if response.StatusCode >= 500 {
		return false, 0, "TELEGRAM_SERVER_ERROR"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return true, 0, "TELEGRAM_HTTP_" + strconv.Itoa(response.StatusCode)
	}
	if parseErr != nil {
		return false, 0, "TELEGRAM_INVALID_RESPONSE"
	}
	if result.OK {
		return false, 0, ""
	}
	if result.ErrorCode >= 500 {
		return false, 0, "TELEGRAM_SERVER_ERROR"
	}
	if result.ErrorCode >= 400 {
		return true, 0, "TELEGRAM_REJECTED_" + strconv.Itoa(result.ErrorCode)
	}
	return false, 0, "TELEGRAM_INVALID_RESPONSE"
}
