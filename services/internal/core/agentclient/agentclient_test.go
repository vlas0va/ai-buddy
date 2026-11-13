package agentclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/platform/httpx"
)

func TestNew_TimeoutIsLLMPlusMargin(t *testing.T) {
	c := agentclient.New("http://agent:8090/", 30*time.Second)
	if c.Timeout != 40*time.Second || c.BaseURL != "http://agent:8090" {
		t.Fatalf("got %+v", c)
	}
}

func TestTurn_S2_StreamsEventsWithUserToken_R78(t *testing.T) {
	var got agentclient.TurnRequest
	var auth, reqID, accept, service string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/turn" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		auth, reqID, accept = r.Header.Get("Authorization"), r.Header.Get(httpx.HeaderRequestID), r.Header.Get("Accept")
		service = r.Header.Get("X-Service-Token")
		var raw map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_ = json.Unmarshal(body, &raw)
		if raw["history"] == nil || raw["user_context"].(map[string]any)["open_tasks"] == nil {
			http.Error(w, "nil arrays", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\nevent: status\ndata: {\"stage\":\"thinking\"}\n\n")
		fmt.Fprint(w, ": ping\n\n")
		fmt.Fprint(w, "id: 2\nevent: token\ndata: not-json\n\n")
		fmt.Fprint(w, "id: 3\nevent: token\ndata: {\"text\":\"Привет\"}\n\n")
	}))
	defer srv.Close()

	c := agentclient.New(srv.URL, time.Second)
	c.HTTPClient = &http.Client{}
	c.ServiceToken = "svc"
	ctx := httpx.WithRequestID(context.Background(), "req-1")
	st, err := c.Turn(ctx, "user-jwt", agentclient.TurnRequest{
		SessionID:   "s1",
		Turn:        agentclient.Turn{Kind: agentclient.KindMessage, Text: "как дела"},
		UserContext: agentclient.UserContext{UserID: "intern.anna", Role: "intern", TeamID: "delivery", CurrentPeriod: "week_2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var names []string
	for {
		ev, err := st.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, ev.Name+":"+string(ev.Data))
	}
	want := []string{`status:{"stage":"thinking"}`, `token:{"text":"Привет"}`}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("events %v", names)
	}
	if auth != "Bearer user-jwt" || reqID != "req-1" || accept != "text/event-stream" || service != "svc" {
		t.Fatalf("headers %q %q %q", auth, reqID, accept)
	}
	if got.Turn.Text != "как дела" || got.UserContext.CurrentPeriod != "week_2" || got.SessionID != "s1" {
		t.Fatalf("request %+v", got)
	}
	if strings.Contains(string(mustJSON(t, got)), "draft") {
		t.Fatalf("message turn must not carry confirm fields: %s", mustJSON(t, got))
	}
	if err := st.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestTurn_AgentDown_UpstreamUnavailable_R79(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"context_mismatch","message":"Контекст не совпадает","retryable":false}}`)
	}))
	defer mismatch.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: token\ndata: {\"text\":\"a\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer slow.Close()

	for _, tc := range []struct {
		name string
		c    *agentclient.Client
		req  agentclient.TurnRequest
		op   string
	}{
		{"non-200", agentclient.New(bad.URL, 0), agentclient.TurnRequest{}, "status 503"},
		{"json error body", agentclient.New(mismatch.URL, 0), agentclient.TurnRequest{}, "status 403"},
		{"refused", agentclient.New("http://127.0.0.1:1", 0), agentclient.TurnRequest{}, "connect"},
		{"bad url", &agentclient.Client{BaseURL: "http://bad\x7f"}, agentclient.TurnRequest{}, "build request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.c.Turn(context.Background(), "tok", tc.req)
			var ue *agentclient.UnavailableError
			if !errors.Is(err, agentclient.ErrUnavailable) || !errors.As(err, &ue) || ue.Op != tc.op || ue.Error() == "" {
				t.Fatalf("got %v", err)
			}
		})
	}

	t.Run("timeout while streaming", func(t *testing.T) {
		c := agentclient.New(slow.URL, 0)
		c.Timeout = 200 * time.Millisecond
		st, err := c.Turn(context.Background(), "tok", agentclient.TurnRequest{})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if ev, err := st.Next(); err != nil || ev.Name != agentclient.EventToken {
			t.Fatalf("first event: %v %v", ev, err)
		}
		_, err = st.Next()
		if !errors.Is(err, agentclient.ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("agent error body is kept", func(t *testing.T) {
		_, err := agentclient.New(mismatch.URL, 0).Turn(context.Background(), "tok", agentclient.TurnRequest{})
		var ue *agentclient.UnavailableError
		if !errors.As(err, &ue) || ue.Status != http.StatusForbidden || ue.Code != "context_mismatch" || ue.Message == "" || ue.Retryable {
			t.Fatalf("got %+v", ue)
		}
	})

	t.Run("unencodable request", func(t *testing.T) {
		_, err := agentclient.New(slow.URL, 0).Turn(context.Background(), "tok",
			agentclient.TurnRequest{Turn: agentclient.Turn{Kind: agentclient.KindConfirm, Draft: json.RawMessage(`{`)}})
		if err == nil || errors.Is(err, agentclient.ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestUnavailableError_Message(t *testing.T) {
	e := &agentclient.UnavailableError{Op: "status 502"}
	if e.Error() != "agentclient: status 502: agent unavailable" || e.Unwrap() != nil {
		t.Fatal(e.Error())
	}
	if errors.Is(errors.New("x"), agentclient.ErrUnavailable) {
		t.Fatal("unrelated error matched")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTurn_S3_ConfirmTurnShape(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: tool_result\ndata: {\"result\":{\"id\":\"ABS-000101\"}}")
	}))
	defer srv.Close()
	due := "2026-11-16"
	st, err := agentclient.New(srv.URL, time.Second).Turn(context.Background(), "tok", agentclient.TurnRequest{
		SessionID: "s1", MessageID: "m1",
		Turn: agentclient.Turn{Kind: agentclient.KindConfirm, ProposalID: "p1",
			Draft: json.RawMessage(`{"tool":"absence.create_request","args":{}}`), ConfirmationToken: "a.b"},
		UserContext: agentclient.UserContext{UserID: "intern.anna", OpenTasks: []agentclient.OpenTask{{TaskID: "t1", Title: "x", DueAt: &due}}},
		Flags:       map[string]bool{"scenario_s3": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ev, err := st.Next()
	if err != nil || ev.Name != agentclient.EventToolResult {
		t.Fatalf("%v %v", ev, err)
	}
	turn := raw["turn"].(map[string]any)
	if turn["kind"] != "confirm" || turn["proposal_id"] != "p1" || turn["confirmation_token"] != "a.b" || turn["draft"] == nil {
		t.Fatalf("turn %v", turn)
	}
	uc := raw["user_context"].(map[string]any)
	task := uc["open_tasks"].([]any)[0].(map[string]any)
	if raw["message_id"] != "m1" || raw["flags"] == nil || uc["flags"] != nil || task["due_at"] != due || task["human_only"] != false {
		t.Fatalf("request %v", raw)
	}
	if _, ok := task["checklist_item_id"]; ok {
		t.Fatalf("nil checklist_item_id must be omitted: %v", task)
	}
	if _, ok := uc["mentees"]; ok {
		t.Fatalf("nil mentees must be omitted: %v", uc)
	}
}

func TestStream_SizeLimits_R76(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: token\ndata: {\"text\":\"ok\"}\n\n")
		switch r.URL.Query().Get("case") {
		case "event":
			fmt.Fprintf(w, "event: token\ndata: {\"text\":\"%s\"}\n\n", strings.Repeat("я", 2000))
		case "stream":
			for i := 0; i < 200; i++ {
				fmt.Fprint(w, "event: token\ndata: {\"text\":\"0123456789\"}\n\n")
			}
		}
	}))
	defer srv.Close()
	for _, tc := range []struct{ name, query string }{{"one event over the limit", "event"}, {"stream over the limit", "stream"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &agentclient.Client{BaseURL: srv.URL, MaxEventBytes: 1024, MaxStreamBytes: 2048}
			c.HTTPClient = &http.Client{Transport: rewriteQuery{tc.query}}
			st, err := c.Turn(context.Background(), "tok", agentclient.TurnRequest{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if ev, err := st.Next(); err != nil || ev.Name != "token" {
				t.Fatalf("first: %v %v", ev, err)
			}
			for {
				_, err = st.Next()
				if err != nil {
					break
				}
			}
			var ue *agentclient.UnavailableError
			if !errors.Is(err, agentclient.ErrTooLarge) || !errors.As(err, &ue) {
				t.Fatalf("got %v", err)
			}
		})
	}
	st, err := agentclient.New(srv.URL, time.Second).Turn(context.Background(), "tok", agentclient.TurnRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Next(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}

// rewriteQuery добавляет ?case=<q> к каждому запросу.
type rewriteQuery struct{ q string }

func (r rewriteQuery) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.RawQuery = "case=" + r.q
	return http.DefaultTransport.RoundTrip(req)
}
