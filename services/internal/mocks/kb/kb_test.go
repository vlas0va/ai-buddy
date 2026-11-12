package kb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

var (
	jwtSecret  = []byte("test-jwt-secret")
	confSecret = []byte("test-confirm-secret")
)

const (
	key1 = "11111111-1111-4111-8111-111111111111"
	key2 = "22222222-2222-4222-8222-222222222222"
)

// fixtureDir holds the synthetic documents the search tests rely on; data/kb (owned by A) is checked separately.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata/kb")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func realKBDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../../../data/kb")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newServer(t *testing.T, pool *pgxpool.Pool) *mcpx.Server {
	t.Helper()
	srv, err := mcpx.NewServer("kb", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return srv
}

// loaded returns a server over a fresh database with the test fixtures loaded.
func loaded(t *testing.T) (*mcpx.Server, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.New(t)
	if _, err := LoadDir(context.Background(), pool, fixtureDir(t)); err != nil {
		t.Fatal(err)
	}
	return newServer(t, pool), pool
}

func who(sub, role, team string) mcpx.Caller {
	return mcpx.Caller{Claims: authn.Claims{Sub: sub, Role: role, TeamID: team}}
}

var (
	anna  = who("intern.anna", "intern", "delivery")
	ivan  = who("intern.ivan", "intern", "search")
	oleg  = who("mentor.oleg", "mentor", "delivery")
	elena = who("hrbp.elena", "hrbp", "people")
)

type found struct {
	DocID, Title, Snippet, SourceType, URL string
	Score                                  float64
}

func doSearch(t *testing.T, srv *mcpx.Server, c mcpx.Caller, args map[string]any) []found {
	t.Helper()
	raw, aerr := srv.Invoke(context.Background(), c, "search", args)
	if aerr != nil {
		t.Fatalf("search %v: %v", args, aerr)
	}
	var out struct {
		Items []struct {
			DocID      string  `json:"doc_id"`
			Title      string  `json:"title"`
			Snippet    string  `json:"snippet"`
			SourceType string  `json:"source_type"`
			URL        string  `json:"url"`
			Score      float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	res := []found{}
	for _, i := range out.Items {
		res = append(res, found{i.DocID, i.Title, i.Snippet, i.SourceType, i.URL, i.Score})
	}
	return res
}

func ids(hs []found) []string {
	out := []string{}
	for _, h := range hs {
		out = append(out, h.DocID)
	}
	return out
}

func has(hs []found, id string) bool {
	for _, h := range hs {
		if h.DocID == id {
			return true
		}
	}
	return false
}

func TestSearch_S2_ACL_R71(t *testing.T) {
	srv, _ := loaded(t)
	q := map[string]any{"query": "канал", "k": 10}
	if got := doSearch(t, srv, anna, q); !has(got, "team-channels-delivery") {
		t.Fatalf("delivery intern must see team channels: %v", ids(got))
	}
	for _, q := range []string{"канал", "отгул", "доступ", "наставник", "персональные данные"} {
		got := doSearch(t, srv, ivan, map[string]any{"query": q, "k": 10})
		for _, h := range got {
			if strings.HasSuffix(h.DocID, "-delivery") {
				t.Fatalf("search intern saw delivery doc %s for %q", h.DocID, q)
			}
		}
	}
	// general docs (teams *) are visible to the other team
	if got := doSearch(t, srv, ivan, map[string]any{"query": "доступы Confluence Jira"}); !has(got, "access-guide") {
		t.Fatalf("general doc must be visible: %v", ids(got))
	}
	// role ACL: hrbp is not in any doc's roles
	if got := doSearch(t, srv, elena, map[string]any{"query": "доступы Confluence Jira"}); len(got) != 0 {
		t.Fatalf("hrbp must see nothing: %v", ids(got))
	}
}

func TestSearch_S2_PrivateNotes_R71(t *testing.T) {
	srv, pool := loaded(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO kb.documents (doc_id, title, body, acl_roles, acl_teams, kind, owner_id)
		VALUES ('note-anna-1', 'Заметка про ревью', 'Спросить Олега про зеркальные гномики', ARRAY['intern'], ARRAY['delivery'], 'note', 'intern.anna')`); err != nil {
		t.Fatal(err)
	}
	if got := doSearch(t, srv, anna, map[string]any{"query": "гномики"}); !has(got, "note-anna-1") {
		t.Fatalf("owner must see her note: %v", ids(got))
	}
	other := who("intern.zed", "intern", "delivery")
	if got := doSearch(t, srv, other, map[string]any{"query": "гномики"}); len(got) != 0 {
		t.Fatalf("foreign note leaked: %v", ids(got))
	}
}

func TestSearch_S2_RussianMorphologyAndShape_R69(t *testing.T) {
	srv, _ := loaded(t)
	got := doSearch(t, srv, anna, map[string]any{"query": "отгулы"})
	if !has(got, "absence-policy") || !has(got, "absence-policy-delivery") {
		t.Fatalf("«отгулы» must find «отгул»: %v", ids(got))
	}
	for _, h := range got {
		if h.SourceType != "test" || h.Title == "" || h.Snippet == "" {
			t.Fatalf("bad found %+v", h)
		}
		if utf8.RuneCountInString(h.Snippet) > 300 {
			t.Fatalf("snippet too long: %d", utf8.RuneCountInString(h.Snippet))
		}
		if strings.ContainsAny(h.Snippet, "<>") {
			t.Fatalf("snippet should be plain text: %q", h.Snippet)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Fatalf("not ranked desc: %v", got)
		}
	}
	if got[0].URL != "" {
		t.Fatalf("url must be empty when unknown, got %q", got[0].URL)
	}
}

func TestSearch_S2_KAndValidation_R79(t *testing.T) {
	srv, _ := loaded(t)
	if got := doSearch(t, srv, anna, map[string]any{"query": "тестовая инструкция прототипа", "k": 2}); len(got) != 2 {
		t.Fatalf("k=2 gave %d", len(got))
	}
	if got := doSearch(t, srv, anna, map[string]any{"query": "тестовые документы"}); len(got) > 5 {
		t.Fatalf("default k is 5, got %d", len(got))
	}
	for _, args := range []map[string]any{{"query": "а"}, {"query": "отгул", "k": 11}, {"query": "отгул", "k": 0}, {}} {
		if _, aerr := srv.Invoke(context.Background(), anna, "search", args); aerr == nil || aerr.Code != apierr.CodeValidation {
			t.Fatalf("args %v: %v", args, aerr)
		}
	}
	// a query of only stop words matches nothing but is not an error
	if got := doSearch(t, srv, anna, map[string]any{"query": "и в на"}); len(got) != 0 {
		t.Fatalf("got %v", ids(got))
	}
}

func TestSearch_URLAndLongSnippet(t *testing.T) {
	srv, pool := loaded(t)
	long := strings.Repeat("слово ", 400) + "зебра " + strings.Repeat("слово ", 400)
	if _, err := pool.Exec(context.Background(), `INSERT INTO kb.documents (doc_id, title, body, acl_roles, acl_teams, url)
		VALUES ('long-doc', 'Длинный', $1, ARRAY['intern'], ARRAY['*'], 'https://portal.example.test/doc')`, long); err != nil {
		t.Fatal(err)
	}
	got := doSearch(t, srv, anna, map[string]any{"query": "зебра"})
	if len(got) != 1 || got[0].URL != "https://portal.example.test/doc" {
		t.Fatalf("got %+v", got)
	}
	if n := utf8.RuneCountInString(truncateRunes(strings.Repeat("я", 500), 300)); n != 300 {
		t.Fatalf("truncate gave %d", n)
	}
}

func addArgs(t *testing.T, c mcpx.Caller, key, title, body string) map[string]any {
	t.Helper()
	a, err := mcpx.WithConfirmation(confSecret, c.Sub, key, map[string]any{"title": title, "body": body}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func addFAQResult(t *testing.T, srv *mcpx.Server, c mcpx.Caller, args map[string]any) (map[string]any, *apierr.Error) {
	t.Helper()
	raw, aerr := srv.Invoke(context.Background(), c, "add_faq", args)
	if aerr != nil {
		return nil, aerr
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestAddFAQ_S2_Rights_R71(t *testing.T) {
	srv, _ := loaded(t)
	// only mentors; the role check runs before the handler
	for _, c := range []mcpx.Caller{anna, who("lead.maria", "lead", "delivery"), elena} {
		if _, aerr := addFAQResult(t, srv, c, addArgs(t, c, key1, "Как оформить пропуск", "Через портал.")); aerr == nil || aerr.Code != apierr.CodeForbidden {
			t.Fatalf("%s: got %v", c.Role, aerr)
		}
	}
	// no / foreign confirmation token
	if _, aerr := addFAQResult(t, srv, oleg, map[string]any{"title": "Заголовок", "body": "Текст ответа", "idempotency_key": key1, "confirmation_token": "x.y"}); aerr == nil || aerr.Code != apierr.CodeInvalidConfirmation {
		t.Fatalf("got %v", aerr)
	}
	foreign := addArgs(t, who("mentor.sergey", "mentor", "search"), key1, "Заголовок", "Текст ответа")
	if _, aerr := addFAQResult(t, srv, oleg, foreign); aerr == nil || aerr.Code != apierr.CodeInvalidConfirmation {
		t.Fatalf("foreign token: %v", aerr)
	}
}

func TestAddFAQ_S2_TeamScopeAndSearch_R71(t *testing.T) {
	srv, pool := loaded(t)
	out, aerr := addFAQResult(t, srv, oleg, addArgs(t, oleg, key1, "Пропуск в офис", "Заказать пропуск нужно через портал за день."))
	if aerr != nil {
		t.Fatal(aerr)
	}
	id := out["doc_id"].(string)
	if !strings.HasPrefix(id, "faq-delivery-") || len(id) != len("faq-delivery-")+8 || out["source_type"] != "test" || out["replayed"] != false {
		t.Fatalf("got %v", out)
	}
	var kind, createdBy string
	var roles, teams []string
	if err := pool.QueryRow(context.Background(), `SELECT kind, created_by, acl_roles, acl_teams FROM kb.documents WHERE doc_id=$1`, id).Scan(&kind, &createdBy, &roles, &teams); err != nil {
		t.Fatal(err)
	}
	if kind != "faq" || createdBy != "mentor.oleg" || strings.Join(roles, ",") != "intern,mentor,lead" || strings.Join(teams, ",") != "delivery" {
		t.Fatalf("stored %s %s %v %v", kind, createdBy, roles, teams)
	}
	if got := doSearch(t, srv, anna, map[string]any{"query": "пропуск"}); !has(got, id) {
		t.Fatalf("delivery intern must find the faq: %v", ids(got))
	}
	if got := doSearch(t, srv, ivan, map[string]any{"query": "пропуск"}); has(got, id) {
		t.Fatal("search team must not see delivery faq")
	}
}

func TestAddFAQ_S2_Idempotent_R79(t *testing.T) {
	srv, pool := loaded(t)
	args := addArgs(t, oleg, key1, "Пропуск в офис", "Заказать пропуск нужно через портал.")
	first, aerr := addFAQResult(t, srv, oleg, args)
	if aerr != nil {
		t.Fatal(aerr)
	}
	second, aerr := addFAQResult(t, srv, oleg, args)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if second["doc_id"] != first["doc_id"] || second["replayed"] != true || second["source_type"] != "test" {
		t.Fatalf("first %v second %v", first, second)
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM kb.documents WHERE kind='faq'`).Scan(&n)
	if n != 1 {
		t.Fatalf("want one faq, got %d", n)
	}
	// same key, different arguments -> conflict
	if _, aerr := addFAQResult(t, srv, oleg, addArgs(t, oleg, key1, "Другой заголовок", "Другой текст")); aerr == nil || aerr.Code != apierr.CodeConflict {
		t.Fatalf("got %v", aerr)
	}
	// same key and arguments from another mentor -> conflict, no leak
	other := who("mentor.sergey", "mentor", "search")
	if _, aerr := addFAQResult(t, srv, other, addArgs(t, other, key1, "Пропуск в офис", "Заказать пропуск нужно через портал.")); aerr == nil || aerr.Code != apierr.CodeConflict {
		t.Fatalf("got %v", aerr)
	}
	// a new key creates a new document
	if o, aerr := addFAQResult(t, srv, oleg, addArgs(t, oleg, key2, "Пропуск в офис", "Заказать пропуск нужно через портал.")); aerr != nil || o["doc_id"] == first["doc_id"] {
		t.Fatalf("got %v %v", o, aerr)
	}
}

func TestKB_DBFailure(t *testing.T) {
	pool := pgtest.New(t)
	srv := newServer(t, pool)
	pool.Close()
	if _, aerr := srv.Invoke(context.Background(), anna, "search", map[string]any{"query": "отгул"}); aerr == nil || aerr.Code != apierr.CodeUnavailable {
		t.Fatalf("got %v", aerr)
	}
	if _, aerr := srv.Invoke(context.Background(), oleg, "add_faq", addArgs(t, oleg, key1, "Заголовок", "Текст")); aerr == nil || aerr.Code != apierr.CodeUnavailable {
		t.Fatalf("got %v", aerr)
	}
}

func TestRegister_Errors(t *testing.T) {
	if err := Register(nil, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTP_S2_RoundTrip_R79(t *testing.T) {
	srv, _ := loaded(t)
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux, srv); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()
	cl := &mcpx.Client{BaseURL: ts.URL + "/mcp/"}
	tok, _ := authn.Issue(jwtSecret, authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, time.Hour)
	raw, aerr, err := cl.Call(context.Background(), tok, "kb", "search", map[string]any{"query": "отгулы"})
	if err != nil || aerr != nil {
		t.Fatalf("%v %v", err, aerr)
	}
	if !strings.Contains(string(raw), "absence-policy") {
		t.Fatalf("got %s", raw)
	}
	_, aerr, err = cl.Call(context.Background(), tok, "kb", "add_faq", addArgs(t, anna, key1, "Заголовок", "Текст ответа"))
	if err != nil || aerr == nil || aerr.Code != apierr.CodeForbidden {
		t.Fatalf("intern add_faq: %v %v", aerr, err)
	}
}

// ---- loader ----

const goodDoc = `---
doc_id: doc-one
title: Первый документ
source_type: test
acl: {roles: [intern, mentor], teams: [delivery]}
checklist_item_id: CL-04
url: https://portal.example.test/one
updated: 2026-10-01
---
Тестовый текст про самокаты и велосипеды.
`

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadDir_R69_Valid(t *testing.T) {
	pool := pgtest.New(t)
	dir := writeFiles(t, map[string]string{
		"one.md":    goodDoc,
		"two.md":    strings.NewReplacer("doc-one", "doc-two", "updated: 2026-10-01\n", "").Replace(goodDoc),
		"notes.txt": "не документ",
		"bad.yaml":  "x: [",
	})
	if err := os.Mkdir(filepath.Join(dir, "sub.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := LoadDir(context.Background(), pool, dir)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var title, item, url, kind string
	var updated time.Time
	if err := pool.QueryRow(context.Background(), `SELECT title, checklist_item_id, url, kind, updated FROM kb.documents WHERE doc_id='doc-one'`).Scan(&title, &item, &url, &kind, &updated); err != nil {
		t.Fatal(err)
	}
	if title != "Первый документ" || item != "CL-04" || url != "https://portal.example.test/one" || kind != "doc" || updated.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("stored %q %q %q %q %v", title, item, url, kind, updated)
	}
	// reload with changed text is an upsert
	if err := os.WriteFile(filepath.Join(dir, "one.md"), []byte(strings.Replace(goodDoc, "Первый документ", "Новое название", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err = LoadDir(context.Background(), pool, dir); err != nil || n != 2 {
		t.Fatalf("reload n=%d err=%v", n, err)
	}
	var count int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM kb.documents WHERE doc_id='doc-one' AND title='Новое название'`).Scan(&count)
	if count != 1 {
		t.Fatal("upsert did not update")
	}
}

func TestLoadDir_R69_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name, content, wantErr string
	}{
		{"bad-source.md", strings.Replace(goodDoc, "source_type: test", "source_type: official", 1), "source_type"},
		{"no-source.md", strings.Replace(goodDoc, "source_type: test\n", "", 1), "source_type"},
		{"no-id.md", strings.Replace(goodDoc, "doc_id: doc-one\n", "", 1), "doc_id"},
		{"no-title.md", strings.Replace(goodDoc, "title: Первый документ\n", "", 1), "title"},
		{"no-acl.md", strings.Replace(goodDoc, "acl: {roles: [intern, mentor], teams: [delivery]}\n", "", 1), "acl.roles, acl.teams"},
		{"empty-body.md", "---\ndoc_id: a\ntitle: t\nsource_type: test\nacl: {roles: [intern], teams: ['*']}\n---\n", "body"},
		{"no-frontmatter.md", "просто текст", "frontmatter"},
		{"unclosed.md", "---\ndoc_id: a\n", "not closed"},
		{"broken-yaml.md", "---\ndoc_id: [\n---\nтекст", "invalid frontmatter"},
		{"bad-role.md", strings.Replace(goodDoc, "[intern, mentor]", "[admin]", 1), "unknown role"},
		{"bad-id.md", strings.Replace(goodDoc, "doc-one", "Doc One", 1), "invalid doc_id"},
		{"bad-item.md", strings.Replace(goodDoc, "CL-04", "X1", 1), "checklist_item_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.New(t)
			dir := writeFiles(t, map[string]string{"ok.md": strings.Replace(goodDoc, "doc-one", "doc-ok", 1), tc.name: tc.content})
			n, err := LoadDir(context.Background(), pool, dir)
			if err == nil || n != 0 {
				t.Fatalf("n=%d err=%v", n, err)
			}
			if !strings.Contains(err.Error(), tc.name) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error must name the file and the problem: %v", err)
			}
			var count int
			_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM kb.documents WHERE doc_id='doc-ok'`).Scan(&count)
			if count != 0 {
				t.Fatal("a failed load must not write anything")
			}
		})
	}
}

func TestLoadDir_R69_EdgeCases(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.New(t)
	if n, err := LoadDir(ctx, pool, filepath.Join(t.TempDir(), "missing")); n != 0 || err != nil {
		t.Fatalf("missing dir: %d %v", n, err)
	}
	if n, err := LoadDir(ctx, pool, t.TempDir()); n != 0 || err != nil {
		t.Fatalf("empty dir: %d %v", n, err)
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	if _, err := LoadDir(ctx, pool, f); err == nil {
		t.Fatal("a file instead of a dir must fail")
	}
	dup := writeFiles(t, map[string]string{"a.md": goodDoc, "b.md": goodDoc})
	if _, err := LoadDir(ctx, pool, dup); err == nil || !strings.Contains(err.Error(), "b.md") {
		t.Fatalf("duplicate doc_id: %v", err)
	}
	crlf := writeFiles(t, map[string]string{"c.md": "\xef\xbb\xbf" + strings.ReplaceAll(goodDoc, "\n", "\r\n")})
	if n, err := LoadDir(ctx, pool, crlf); n != 1 || err != nil {
		t.Fatalf("CRLF/BOM: %d %v", n, err)
	}
	// a faq record must not be overwritten by a file with the same doc_id
	if _, err := pool.Exec(ctx, `INSERT INTO kb.documents (doc_id, title, body, acl_roles, acl_teams, kind) VALUES ('faq-x', 't', 'b', ARRAY['intern'], ARRAY['*'], 'faq')`); err != nil {
		t.Fatal(err)
	}
	clash := writeFiles(t, map[string]string{"x.md": strings.Replace(goodDoc, "doc-one", "faq-x", 1)})
	if _, err := LoadDir(ctx, pool, clash); err == nil || !strings.Contains(err.Error(), "x.md") {
		t.Fatalf("clash: %v", err)
	}
	pool.Close()
	if _, err := LoadDir(ctx, pool, writeFiles(t, map[string]string{"a.md": goodDoc})); err == nil {
		t.Fatal("closed pool must fail")
	}
}

// TestLoadDir_R69_ExtraFields loads frontmatter written like A's data/kb: flow-style acl, checklist_item_id: null,
// fields the loader does not know (checklist, tags, origin) and an `updated` that is not a date. README.md is skipped.
func TestLoadDir_R69_ExtraFields(t *testing.T) {
	pool := pgtest.New(t)
	dir := writeFiles(t, map[string]string{
		"README.md": "# Синтетическая база знаний\n\nОписание формата, без frontmatter.\n",
		"common.md": `---
doc_id: common-doc
title: "Общий документ"
source_type: test
acl: {roles: [intern, mentor, lead, hrbp, alumni], teams: ["*"]}
checklist_item_id: null
checklist: []
tags: [отгул, absence]
origin: escalation
updated: 2026-11-01
---
# Общий документ

Тестовая инструкция прототипа.
`,
		"team.md": `---
doc_id: team-doc
title: Командный документ
source_type: test
acl:
  roles: [intern, mentor, lead]
  teams: [delivery]
checklist_item_id: CL-07
checklist: [CL-07, CL-08]
updated: на прошлой неделе
---
Текст.
`,
	})
	n, err := LoadDir(context.Background(), pool, dir)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var item *string
	var updated time.Time
	if err := pool.QueryRow(context.Background(), `SELECT checklist_item_id, updated FROM kb.documents WHERE doc_id='common-doc'`).Scan(&item, &updated); err != nil {
		t.Fatal(err)
	}
	if item != nil || updated.Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("common-doc: item=%v updated=%v", item, updated)
	}
	var roles, teams []string
	if err := pool.QueryRow(context.Background(), `SELECT checklist_item_id, acl_roles, acl_teams FROM kb.documents WHERE doc_id='team-doc'`).Scan(&item, &roles, &teams); err != nil {
		t.Fatal(err)
	}
	if item == nil || *item != "CL-07" || len(roles) != 3 || len(teams) != 1 || teams[0] != "delivery" {
		t.Fatalf("team-doc: item=%v roles=%v teams=%v", item, roles, teams)
	}
}

// TestLoadDir_R69_RealDataDir loads the knowledge base A ships in data/kb exactly as mcp-mocks does at startup:
// every document must be valid, marked source_type test, and the document of absence.get_policy must exist.
func TestLoadDir_R69_RealDataDir(t *testing.T) {
	pool := pgtest.New(t)
	n, err := LoadDir(context.Background(), pool, realKBDir(t))
	if err != nil {
		t.Fatalf("data/kb must load without errors: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(realKBDir(t), "*.md"))
	docs := 0
	for _, f := range files {
		if !strings.EqualFold(filepath.Base(f), "README.md") {
			docs++
		}
	}
	if n != docs || n == 0 {
		t.Fatalf("loaded %d of %d documents", n, docs)
	}
	var nonTest int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM kb.documents WHERE source_type <> 'test'`).Scan(&nonTest); err != nil || nonTest != 0 {
		t.Fatalf("non-test documents: %d %v", nonTest, err)
	}
	var title string
	if err := pool.QueryRow(context.Background(), `SELECT title FROM kb.documents WHERE doc_id='absence-policy'`).Scan(&title); err != nil {
		t.Fatalf("absence-policy (used by absence.get_policy) must be in data/kb: %v", err)
	}
	// loading twice is idempotent
	if n2, err := LoadDir(context.Background(), pool, realKBDir(t)); err != nil || n2 != n {
		t.Fatalf("reload: %d %v", n2, err)
	}
}

func TestGet_S2_FAQOpensWithACL_R71(t *testing.T) {
	srv, _ := loaded(t)
	out, aerr := addFAQResult(t, srv, oleg, addArgs(t, oleg, key1, "Пропуск в офис", "Заказать пропуск нужно через портал за день."))
	if aerr != nil {
		t.Fatal(aerr)
	}
	id := out["doc_id"].(string)
	raw, aerr := srv.Invoke(context.Background(), anna, "get", map[string]any{"doc_id": id})
	if aerr != nil {
		t.Fatal(aerr)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d["kind"] != "faq" || d["source_type"] != "test" || !strings.Contains(d["body"].(string), "через портал") {
		t.Fatalf("got %v", d)
	}
	if _, aerr := srv.Invoke(context.Background(), ivan, "get", map[string]any{"doc_id": id}); aerr == nil || aerr.Code != apierr.CodeNotFound {
		t.Fatalf("another team must not open the faq: %v", aerr)
	}
	if _, aerr := srv.Invoke(context.Background(), anna, "get", map[string]any{"doc_id": "nope"}); aerr == nil || aerr.Code != apierr.CodeNotFound {
		t.Fatalf("missing: %v", aerr)
	}
}

func TestAddFAQ_ScopeAllIsReadByEveryTeam_Feedback1211(t *testing.T) {
	srv, _ := loaded(t)
	args, err := mcpx.WithConfirmation(confSecret, oleg.Sub, key1,
		map[string]any{"title": "Можно ли приходить в офис с собакой", "body": "Нет, только собаки-поводыри.", "scope": "all"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	out, aerr := addFAQResult(t, srv, oleg, args)
	if aerr != nil {
		t.Fatal(aerr)
	}
	id := out["doc_id"].(string)
	if _, aerr := srv.Invoke(context.Background(), ivan, "get", map[string]any{"doc_id": id}); aerr != nil {
		t.Fatalf("another team must read a general answer: %v", aerr)
	}
	if got := doSearch(t, srv, ivan, map[string]any{"query": "собака офис"}); !has(got, id) {
		t.Fatalf("search team must find it: %v", ids(got))
	}
}
