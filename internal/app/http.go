package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

var requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)

func formatID(id int64) string          { return strconv.FormatInt(id, 10) }
func adminRequest(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/admin/") }
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		respondError(w, 400, "INVALID_BODY", "请求格式或大小无效")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		respondError(w, 400, "INVALID_BODY", "请求必须只包含一个 JSON 对象")
		return false
	}
	return true
}
func (p *Plugin) databaseError(w http.ResponseWriter) {
	respondError(w, 503, "STORAGE_UNAVAILABLE", "工单存储暂时不可用，请保留内容并使用相同请求重试")
}

func (p *Plugin) list(w http.ResponseWriter, r *http.Request) {
	caller, _ := sdk.CallerFrom(r.Context())
	admin := adminRequest(r)
	filter := r.URL.Query().Get("status")
	if !slices.Contains([]string{"", "all", "open", "resolved", "pending"}, filter) || (filter == "pending" && !admin) {
		respondError(w, 400, "INVALID_FILTER", "工单筛选无效")
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 200 {
		respondError(w, 400, "INVALID_SEARCH", "搜索内容最多 200 字")
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 || offset > 1000000 {
		respondError(w, 400, "INVALID_PAGE", "分页参数无效")
		return
	}
	where := "1=1"
	args := []any{}
	if !admin {
		where += " AND user_id=?"
		args = append(args, caller.UserID)
	}
	var total, opened, resolved, pending int
	err = p.db.QueryRowContext(r.Context(), "SELECT count(*),coalesce(sum(status='open'),0),coalesce(sum(status='resolved'),0),coalesce(sum(status='open' AND awaiting_admin=1),0) FROM tickets WHERE "+where, args...).Scan(&total, &opened, &resolved, &pending)
	if err != nil {
		p.databaseError(w)
		return
	}
	if filter == "pending" {
		where += " AND status='open' AND awaiting_admin=1"
	} else if filter == "open" || filter == "resolved" {
		where += " AND status=?"
		args = append(args, filter)
	}
	if search != "" {
		pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search) + "%"
		where += " AND (title LIKE ? ESCAPE '\\' OR cast(id AS TEXT) LIKE ? ESCAPE '\\' OR EXISTS(SELECT 1 FROM messages WHERE ticket_id=tickets.id AND body LIKE ? ESCAPE '\\'))"
		args = append(args, pattern, pattern, pattern)
	}
	args = append(args, 31, offset)
	rows, err := p.db.QueryContext(r.Context(), "SELECT "+ticketColumns+" FROM tickets WHERE "+where+" ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		p.databaseError(w)
		return
	}
	defer rows.Close()
	result := []Ticket{}
	for rows.Next() {
		value, err := scanTicket(rows)
		if err != nil {
			p.databaseError(w)
			return
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		p.databaseError(w)
		return
	}
	if err = rows.Close(); err != nil {
		p.databaseError(w)
		return
	}
	next := -1
	if len(result) > 30 {
		result = result[:30]
		next = offset + 30
	}
	p.fillMissingNames(r.Context(), result, nil)
	fillPlaceholders(result, nil)
	counts := map[string]int{"all": total, "open": opened, "resolved": resolved}
	if admin {
		counts["pending"] = pending
	}
	sdk.WriteJSON(w, 200, map[string]any{"rows": result, "next_offset": next, "counts": counts})
}

func (p *Plugin) detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(w, 404, "NOT_FOUND", "工单不存在")
		return
	}
	caller, _ := sdk.CallerFrom(r.Context())
	ticket, err := scanTicket(p.db.QueryRowContext(r.Context(), "SELECT "+ticketColumns+" FROM tickets WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!adminRequest(r) && ticket.UserID != caller.UserID) {
		respondError(w, 404, "NOT_FOUND", "工单不存在")
		return
	}
	if err != nil {
		p.databaseError(w)
		return
	}
	before := int64(0)
	if r.URL.Query().Get("before") != "" {
		before, err = strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if err != nil || before <= 0 {
			respondError(w, 400, "INVALID_PAGE", "分页参数无效")
			return
		}
	}
	rows, err := p.db.QueryContext(r.Context(), "SELECT id,actor_id,author,role,body,created_at FROM messages WHERE ticket_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT 51", id, before, before)
	if err != nil {
		p.databaseError(w)
		return
	}
	defer rows.Close()
	stored := []storedMessage{}
	for rows.Next() {
		var message storedMessage
		if err = rows.Scan(&message.ID, &message.ActorID, &message.Author, &message.Role, &message.Body, &message.CreatedAt); err != nil {
			p.databaseError(w)
			return
		}
		stored = append(stored, message)
	}
	if err = rows.Err(); err != nil {
		p.databaseError(w)
		return
	}
	if err = rows.Close(); err != nil {
		p.databaseError(w)
		return
	}
	next := int64(0)
	if len(stored) > 50 {
		stored = stored[:50]
		next = stored[49].ID
	}
	tickets := []Ticket{ticket}
	p.fillMissingNames(r.Context(), tickets, stored)
	fillPlaceholders(tickets, stored)
	ticket = tickets[0]
	messages := make([]Message, len(stored))
	for index, message := range stored {
		messages[index] = message.Message
	}
	slices.Reverse(messages)
	sdk.WriteJSON(w, 200, map[string]any{"ticket": ticket, "messages": messages, "next_before": next})
}

func (p *Plugin) change(w http.ResponseWriter, r *http.Request) {
	caller, _ := sdk.CallerFrom(r.Context())
	var input operation
	if !decodeBody(w, r, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if !requestPattern.MatchString(input.RequestID) {
		respondError(w, 400, "INVALID_REQUEST_ID", "需要有效的请求标识")
		return
	}
	action := r.PathValue("action")
	id := int64(0)
	var err error
	if action == "" {
		action = "create"
	} else {
		id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			respondError(w, 404, "NOT_FOUND", "工单不存在")
			return
		}
	}
	if !slices.Contains([]string{"create", "reply", "resolve", "reopen"}, action) {
		respondError(w, 404, "NOT_FOUND", "操作不存在")
		return
	}
	if action == "create" && (!utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) < 1 || utf8.RuneCountInString(input.Title) > 80 || !slices.Contains([]string{"API 调用", "充值与账单", "福利活动", "账户与权限", "其他问题"}, input.Category) || !slices.Contains([]string{"普通", "紧急"}, input.Priority)) {
		respondError(w, 400, "INVALID_TICKET", "请填写标题、问题类型和优先级")
		return
	}
	if (action == "create" || action == "reply") && (!utf8.ValidString(input.Body) || utf8.RuneCountInString(input.Body) < 1 || utf8.RuneCountInString(input.Body) > 4000) {
		respondError(w, 400, "INVALID_MESSAGE", "正文需要 1–4000 字")
		return
	}
	fingerprintJSON, _ := json.Marshal(struct {
		Action string
		ID     int64
		Admin  bool
		Input  operation
	}{action, id, adminRequest(r), input})
	digest := sha256.Sum256(fingerprintJSON)
	fingerprint := hex.EncodeToString(digest[:])
	tx, err := p.db.BeginTx(r.Context(), nil)
	if err != nil {
		p.databaseError(w)
		return
	}
	defer tx.Rollback()
	var previousFingerprint string
	var previousID int64
	err = tx.QueryRowContext(r.Context(), "SELECT fingerprint,ticket_id FROM operations WHERE actor_id=? AND request_id=?", caller.UserID, input.RequestID).Scan(&previousFingerprint, &previousID)
	if err == nil {
		if previousFingerprint != fingerprint {
			respondError(w, 409, "IDEMPOTENCY_CONFLICT", "请求标识已被其他操作使用")
			return
		}
		sdk.WriteJSON(w, 200, map[string]int64{"id": previousID})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		p.databaseError(w)
		return
	}
	now := time.Now().UnixMilli()
	author := p.nameFor(r.Context(), caller)
	body, role, event := input.Body, "user", ""
	if action == "create" {
		result, err := tx.ExecContext(r.Context(), "INSERT INTO tickets(user_id,username,title,category,priority,status,awaiting_admin,created_at,updated_at) VALUES(?,?,?,?,?,'open',1,?,?)", caller.UserID, author, input.Title, input.Category, input.Priority, now, now)
		if err != nil {
			p.databaseError(w)
			return
		}
		id, err = result.LastInsertId()
		if err != nil {
			p.databaseError(w)
			return
		}
		event = "新工单"
	} else {
		ticket, err := scanTicket(tx.QueryRowContext(r.Context(), "SELECT "+ticketColumns+" FROM tickets WHERE id=?", id))
		if errors.Is(err, sql.ErrNoRows) || err == nil && (!adminRequest(r) && ticket.UserID != caller.UserID) {
			respondError(w, 404, "NOT_FOUND", "工单不存在")
			return
		}
		if err != nil {
			p.databaseError(w)
			return
		}
		input.Category = ticket.Category
		status, awaiting := ticket.Status, ticket.AwaitingAdmin
		switch action {
		case "reply":
			if status != "open" {
				respondError(w, 409, "TICKET_RESOLVED", "请先重新打开工单再回复")
				return
			}
			awaiting = !adminRequest(r)
			if adminRequest(r) {
				role = "staff"
			} else {
				event = "用户新回复"
			}
		case "resolve":
			if status != "open" {
				respondError(w, 409, "ALREADY_RESOLVED", "工单已解决")
				return
			}
			status = "resolved"
			awaiting = false
			body = "工单已标记为已解决"
			role = "event"
		case "reopen":
			if status != "resolved" {
				respondError(w, 409, "ALREADY_OPEN", "工单已经打开")
				return
			}
			status = "open"
			awaiting = !adminRequest(r)
			body = "工单已重新打开"
			role = "event"
			if !adminRequest(r) {
				event = "用户重新打开"
			}
		}
		if _, err = tx.ExecContext(r.Context(), "UPDATE tickets SET status=?,awaiting_admin=?,updated_at=? WHERE id=?", status, awaiting, now, id); err != nil {
			p.databaseError(w)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO messages(ticket_id,actor_id,author,role,body,created_at) VALUES(?,?,?,?,?,?)", id, caller.UserID, author, role, body, now); err != nil {
		p.databaseError(w)
		return
	}
	if event != "" {
		if err = p.enqueue(r.Context(), tx, id, input.Category, event); err != nil {
			p.databaseError(w)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO operations(actor_id,request_id,fingerprint,ticket_id) VALUES(?,?,?,?)", caller.UserID, input.RequestID, fingerprint, id); err != nil {
		p.databaseError(w)
		return
	}
	if err = tx.Commit(); err != nil {
		p.databaseError(w)
		return
	}
	sdk.WriteJSON(w, 200, map[string]int64{"id": id})
}
