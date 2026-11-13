package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

func do(t *testing.T, method, url, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestStore_Create_DedupeAndValidation_R54(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	s := notify.Store{}
	n := notify.Notification{UserID: "intern.anna", Kind: notify.KindReminder, Title: "t", Body: "b", ChecklistItemID: "CL-05", DedupeKey: "k1"}
	for i := 0; i < 3; i++ {
		if err := s.Create(ctx, pool, n); err != nil {
			t.Fatal(err)
		}
	}
	var cnt int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND dedupe_key='k1'`).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("dedupe: count=%d err=%v", cnt, err)
	}
	// тот же ключ у другого пользователя — другое уведомление
	n2 := n
	n2.UserID = "intern.ivan"
	created, err := s.CreateIfNew(ctx, pool, n2)
	if err != nil || !created {
		t.Fatalf("other user: created=%v err=%v", created, err)
	}
	// без ключа дедупликации: вставляется всегда
	for i := 0; i < 2; i++ {
		if err := s.Create(ctx, pool, notify.Notification{UserID: "intern.anna", Kind: notify.KindStatusChange, Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna'`).Scan(&cnt); err != nil || cnt != 3 {
		t.Fatalf("count=%d err=%v", cnt, err)
	}
	for _, bad := range []notify.Notification{
		{UserID: "intern.anna", Kind: "weird", Title: "t"},
		{UserID: "", Kind: notify.KindReminder, Title: "t"},
		{UserID: "intern.anna", Kind: notify.KindReminder, Title: ""},
	} {
		if err := s.Create(ctx, pool, bad); !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("%+v: err=%v, want ErrInvalid", bad, err)
		}
	}
	// неизвестный пользователь нарушает внешний ключ
	if err := s.Create(ctx, pool, notify.Notification{UserID: "nobody", Kind: notify.KindReminder, Title: "t"}); err == nil {
		t.Error("expected FK error")
	}
	// работает внутри транзакции, которая откатывается
	tx, _ := pool.Begin(ctx)
	_ = s.Create(ctx, tx, notify.Notification{UserID: "intern.ivan", Kind: notify.KindOverdue, Title: "rolled back"})
	_ = tx.Rollback(ctx)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE title='rolled back'`).Scan(&cnt); err != nil || cnt != 0 {
		t.Fatalf("rollback: count=%d err=%v", cnt, err)
	}
}

func TestNotifications_ListAndRead_R54_R71(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	notify.New(pool, nil).Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	defer srv.Close()
	tok := func(user, role string) string {
		s, _ := authn.Issue(secret, authn.Claims{Sub: user, Role: role, TeamID: "delivery"}, time.Hour)
		return s
	}
	s := notify.Store{}
	_ = s.Create(ctx, pool, notify.Notification{UserID: "intern.anna", Kind: notify.KindReminder, Title: "old", Body: "body", ChecklistItemID: "CL-05"})
	time.Sleep(5 * time.Millisecond)
	_ = s.Create(ctx, pool, notify.Notification{UserID: "intern.anna", Kind: notify.KindOverdue, Title: "new"})
	_ = s.Create(ctx, pool, notify.Notification{UserID: "intern.ivan", Kind: notify.KindReminder, Title: "ivan's"})

	resp, body := do(t, "GET", srv.URL+"/api/notifications", tok("intern.anna", "intern"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0]["title"] != "new" || list[1]["title"] != "old" {
		t.Fatalf("order/own filter: %v", list)
	}
	if list[1]["body"] != "body" || list[1]["checklist_item_id"] != "CL-05" || list[1]["read"] != false || list[0]["body"] != nil {
		t.Fatalf("fields: %v", list)
	}
	id := list[0]["id"].(string)

	// чужой пользователь не может пометить его прочитанным и не может узнать, существует ли оно
	resp, _ = do(t, "POST", srv.URL+"/api/notifications/"+id+"/read", tok("intern.ivan", "intern"))
	if resp.StatusCode != 403 {
		t.Fatalf("foreign: %d", resp.StatusCode)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/notifications/00000000-0000-4000-8000-000000000000/read", tok("intern.ivan", "intern"))
	if resp.StatusCode != 403 {
		t.Fatalf("unknown: %d", resp.StatusCode)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/notifications/not-a-uuid/read", tok("intern.ivan", "intern"))
	if resp.StatusCode != 404 {
		t.Fatalf("malformed: %d", resp.StatusCode)
	}
	for i := 0; i < 2; i++ { // идемпотентно
		resp, _ = do(t, "POST", srv.URL+"/api/notifications/"+id+"/read", tok("intern.anna", "intern"))
		if resp.StatusCode != 204 {
			t.Fatalf("read: %d", resp.StatusCode)
		}
	}
	_, body = do(t, "GET", srv.URL+"/api/notifications", tok("intern.anna", "intern"))
	_ = json.Unmarshal(body, &list)
	if list[0]["read"] != true || list[1]["read"] != false {
		t.Fatalf("read flags: %v", list)
	}
	// пустой список — [], а не null
	_, body = do(t, "GET", srv.URL+"/api/notifications", tok("lead.maria", "lead"))
	if string(body) != "[]" {
		t.Fatalf("empty: %s", body)
	}
	resp, _ = do(t, "GET", srv.URL+"/api/notifications", "")
	if resp.StatusCode != 401 {
		t.Fatalf("anon: %d", resp.StatusCode)
	}
}

func TestHandler_NoClaims(t *testing.T) {
	mux := http.NewServeMux()
	notify.New(nil, nil).Register(mux) // без middleware authn: обработчики должны отказывать сами
	for _, r := range []*http.Request{
		httptest.NewRequest("GET", "/api/notifications", nil),
		httptest.NewRequest("POST", "/api/notifications/00000000-0000-4000-8000-000000000000/read", nil),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != 401 {
			t.Errorf("%s: %d", r.URL.Path, rec.Code)
		}
	}
}
