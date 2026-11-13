// Пакет mcpclient — типизированный адаптер ядра поверх mcpx.Client для мок-серверов MCP (contracts/mcp-tools.md).
//
// Ядро само вызывает MCP только там, где подтверждением служит нажатие кнопки: кнопки решения руководителя
// (approvals.list_pending, approvals.decide) и ответ наставника, публикуемый в базу знаний (kb.add_faq). Подтверждённые
// записи из чата (absence.create_request, access.request_access) выполняет агент в ходе подтверждения
// (contracts/agent-stream.md); типы их результатов лежат здесь, потому что ядро их декодирует и сохраняет. Каждый вызов
// несёт собственный JWT пользователя, поэтому MCP-серверы берут личность из него (R71). Для инструментов записи клиент
// подписывает токен подтверждения на проверенного субъекта вызывающего, ключ идемпотентности и ровно те аргументы,
// которые отправляет (mcpx.WithConfirmation).
//
// Ошибки: ошибки инструментов возвращаются как *apierr.Error с кодом MCP (forbidden, not_found, validation_error,
// conflict, invalid_confirmation, unavailable); сбои транспорта и протокола оборачивают *apierr.Error
// (upstream_unavailable, unauthorized, internal). apierr.WriteJSON переводит и те и другие в коды openapi.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
)

// DefaultBaseURL — MCP_BASE_URL, если он не задан.
const DefaultBaseURL = "http://mcp-mocks:8100/mcp"

// DefaultConfirmationTTL — время жизни токена подтверждения, выпущенного клиентом (openapi: exp 5 мин).
const DefaultConfirmationTTL = 5 * time.Minute

// Имена инструментов в форме "<server>.<tool>", как в черновиках агента.
const (
	ToolCreateAbsence  = "absence.create_request"
	ToolRequestAccess  = "access.request_access"
	ToolListApprovals  = "approvals.list_pending"
	ToolDecideApproval = "approvals.decide"
	ToolAddFAQ         = "kb.add_faq"
	ToolCreateTrip     = "trips.create_request"
	ToolSignConsent    = "consents.sign"
	ToolReschedule     = "absence.reschedule_request"
	ToolCancelAbsence  = "absence.cancel_request"
)

// Config настраивает Client.
type Config struct {
	BaseURL            string        // MCP_BASE_URL; пусто — DefaultBaseURL
	JWTSecret          []byte        // JWT_SECRET: субъект подтверждения берётся из проверенного токена пользователя
	ConfirmationSecret []byte        // CONFIRMATION_HMAC_SECRET
	HTTPClient         *http.Client  // nil означает http.DefaultTransport без таймаута клиента (используйте дедлайны ctx)
	ConfirmationTTL    time.Duration // <= 0 — DefaultConfirmationTTL
}

// Client вызывает инструменты MCP от имени пользователей. Безопасен для конкурентного использования.
type Client struct {
	mcp      *mcpx.Client
	verifier *authn.Verifier
	secret   []byte
	ttl      time.Duration
}

// New возвращает Client; оба секрета обязательны.
func New(cfg Config) (*Client, error) {
	if len(cfg.JWTSecret) == 0 || len(cfg.ConfirmationSecret) == 0 {
		return nil, errors.New("mcpclient: JWT and confirmation secrets are required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	ttl := cfg.ConfirmationTTL
	if ttl <= 0 {
		ttl = DefaultConfirmationTTL
	}
	return &Client{
		mcp:      &mcpx.Client{BaseURL: base, HTTPClient: cfg.HTTPClient},
		verifier: authn.NewVerifier(cfg.JWTSecret),
		secret:   cfg.ConfirmationSecret,
		ttl:      ttl,
	}, nil
}

// SplitTool делит "<server>.<tool>" на части.
func SplitTool(tool string) (server, name string, ok bool) {
	server, name, ok = strings.Cut(tool, ".")
	if !ok || server == "" || name == "" || strings.ContainsAny(name, "./ ") || strings.ContainsAny(server, "/ ") {
		return "", "", false
	}
	return server, name, true
}

// Call вызывает инструмент чтения ("<server>.<tool>") с args (nil — {}) и возвращает его структурированный результат.
func (c *Client) Call(ctx context.Context, userToken, tool string, args any) (json.RawMessage, error) {
	server, name, ok := SplitTool(tool)
	if !ok {
		return nil, fmt.Errorf("mcpclient: invalid tool name %q: %w", tool, apierr.Internal())
	}
	out, toolErr, err := c.mcp.Call(ctx, userToken, server, name, args)
	if err != nil {
		return nil, err
	}
	if toolErr != nil {
		return nil, toolErr
	}
	return out, nil
}

// Execute вызывает инструмент записи ("<server>.<tool>") с args плюс idempotency_key = key и токеном подтверждения,
// подписанным на субъекта userToken. args не должны содержать idempotency_key или confirmation_token (они
// перезаписываются). Повторный вызов с тем же ключом возвращает сохранённый результат (`replayed: true`).
func (c *Client) Execute(ctx context.Context, userToken, tool string, args map[string]any, key string) (json.RawMessage, error) {
	claims, err := c.verifier.Verify(userToken)
	if err != nil {
		return nil, fmt.Errorf("mcpclient: user token: %w: %w", apierr.Unauthorized(), err)
	}
	if args == nil {
		args = map[string]any{}
	}
	signed, err := mcpx.WithConfirmation(c.secret, claims.Sub, key, args, c.ttl)
	if err != nil {
		return nil, fmt.Errorf("mcpclient: sign %s: %w: %w", tool, apierr.Internal(), err)
	}
	return c.Call(ctx, userToken, tool, signed)
}

// AbsenceRequest — результат absence.create_request (contracts/schemas/absence.json).
type AbsenceRequest struct {
	ID                  string `json:"id"`
	Type                string `json:"type"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	Comment             string `json:"comment,omitempty"`
	Status              string `json:"status"`
	ApproverID          string `json:"approver_id"`
	ApproverDisplayName string `json:"approver_display_name,omitempty"`
	CreatedAt           string `json:"created_at"`
	Replayed            bool   `json:"replayed,omitempty"`
}

// TripRequest — результат trips.create_request (contracts/schemas/trips.json).
type TripRequest struct {
	ID                  string `json:"id"`
	City                string `json:"city"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	Purpose             string `json:"purpose"`
	Status              string `json:"status"`
	ApproverID          string `json:"approver_id"`
	ApproverDisplayName string `json:"approver_display_name,omitempty"`
}

// ConsentSignature — результат consents.sign (contracts/schemas/consents.json).
type ConsentSignature struct {
	ID        string `json:"id"`
	ConsentID string `json:"consent_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	SignedAt  string `json:"signed_at"`
}

// AccessRequest — результат access.request_access (contracts/schemas/access.json).
type AccessRequest struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	ResourceName    string  `json:"resource_name,omitempty"`
	Status          string  `json:"status"`
	ApproverID      *string `json:"approver_id,omitempty"`
	ChecklistItemID *string `json:"checklist_item_id,omitempty"`
	Environment     *string `json:"environment,omitempty"` // dev, stage, prod для базы данных (чекпоинт 11.11)
	Level           string  `json:"level,omitempty"`       // read, write, admin
	CreatedAt       string  `json:"created_at"`
	Replayed        bool    `json:"replayed,omitempty"`
}

// Approval — элемент approvals.list_pending и результат approvals.decide (contracts/schemas/approvals.json).
type Approval struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	RefID                string   `json:"ref_id"`
	RequesterID          string   `json:"requester_id"`
	RequesterDisplayName string   `json:"requester_display_name,omitempty"`
	Summary              string   `json:"summary"`
	Status               string   `json:"status"`
	Comment              string   `json:"comment,omitempty"`
	Stage                string   `json:"stage,omitempty"` // отсутствие: lead, затем hr_admin
	CreatedAt            string   `json:"created_at"`
	TeamAbsent           []string `json:"team_absent,omitempty"`
	Replayed             bool     `json:"replayed,omitempty"`
}

// FAQ — результат kb.add_faq.
type FAQ struct {
	DocID      string `json:"doc_id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	Replayed   bool   `json:"replayed,omitempty"`
}

// Decode разбирает результат инструмента в dst; некорректный результат — внутренняя ошибка.
func Decode(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("mcpclient: decode result: %w: %w", apierr.Internal(), err)
	}
	return nil
}

func execTyped[T any](ctx context.Context, c *Client, token, tool string, args map[string]any, key string) (T, error) {
	var out T
	raw, err := c.Execute(ctx, token, tool, args, key)
	if err != nil {
		return out, err
	}
	err = Decode(raw, &out)
	return out, err
}

// ListPendingApprovals вызывает approvals.list_pending с токеном руководителя.
func (c *Client) ListPendingApprovals(ctx context.Context, userToken string) ([]Approval, error) {
	raw, err := c.Call(ctx, userToken, ToolListApprovals, map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []Approval `json:"items"`
	}
	if err := Decode(raw, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []Approval{}
	}
	return out.Items, nil
}

// DecideApproval вызывает approvals.decide с args {approval_id, decision[, comment]} (comment — только если не пустой)
// и idempotency_key = key (заголовок Idempotency-Key запроса).
func (c *Client) DecideApproval(ctx context.Context, userToken, approvalID, decision, comment, key string) (Approval, error) {
	args := map[string]any{"approval_id": approvalID, "decision": decision}
	if comment != "" {
		args["comment"] = comment
	}
	return execTyped[Approval](ctx, c, userToken, ToolDecideApproval, args, key)
}

// AddFAQ вызывает kb.add_faq с токеном наставника; idempotencyKey — id обращения. Реализует escalations.FAQWriter.
func (c *Client) AddFAQ(ctx context.Context, mentorToken, title, body, scope, idempotencyKey string) error {
	args := map[string]any{"title": title, "body": body}
	if scope == "all" {
		args["scope"] = "all"
	}
	_, err := execTyped[FAQ](ctx, c, mentorToken, ToolAddFAQ, args, idempotencyKey)
	return err
}
