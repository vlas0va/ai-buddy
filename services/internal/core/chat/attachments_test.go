package chat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"aibuddy/services/internal/core/agentclient"
)

func TestChat_AT01_AttachmentsReachAgentAndHistory(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	log := "ERROR connect db password=hunter2\n"
	s := e.stream("/api/chat/sessions/"+sid+"/messages", anna, map[string]any{"text": "", "attachments": []map[string]any{
		{"name": "app.log", "kind": "text", "mime": "text/plain", "size": len(log), "text": log},
		{"name": " screen.png ", "kind": "image", "mime": "image/png", "size": 1200, "text": "must be dropped"},
	}})
	if s.last()["message_id"] == nil {
		t.Fatalf("stream %s", s.names())
	}
	turn := e.agent.Last(agentclient.KindMessage).Turn
	if len(turn.Attachments) != 2 || turn.Attachments[0].Text != log || turn.Attachments[1].Name != "screen.png" ||
		turn.Attachments[1].Text != "" || turn.Text != "" {
		t.Fatalf("turn %+v", turn)
	}
	r := e.do(http.MethodGet, "/api/chat/sessions/"+sid, anna, nil)
	var out struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil || len(out.Messages) < 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	user := out.Messages[0]
	if user.Author != "user" || len(user.Attachments) != 2 || user.Attachments[0].Kind != "text" || strings.Contains(string(r.body), "hunter2") {
		t.Fatalf("history %s", r.body)
	}
	if out.Messages[len(out.Messages)-1].Attachments == nil {
		t.Fatal("assistant attachments must be an empty list")
	}
	if n := e.count(`SELECT count(*) FROM core.chat_messages WHERE attachments::text LIKE '%must be dropped%'`); n != 0 {
		t.Fatal("image text stored")
	}
}

func TestChat_AT02_AttachmentValidation(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	att := func(kv ...any) map[string]any {
		m := map[string]any{"name": "a.txt", "kind": "text", "size": 1, "text": "x"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	for name, atts := range map[string][]map[string]any{
		"too many":  {att(), att(), att(), att()},
		"no name":   {att("name", "  ")},
		"long name": {att("name", strings.Repeat("я", 201))},
		"bad kind":  {att("kind", "video")},
		"negative":  {att("size", -1)},
		"huge":      {att("size", 21<<20)},
		"long text": {att("text", strings.Repeat("a", 33<<10))},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.do(http.MethodPost, "/api/chat/sessions/"+sid+"/messages", anna, map[string]any{"text": "смотри", "attachments": atts})
			if r.status != http.StatusBadRequest || r.code(t) != "validation_error" {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	if len(e.agent.Requests()) != 0 {
		t.Fatal("invalid attachments reached the agent")
	}
	long := strings.Repeat("m", 150)
	s := e.stream("/api/chat/sessions/"+sid+"/messages", anna, map[string]any{"text": "вот документ",
		"attachments": []map[string]any{{"name": "offer.pdf", "kind": "document", "mime": long, "size": 10}}})
	if s.last()["message_id"] == nil {
		t.Fatal("document must be accepted")
	}
	if got := e.agent.Last(agentclient.KindMessage).Turn.Attachments[0].Mime; len(got) != 100 {
		t.Fatalf("mime %d", len(got))
	}
}
