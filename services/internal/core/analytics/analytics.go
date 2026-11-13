// Пакет analytics записывает события продуктовой аналитики (contracts/events.md, R24, R62) и обслуживает POST
// /api/events и POST /api/feedback.
//
// Emitter.Emit пишет core.analytics_events и, если включена Kafka, core.outbox одним запросом, поэтому вызывающий с
// pgx.Tx получает обе строки в своей транзакции (а вызывающий с пулом — обе или ни одной). Publisher выгружает outbox в
// Kafka. Props фильтруются по белому списку для каждого события: текст сообщений и имена в таблицы не попадают (R72).
package analytics

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX — подмножество pgxpool.Pool и pgx.Tx, которое здесь используется.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Топики Kafka (outbox.topic).
const (
	TopicDomain      = "buddy.domain"
	TopicClickstream = "buddy.clickstream"
)

// ErrInvalid возвращает Emit для некорректного имени события.
var ErrInvalid = errors.New("analytics: invalid event")

// Event — одно аналитическое событие. UserID, Role и TeamID могут быть пустыми (системные события, например AI-cron).
type Event struct {
	Name   string
	UserID string
	Role   string
	TeamID string
	Props  map[string]any
	// TS необязателен; нулевое значение означает «сейчас».
	TS time.Time
}

// Emitter пишет события. Nil *Emitter отбрасывает события (удобно в тестах других пакетов).
type Emitter struct {
	kafka bool
	now   func() time.Time
}

// NewEmitter возвращает Emitter; kafkaEnabled добавляет строку outbox на каждое событие (задан KAFKA_BROKERS).
func NewEmitter(kafkaEnabled bool) *Emitter {
	return &Emitter{kafka: kafkaEnabled, now: time.Now}
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

// propKeys — белый список props для каждого имени события (contracts/events.md).
var propKeys = map[string][]string{
	"session.started":       {"period"},
	"chat.message_sent":     {"session_id"},
	"chat.answered":         {"session_id", "message_id", "source_count", "latency_ms", "provider"},
	"chat.refused":          {"reason"},
	"chat.escalated":        {"to_role"},
	"escalation.sent":       {"to_role"},
	"chat.feedback":         {"message_id", "useful"},
	"escalation.answered":   {"added_to_kb"},
	"action.proposed":       {"kind", "proposal_id"},
	"action.created":        {"kind", "action_id", "proposal_id", "duration_ms_since_proposal", "attempt"},
	"action.failed":         {"kind", "code", "retryable"},
	"action.status_changed": {"kind", "action_id", "status"},
	"plan.task_done":        {"checklist_item_id", "period", "on_time"},
	"plan.task_overdue":     {"checklist_item_id", "period"},
	"plan.goals_set":        {"count", "by_role"},
	"notification.created":  {"kind", "checklist_item_id"},
	"flag.changed":          {"key", "enabled", "teams_count"},
	"ui.page_view":          {"page"},
	"ui.next_step_clicked":  {"action"},
	"ui.task_toggle":        {"checklist_item_id", "to_status", "latency_ms"},
	"ui.retry_clicked":      {"code"},
	"ui.source_opened":      {"doc_id"},
}

// unknownKeys — объединение всех разрешённых ключей; события с именем вне контракта сохраняют только их.
var unknownKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, ks := range propKeys {
		for _, k := range ks {
			m[k] = true
		}
	}
	return m
}()

const maxPropString = 100

// SanitizeProps оставляет только разрешённые ключи события со скалярными значениями (короткие строки, bool, числа).
func SanitizeProps(name string, props map[string]any) map[string]any {
	allowed := map[string]bool{}
	if ks, ok := propKeys[name]; ok {
		for _, k := range ks {
			allowed[k] = true
		}
	} else {
		allowed = unknownKeys
	}
	out := map[string]any{}
	for k, v := range props {
		if !allowed[k] {
			continue
		}
		switch x := v.(type) {
		case string:
			if len(x) <= maxPropString {
				out[k] = x
			}
		case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			out[k] = x
		case json.Number:
			out[k] = x
		}
	}
	return out
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand не падает на поддерживаемых платформах
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Emit сохраняет ev через q. При включённой Kafka конверт ещё и ставится в core.outbox тем же запросом.
func (e *Emitter) Emit(ctx context.Context, q DBTX, ev Event) error {
	if e == nil {
		return nil
	}
	if !nameRE.MatchString(ev.Name) {
		return fmt.Errorf("%w: name %q", ErrInvalid, ev.Name)
	}
	ts := ev.TS
	if ts.IsZero() {
		ts = e.now()
	}
	ts = ts.UTC()
	id := newUUID()
	props := SanitizeProps(ev.Name, ev.Props)
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("encode props: %w", err)
	}
	if !e.kafka {
		_, err = q.Exec(ctx, `INSERT INTO core.analytics_events (event_id, ts, name, user_id, role, team_id, props)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, ts, ev.Name, nullable(ev.UserID), nullable(ev.Role), nullable(ev.TeamID), propsJSON)
		if err != nil {
			return fmt.Errorf("insert analytics event: %w", err)
		}
		return nil
	}
	topic := TopicDomain
	if strings.HasPrefix(ev.Name, "ui.") {
		topic = TopicClickstream
	}
	envelope, err := json.Marshal(map[string]any{
		"event_id": id, "name": ev.Name, "ts": ts.Format(time.RFC3339Nano),
		"user_id": nullable(ev.UserID), "role": nullable(ev.Role), "team_id": nullable(ev.TeamID), "props": props,
	})
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	_, err = q.Exec(ctx, `WITH e AS (
			INSERT INTO core.analytics_events (event_id, ts, name, user_id, role, team_id, props)
			VALUES ($1, $2, $3, $4, $5, $6, $7))
		INSERT INTO core.outbox (topic, payload) VALUES ($8, $9)`,
		id, ts, ev.Name, nullable(ev.UserID), nullable(ev.Role), nullable(ev.TeamID), propsJSON, topic, envelope)
	if err != nil {
		return fmt.Errorf("insert analytics event with outbox: %w", err)
	}
	return nil
}
