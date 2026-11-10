package agenttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/agentclient/agenttest"
	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/canonjson"
	"aibuddy/services/internal/platform/confirm"
)

var (
	jwtSecret  = []byte("test-jwt-secret-0123456789abcdef")
	hmacSecret = []byte("test-hmac-secret-0123456789abcdef")
)

func events(t *testing.T, c *agentclient.Client, tok string, req agentclient.TurnRequest) ([]agentclient.Event, error) {
	t.Helper()
	st, err := c.Turn(context.Background(), tok, req)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	var out []agentclient.Event
	for {
		ev, err := st.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}

func names(evs []agentclient.Event) string {
	var n []string
	for _, e := range evs {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func TestFakeAgent_S3_MessageThenConfirmCallsMCP(t *testing.T) {
	fake := mcptest.New(t, jwtSecret, hmacSecret)
	ag := agenttest.New(t, fake.URL)
	c := agentclient.New(ag.URL, time.Second)
	tok := agenttest.Token(t, jwtSecret, "intern.anna", "intern", "delivery", "2026-10-31")

	evs, err := events(t, c, tok, agentclient.TurnRequest{SessionID: "s", Turn: agentclient.Turn{Kind: "message", Text: "Хочу отгул"}})
	if err != nil || names(evs) != "status,status,token,token,sources,action_proposal,done" {
		t.Fatalf("%s %v", names(evs), err)
	}
	var p struct {
		ProposalID string          `json:"proposal_id"`
		Draft      json.RawMessage `json:"draft"`
	}
	_ = json.Unmarshal(evs[5].Data, &p)
	var d struct {
		Args json.RawMessage `json:"args"`
	}
	_ = json.Unmarshal(p.Draft, &d)
	hash, _ := canonjson.SHA256Hex(d.Args)
	ct, _ := confirm.Issue(hmacSecret, "intern.anna", p.ProposalID, hash, time.Minute)
	evs, err = events(t, c, tok, agentclient.TurnRequest{SessionID: "s",
		Turn: agentclient.Turn{Kind: "confirm", ProposalID: p.ProposalID, Draft: p.Draft, ConfirmationToken: ct}})
	if err != nil || names(evs) != "status,status,tool_result,done" || fake.Calls("absence.create_request") != 1 {
		t.Fatalf("%s %v", names(evs), err)
	}
	if ag.Count("message") != 1 || ag.Count("confirm") != 1 || ag.Last("confirm").Auth != "Bearer "+tok || ag.Last("nope").Auth != "" {
		t.Fatal("request log")
	}

	// A token for other args is rejected by mcpx and surfaces as the tool error.
	bad, _ := confirm.Issue(hmacSecret, "intern.anna", p.ProposalID, strings.Repeat("0", 64), time.Minute)
	evs, _ = events(t, c, tok, agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "confirm", ProposalID: p.ProposalID, Draft: p.Draft, ConfirmationToken: bad}})
	if names(evs) != "error" || !strings.Contains(string(evs[0].Data), string(apierr.CodeInvalidConfirmation)) {
		t.Fatalf("%s %s", names(evs), evs[0].Data)
	}
	evs, _ = events(t, c, tok, agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "confirm", Draft: json.RawMessage(`{"tool":"x"}`)}})
	if names(evs) != "error" {
		t.Fatal(names(evs))
	}
	down := agenttest.New(t, "http://127.0.0.1:1/mcp")
	evs, _ = events(t, agentclient.New(down.URL, time.Second), tok, agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "confirm", ProposalID: p.ProposalID, Draft: p.Draft, ConfirmationToken: ct}})
	if names(evs) != "error" || !strings.Contains(string(evs[0].Data), "service_unavailable") {
		t.Fatal(names(evs))
	}
}

func TestFakeAgent_Scripts(t *testing.T) {
	ag := agenttest.New(t, "http://127.0.0.1:1/mcp")
	c := agentclient.New(ag.URL, time.Second)
	for text, want := range map[string]string{"нужен доступ": "status,token,action_proposal,done", "привет": "status,token,done"} {
		evs, err := events(t, c, "tok", agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "message", Text: text}})
		if err != nil || names(evs) != want {
			t.Fatalf("%s: %s %v", text, names(evs), err)
		}
	}
	ag.OnMessage(func(agenttest.Request) []agenttest.Event {
		return []agenttest.Event{{Name: "token", Data: json.RawMessage(`{"text":"raw"}`)}}
	})
	ag.OnConfirm(func(agenttest.Request) []agenttest.Event { return []agenttest.Event{agenttest.Done()} })
	if evs, _ := events(t, c, "tok", agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "message", Text: "x"}}); string(evs[0].Data) != `{"text":"raw"}` {
		t.Fatal(names(evs))
	}
	if evs, _ := events(t, c, "tok", agentclient.TurnRequest{Turn: agentclient.Turn{Kind: "confirm"}}); names(evs) != "done" {
		t.Fatal(names(evs))
	}
	ag.FailWith(http.StatusForbidden)
	if _, err := events(t, c, "tok", agentclient.TurnRequest{}); !errors.Is(err, agentclient.ErrUnavailable) {
		t.Fatal(err)
	}
	ag.FailWith(0)
	ag.Hang(true)
	c.Timeout = 200 * time.Millisecond
	if evs, err := events(t, c, "tok", agentclient.TurnRequest{}); names(evs) != "status" || !errors.Is(err, agentclient.ErrUnavailable) {
		t.Fatalf("%s %v", names(evs), err)
	}
	for _, r := range []struct{ method, body string }{{http.MethodGet, ""}, {http.MethodPost, "{"}} {
		req, _ := http.NewRequest(r.method, ag.URL+"/v1/turn", strings.NewReader(r.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode < 400 {
			t.Fatalf("%s: %v %v", r.method, resp, err)
		}
		resp.Body.Close()
	}
	if agenttest.NewID() == agenttest.NewID() {
		t.Fatal("ids repeat")
	}
}
