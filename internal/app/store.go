package app

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Plugin) initializeStore(ctx context.Context) error {
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var version int
	if err := p.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("数据库版本高于当前插件支持范围，请恢复兼容版本，禁止降级改写")
	}
	if version == 1 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
 CREATE TABLE tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  username TEXT NOT NULL,
  title TEXT NOT NULL,
  category TEXT NOT NULL,
  priority TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('open','resolved')),
  awaiting_admin INTEGER NOT NULL CHECK(awaiting_admin IN (0,1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
 );
 CREATE INDEX tickets_user_updated ON tickets(user_id,updated_at DESC,id DESC);
 CREATE INDEX tickets_status_updated ON tickets(status,awaiting_admin,updated_at DESC,id DESC);
 CREATE TABLE messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id),
  actor_id INTEGER NOT NULL,
  author TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('user','staff','event')),
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL
 );
 CREATE INDEX messages_ticket ON messages(ticket_id,id DESC);
 CREATE TABLE operations (
  actor_id INTEGER NOT NULL,
  request_id TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id),
  PRIMARY KEY(actor_id,request_id)
 );
 CREATE TABLE notification_outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER REFERENCES tickets(id),
  chat_id TEXT NOT NULL,
  text TEXT NOT NULL,
  link TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sent','failed')),
  attempts INTEGER NOT NULL DEFAULT 0,
  next_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  sent_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  request_key TEXT UNIQUE
 );
 CREATE INDEX notification_due ON notification_outbox(state,next_at,id);
 PRAGMA user_version=1;
 `)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type Ticket struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"user_id"`
	Username      string `json:"username"`
	Title         string `json:"title"`
	Category      string `json:"category"`
	Priority      string `json:"priority"`
	Status        string `json:"status"`
	AwaitingAdmin bool   `json:"awaiting_admin"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const ticketColumns = "id,user_id,username,title,category,priority,status,awaiting_admin,created_at,updated_at"

func scanTicket(row interface{ Scan(...any) error }) (Ticket, error) {
	var value Ticket
	err := row.Scan(&value.ID, &value.UserID, &value.Username, &value.Title, &value.Category, &value.Priority, &value.Status, &value.AwaitingAdmin, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

type Message struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Role      string `json:"role"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

type operation struct {
	RequestID string `json:"request_id"`
	Title     string `json:"title"`
	Category  string `json:"category"`
	Priority  string `json:"priority"`
	Body      string `json:"body"`
}

func (p *Plugin) enqueue(ctx context.Context, tx *sql.Tx, ticketID int64, category, event string) error {
	config := p.configuration.Load()
	if !config.Enabled {
		return nil
	}
	text := "支持工单 · " + event + "\n#TK-" + formatID(ticketID) + " · " + category + "\n待管理员回复\n请登录管理端查看并回复。"
	link := config.SiteURL + "/admin/ext/tokenflux.ticket?ticket=" + formatID(ticketID)
	return insertNotice(ctx, tx, ticketID, config.ChatID, text, link)
}

func insertNotice(ctx context.Context, tx *sql.Tx, ticketID int64, chatID, text, link string) error {
	var ticket any = ticketID
	if ticketID == 0 {
		ticket = nil
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO notification_outbox(ticket_id,chat_id,text,link,next_at,created_at) VALUES(?,?,?,?,?,?)", ticket, chatID, text, link, time.Now().Unix(), time.Now().Unix())
	return err
}
