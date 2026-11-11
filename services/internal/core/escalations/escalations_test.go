package escalations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/escalations"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

type user struct{ id, role, team string }

var (
	anna   = user{"intern.anna", "intern", "delivery"}
	ivan   = user{"intern.ivan", "intern", "search"}
	oleg   = user{"mentor.oleg", "mentor", "delivery"}
	sergey = user{"mentor.sergey", "mentor", "search"}
	maria  = user{"lead.maria", "lead", "delivery"}
	elena  = user{"hrbp.elena", "hrbp", "people"}
)

type fakeFlags struct{ on map[string]bool }

func (f fakeFlags) Enabled(_ context.Context, key, _ string) bool { return f.on[key] }

type fakeFAQ struct {
	calls []map[string]string
	err   error
}

func (f *fakeFAQ) AddFAQ(_ context.Context, token, title, body, key string) error {
	f.calls = append(f.calls, map[string]string{"token": token, "title": title, "body": body, "key": key})
	return f.err
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *httptest.Server
	faq  *fakeFAQ
}

func setup(t *testing.T, kbFlag bool) *env {
	t.Helper()
	pool := pgtest.New(t)
	faq := &fakeFAQ{}
	h := escalations.New(pool, analytics.NewEmitter(false), fakeFlags{map[string]bool{"escalation_to_kb": kbFlag}}, faq, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	t.Cleanup(srv.Close)
	return &env{t, pool, srv, faq}
}

func (e *env) do(u user, method, path, body string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader([]byte(body)))
	if u.id != "" {
		tok, _ := authn.Issue(secret, authn.Claims{Sub: u.id, Role: u.role, TeamID: u.team}, time.Hour)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) send(u user, toRole, text string) map[string]any {
	e.t.Helper()
	code, b := e.do(u, "POST", "/api/escalations", `{"to_role":"`+toRole+`","text":`+jsonStr(text)+`}`)
	if code != 201 {
		e.t.Fatalf("send: %d %s", code, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestCreate_Targets_S2_R10(t *testing.T) {
	e := setup(t, false)
	cases := []struct {
		from     user
		toRole   string
		wantUser string
	}{
		{anna, "mentor", "mentor.oleg"},
		{ivan, "mentor", "mentor.sergey"},
		{anna, "lead", "lead.maria"},
		{ivan, "lead", "lead.dmitry"},
		{anna, "hrbp", "hrbp.elena"},
		{anna, "curator", "curator.irina"},
	}
	for _, c := range cases {
		m := e.send(c.from, c.toRole, "  Как оформить доступ?  ")
		if m["to_role"] != c.toRole || m["status"] != "open" || m["answer"] != nil || m["added_to_kb"] != false ||
			m["text"] != "Как оформить доступ?" || m["from_display_name"] == "" || m["created_at"] == nil {
			t.Fatalf("response: %v", m)
		}
		if n := e.count(`SELECT count(*) FROM core.escalations WHERE id=$1 AND from_user_id=$2 AND to_user_id=$3`, m["id"], c.from.id, c.wantUser); n != 1 {
			t.Errorf("%s -> %s: wrong addressee", c.from.id, c.toRole)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='escalation.sent'`); n != len(cases) {
		t.Errorf("escalation.sent events: %d", n)
	}
	// addressee is told, without the question text
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id='mentor.oleg' AND kind='reminder' AND body NOT LIKE '%доступ%'`); n != 1 {
		t.Errorf("addressee notification: %d", n)
	}
}

func TestCreate_Validation_R71(t *testing.T) {
	e := setup(t, false)
	var sid, foreignSid string
	_ = e.pool.QueryRow(context.Background(), `INSERT INTO core.chat_sessions (user_id) VALUES ('intern.anna') RETURNING id::text`).Scan(&sid)
	_ = e.pool.QueryRow(context.Background(), `INSERT INTO core.chat_sessions (user_id) VALUES ('intern.ivan') RETURNING id::text`).Scan(&foreignSid)
	long := strings.Repeat("я", 4001)
	cases := []struct {
		name string
		u    user
		body string
		want int
	}{
		{"with own session", anna, `{"to_role":"mentor","text":"q","session_id":"` + sid + `"}`, 201},
		{"foreign session", anna, `{"to_role":"mentor","text":"q","session_id":"` + foreignSid + `"}`, 403},
		{"unknown session", anna, `{"to_role":"mentor","text":"q","session_id":"00000000-0000-4000-8000-000000000000"}`, 403},
		{"malformed session", anna, `{"to_role":"mentor","text":"q","session_id":"x"}`, 400},
		{"bad role", anna, `{"to_role":"alumni","text":"q"}`, 400},
		{"empty text", anna, `{"to_role":"mentor","text":"   "}`, 400},
		{"long text", anna, `{"to_role":"mentor","text":"` + long + `"}`, 400},
		{"unknown field", anna, `{"to_role":"mentor","text":"q","x":1}`, 400},
		{"mentor cannot escalate", oleg, `{"to_role":"lead","text":"q"}`, 403},
		{"anonymous", user{}, `{}`, 401},
	}
	for _, c := range cases {
		if code, b := e.do(c.u, "POST", "/api/escalations", c.body); code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, code, c.want, b)
		}
	}
	// no mentor assigned -> addressee not found
	e.pool.Exec(context.Background(), `INSERT INTO core.users (id, display_name, role, team_id, cohort_start, password_hash) VALUES ('intern.solo','Соло','intern','delivery',current_date,'x')`)
	if code, _ := e.do(user{"intern.solo", "intern", "delivery"}, "POST", "/api/escalations", `{"to_role":"mentor","text":"q"}`); code != 404 {
		t.Errorf("no mentor: %d", code)
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM core.users WHERE role='hrbp'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(anna, "POST", "/api/escalations", `{"to_role":"hrbp","text":"q"}`); code != 404 {
		t.Errorf("no hrbp: %d", code)
	}
}

func TestList_OnlySenderAndAddressee_R71(t *testing.T) {
	e := setup(t, false)
	a := e.send(anna, "mentor", "вопрос Анны")
	time.Sleep(5 * time.Millisecond)
	e.send(anna, "lead", "вопрос лиду")
	e.send(ivan, "mentor", "вопрос Ивана")
	list := func(u user) []map[string]any {
		code, b := e.do(u, "GET", "/api/escalations", "")
		if code != 200 {
			t.Fatalf("%s: %d", u.id, code)
		}
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	if l := list(anna); len(l) != 2 || l[0]["text"] != "вопрос лиду" || l[1]["id"] != a["id"] {
		t.Fatalf("anna: %v", l)
	}
	if l := list(oleg); len(l) != 1 || l[0]["text"] != "вопрос Анны" || l[0]["from_display_name"] != "Анна Котова" {
		t.Fatalf("oleg: %v", l)
	}
	if l := list(sergey); len(l) != 1 || l[0]["text"] != "вопрос Ивана" {
		t.Fatalf("sergey: %v", l)
	}
	if l := list(elena); len(l) != 0 {
		t.Fatalf("elena sees %v", l)
	}
	if code, b := e.do(elena, "GET", "/api/escalations", ""); code != 200 || string(b) != "[]" {
		t.Fatalf("empty list: %d %s", code, b)
	}
	if code, _ := e.do(user{}, "GET", "/api/escalations", ""); code != 401 {
		t.Fatal("anon")
	}
}

func TestAnswer_NotifiesSender_S2_R10(t *testing.T) {
	e := setup(t, false)
	esc := e.send(anna, "mentor", "Где найти документацию?")
	id := esc["id"].(string)
	path := "/api/escalations/" + id + "/answer"

	for name, u := range map[string]user{"sender": anna, "other mentor": sergey, "lead": maria} {
		if code, _ := e.do(u, "POST", path, `{"text":"ответ"}`); code != 403 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := e.do(oleg, "POST", "/api/escalations/00000000-0000-4000-8000-000000000000/answer", `{"text":"ответ"}`); code != 403 {
		t.Errorf("unknown id: %d", code)
	}
	if code, _ := e.do(oleg, "POST", "/api/escalations/zzz/answer", `{"text":"ответ"}`); code != 404 {
		t.Errorf("malformed id: %d", code)
	}
	for _, body := range []string{`{"text":" "}`, `{}`, `{"text":"x","add_to_kb":"yes"}`} {
		if code, _ := e.do(oleg, "POST", path, body); code != 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
	code, b := e.do(oleg, "POST", path, `{"text":"  В Confluence, раздел «Команда».  "}`)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if code != 200 || m["status"] != "answered" || m["answer"] != "В Confluence, раздел «Команда»." || m["added_to_kb"] != false {
		t.Fatalf("answer: %d %s", code, b)
	}
	if code, _ := e.do(oleg, "POST", path, `{"text":"ещё раз"}`); code != 409 {
		t.Errorf("second answer: %d", code)
	}
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND kind='escalation_answer' AND dedupe_key=$1`, "escalation_answer:"+id); n != 1 {
		t.Errorf("sender notification: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='escalation.answered' AND props->>'added_to_kb'='false' AND props ? 'text' = false`); n != 1 {
		t.Errorf("escalation.answered: %d", n)
	}
	if len(e.faq.calls) != 0 {
		t.Errorf("KB must not be called without add_to_kb")
	}
	// the sender sees the answer
	_, b = e.do(anna, "GET", "/api/escalations", "")
	if !strings.Contains(string(b), `"status":"answered"`) {
		t.Errorf("sender list: %s", b)
	}
}

func TestAnswer_AddToKB_FlagAndFailure_R10(t *testing.T) {
	long := strings.Repeat("слово ", 30)
	cases := []struct {
		name      string
		flag      bool
		addToKB   bool
		faqErr    error
		wantCalls int
		wantAdded bool
	}{
		{"flag on and requested", true, true, nil, 1, true},
		{"flag off", false, true, nil, 0, false},
		{"not requested", true, false, nil, 0, false},
		{"kb unavailable keeps the answer", true, true, errors.New("mcp down"), 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, c.flag)
			e.faq.err = c.faqErr
			esc := e.send(anna, "mentor", long+"\nвторая строка")
			id := esc["id"].(string)
			body := `{"text":"Ответ наставника","add_to_kb":` + map[bool]string{true: "true", false: "false"}[c.addToKB] + `}`
			req, _ := http.NewRequest("POST", e.srv.URL+"/api/escalations/"+id+"/answer", strings.NewReader(body))
			tok, _ := authn.Issue(secret, authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, time.Hour)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var m map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&m)
			if resp.StatusCode != 200 || m["status"] != "answered" || m["added_to_kb"] != c.wantAdded {
				t.Fatalf("%d %v", resp.StatusCode, m)
			}
			if len(e.faq.calls) != c.wantCalls {
				t.Fatalf("faq calls: %d", len(e.faq.calls))
			}
			if c.wantCalls == 1 {
				call := e.faq.calls[0]
				if call["token"] != tok || call["key"] != id || !strings.Contains(call["body"], "Ответ наставника") ||
					strings.Contains(call["title"], "\n") || len([]rune(call["title"])) > 80 || !strings.HasSuffix(call["title"], "…") {
					t.Fatalf("faq call: %v", call)
				}
			}
			if n := e.count(`SELECT count(*) FROM core.escalations WHERE id=$1 AND added_to_kb=$2`, id, c.wantAdded); n != 1 {
				t.Fatalf("added_to_kb column")
			}
			if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='escalation.answered' AND (props->>'added_to_kb')::bool=$1`, c.wantAdded); n != 1 {
				t.Fatalf("event")
			}
		})
	}
}

func TestAnswer_NoKBWriter(t *testing.T) {
	pool := pgtest.New(t)
	h := escalations.New(pool, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	defer srv.Close()
	var id string
	_ = pool.QueryRow(context.Background(), `INSERT INTO core.escalations (from_user_id,to_user_id,to_role,text) VALUES ('intern.anna','mentor.oleg','mentor','q') RETURNING id::text`).Scan(&id)
	tok, _ := authn.Issue(secret, authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, time.Hour)
	req, _ := http.NewRequest("POST", srv.URL+"/api/escalations/"+id+"/answer", strings.NewReader(`{"text":"a","add_to_kb":true}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
}

func TestHandlers_NoClaims(t *testing.T) {
	mux := http.NewServeMux()
	escalations.New(nil, nil, nil, nil, nil).Register(mux)
	for _, r := range []*http.Request{
		httptest.NewRequest("POST", "/api/escalations", strings.NewReader("{}")),
		httptest.NewRequest("GET", "/api/escalations", nil),
		httptest.NewRequest("POST", "/api/escalations/x/answer", strings.NewReader("{}")),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != 401 {
			t.Errorf("%s %s: %d", r.Method, r.URL.Path, rec.Code)
		}
	}
}

// Checkpoint 11.11: the FAQ that goes to the knowledge base is general — no names and no dates of this intern.
func TestAnswer_Checkpoint_FAQIsGeneral_R10(t *testing.T) {
	e := setup(t, true)
	esc := e.send(anna, "mentor", "Олег, привет! Можно ли Анне Котовой взять отгул 13 ноября перед релизом?")
	body := `{"text":"Анна, да, только предупреди Марию Лебедеву за два дня, в пятницу согласует.","add_to_kb":true}`
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/escalations/"+esc["id"].(string)+"/answer", strings.NewReader(body))
	tok, _ := authn.Issue(secret, authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, time.Hour)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(e.faq.calls) != 1 {
		t.Fatalf("faq calls: %d", len(e.faq.calls))
	}
	call := e.faq.calls[0]
	for _, bad := range []string{"Анн", "Котов", "Олег", "Мари", "Лебедев", "13 ноября", "пятниц", "привет"} {
		if strings.Contains(call["title"]+call["body"], bad) {
			t.Fatalf("FAQ keeps %q: %v", bad, call)
		}
	}
	if !strings.Contains(call["body"], "предупреди за два дня") || !strings.HasPrefix(call["title"], "Можно ли взять отгул в нужный день") {
		t.Fatalf("FAQ text: %v", call)
	}
}
