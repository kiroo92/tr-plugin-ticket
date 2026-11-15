package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/plugintest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

type lookupFixture struct {
	mu     sync.Mutex
	emails map[int64]string
	fail   bool
	calls  int
}

func (f *lookupFixture) hook(_ context.Context, method string, request any) (any, error, bool) {
	if method != "LookupUsers" {
		return nil, nil, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return nil, plugintest.Error(13, "HOST_UNAVAILABLE"), true
	}
	input, _ := request.(*pluginpb.LookupUsersRequest)
	response := &pluginpb.LookupUsersResponse{}
	for _, id := range input.GetIds() {
		if email, ok := f.emails[id]; ok {
			response.Users = append(response.Users, &pluginpb.User{Id: id, Email: email})
		}
	}
	return response, nil, true
}

func useLookup(t *testing.T, h *plugintest.Harness, fixture *lookupFixture) *lookupFixture {
	t.Helper()
	h.Server.Hook = fixture.hook
	return fixture
}

func named(caller sdk.Caller, name string) sdk.Caller {
	caller.Username = name
	return caller
}

func TestMissingUsernameFallsBackToEmail(t *testing.T) {
	h := plugintest.New(t)
	useLookup(t, h, &lookupFixture{emails: map[int64]string{11: "alice@example.com", 1: "admin@example.com"}})
	p := start(t, h)
	anonymous := named(user, "")

	id := create(t, h, p, anonymous, "email-fallback-0001")
	ticket, messages := getDetail(t, h, p, anonymous, fmt.Sprintf("/tickets/%d", id))
	if ticket.Username != "alice@example.com" {
		t.Fatalf("ticket username %q", ticket.Username)
	}
	if len(messages) != 1 || messages[0].Author != "alice@example.com" {
		t.Fatalf("message author %+v", messages)
	}
	if stored := scalarText(t, p, "SELECT username FROM tickets WHERE id="+formatID(id)); stored != "alice@example.com" {
		t.Fatalf("stored username %q", stored)
	}

	list := request(t, h, p, named(admin, ""), "GET", "/admin/tickets", nil, 200)
	var listing struct{ Rows []Ticket }
	if err := json.Unmarshal(list.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Rows) != 1 || listing.Rows[0].Username != "alice@example.com" {
		t.Fatalf("admin list %+v", listing.Rows)
	}

	request(t, h, p, anonymous, "POST", fmt.Sprintf("/tickets/%d/reply", id), operation{RequestID: "email-fallback-0002", Body: "补充"}, 200)
	_, messages = getDetail(t, h, p, named(admin, ""), fmt.Sprintf("/admin/tickets/%d", id))
	if len(messages) != 2 || messages[1].Author != "alice@example.com" {
		t.Fatalf("reply author %+v", messages)
	}
}

func TestMissingUsernameWithoutEmailUsesPlaceholder(t *testing.T) {
	h := plugintest.New(t)
	useLookup(t, h, &lookupFixture{emails: map[int64]string{}})
	p := start(t, h)
	anonymous := named(user, "")
	id := create(t, h, p, anonymous, "placeholder-000001")
	ticket, messages := getDetail(t, h, p, anonymous, fmt.Sprintf("/tickets/%d", id))
	if ticket.Username != "用户 #11" {
		t.Fatalf("ticket username %q", ticket.Username)
	}
	if len(messages) != 1 || messages[0].Author != "用户 #11" {
		t.Fatalf("message author %+v", messages)
	}
}

func TestLookupFailureDoesNotBreakRequests(t *testing.T) {
	h := plugintest.New(t)
	useLookup(t, h, &lookupFixture{emails: map[int64]string{11: "alice@example.com"}, fail: true})
	p := start(t, h)
	anonymous := named(user, "")
	id := create(t, h, p, anonymous, "lookup-failure-001")
	ticket, messages := getDetail(t, h, p, anonymous, fmt.Sprintf("/tickets/%d", id))
	if ticket.Username != "用户 #11" || len(messages) != 1 || messages[0].Author != "用户 #11" {
		t.Fatalf("unexpected fallback %+v %+v", ticket, messages)
	}
}

func TestStoredEmptyUsernameIsBackfilled(t *testing.T) {
	h := plugintest.New(t)
	fixture := useLookup(t, h, &lookupFixture{emails: map[int64]string{}})
	p := start(t, h)
	id := create(t, h, p, user, "backfill-request1")
	if _, err := p.db.Exec("UPDATE tickets SET username='' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Exec("UPDATE messages SET author='' WHERE ticket_id=?", id); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.emails[11] = "alice@example.com"
	fixture.mu.Unlock()

	ticket, messages := getDetail(t, h, p, user, fmt.Sprintf("/tickets/%d", id))
	if ticket.Username != "alice@example.com" || messages[0].Author != "alice@example.com" {
		t.Fatalf("backfill failed %+v %+v", ticket, messages)
	}
	if stored := scalarText(t, p, "SELECT username FROM tickets WHERE id="+formatID(id)); stored != "alice@example.com" {
		t.Fatalf("stored username %q", stored)
	}
	if stored := scalarText(t, p, "SELECT author FROM messages WHERE ticket_id="+formatID(id)+" LIMIT 1"); stored != "alice@example.com" {
		t.Fatalf("stored author %q", stored)
	}
}

func TestLookupBatchesAndCaps(t *testing.T) {
	h := plugintest.New(t)
	fixture := useLookup(t, h, &lookupFixture{emails: map[int64]string{}})
	p := start(t, h)
	ids := make([]int64, 0, 120)
	for index := int64(1); index <= 120; index++ {
		ids = append(ids, index)
		fixture.emails[index] = fmt.Sprintf("user%d@example.com", index)
	}
	resolved := p.resolveEmails(context.Background(), append(ids, ids...))
	if len(resolved) != maxLookupIDs {
		t.Fatalf("resolved %d", len(resolved))
	}
	fixture.mu.Lock()
	calls := fixture.calls
	fixture.mu.Unlock()
	if calls != 1 {
		t.Fatalf("lookup calls %d", calls)
	}
}

func TestNamedCallerSkipsLookup(t *testing.T) {
	h := plugintest.New(t)
	fixture := useLookup(t, h, &lookupFixture{emails: map[int64]string{11: "alice@example.com"}})
	p := start(t, h)
	id := create(t, h, p, user, "named-caller-0001")
	ticket, _ := getDetail(t, h, p, user, fmt.Sprintf("/tickets/%d", id))
	if ticket.Username != "alice" {
		t.Fatalf("username %q", ticket.Username)
	}
	fixture.mu.Lock()
	calls := fixture.calls
	fixture.mu.Unlock()
	if calls != 0 {
		t.Fatalf("unexpected lookup calls %d", calls)
	}
}

func scalarText(t *testing.T, p *Plugin, query string) string {
	t.Helper()
	var value string
	if err := p.db.QueryRow(query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
