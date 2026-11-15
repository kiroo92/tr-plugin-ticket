package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

const (
	maxLookupIDs     = 100
	userPlaceholder  = "用户 #%d"
	staffPlaceholder = "管理员 #%d"
)

type storedMessage struct {
	Message
	ActorID int64
}

func placeholderName(actorID int64, role string) string {
	if role == "staff" {
		return fmt.Sprintf(staffPlaceholder, actorID)
	}
	return fmt.Sprintf(userPlaceholder, actorID)
}

func (p *Plugin) nameFor(ctx context.Context, caller sdk.Caller) string {
	if name := strings.TrimSpace(caller.Username); name != "" {
		return name
	}
	if email, ok := p.resolveEmails(ctx, []int64{caller.UserID})[caller.UserID]; ok {
		return email
	}
	return ""
}

func (p *Plugin) resolveEmails(ctx context.Context, ids []int64) map[int64]string {
	result := map[int64]string{}
	if p.host == nil {
		return result
	}
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
		if len(unique) == maxLookupIDs {
			break
		}
	}
	if len(unique) == 0 {
		return result
	}
	response, err := p.host.Users.LookupIDs(ctx, unique)
	if err != nil {
		return result
	}
	for _, account := range response.GetUsers() {
		if account == nil {
			continue
		}
		if email := strings.TrimSpace(account.GetEmail()); email != "" {
			result[account.GetId()] = email
		}
	}
	return result
}

func (p *Plugin) fillMissingNames(ctx context.Context, tickets []Ticket, messages []storedMessage) {
	ids := make([]int64, 0, len(tickets)+len(messages))
	for _, ticket := range tickets {
		if strings.TrimSpace(ticket.Username) == "" {
			ids = append(ids, ticket.UserID)
		}
	}
	for _, message := range messages {
		if strings.TrimSpace(message.Author) == "" {
			ids = append(ids, message.ActorID)
		}
	}
	if len(ids) == 0 {
		return
	}
	emails := p.resolveEmails(ctx, ids)
	if len(emails) == 0 {
		return
	}
	for index := range tickets {
		if strings.TrimSpace(tickets[index].Username) != "" {
			continue
		}
		email, ok := emails[tickets[index].UserID]
		if !ok {
			continue
		}
		tickets[index].Username = email
		if _, err := p.db.ExecContext(ctx, "UPDATE tickets SET username=? WHERE id=? AND username=''", email, tickets[index].ID); err != nil {
			return
		}
	}
	for index := range messages {
		if strings.TrimSpace(messages[index].Author) != "" {
			continue
		}
		email, ok := emails[messages[index].ActorID]
		if !ok {
			continue
		}
		messages[index].Author = email
		if _, err := p.db.ExecContext(ctx, "UPDATE messages SET author=? WHERE id=? AND author=''", email, messages[index].ID); err != nil {
			return
		}
	}
}

func fillPlaceholders(tickets []Ticket, messages []storedMessage) {
	for index := range tickets {
		if strings.TrimSpace(tickets[index].Username) == "" {
			tickets[index].Username = placeholderName(tickets[index].UserID, "user")
		}
	}
	for index := range messages {
		if strings.TrimSpace(messages[index].Author) != "" {
			continue
		}
		role := messages[index].Role
		if role == "event" {
			continue
		}
		messages[index].Author = placeholderName(messages[index].ActorID, role)
	}
}
