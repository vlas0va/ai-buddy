// Package mcpclient is core's typed adapter over mcpx.Client for the mock MCP servers (contracts/mcp-tools.md).
//
// Core calls MCP itself only where a button press is the confirmation: the lead's decision buttons
// (approvals.list_pending, approvals.decide) and a mentor's answer published to the KB (kb.add_faq). Confirmed chat
// writes (absence.create_request, access.request_access) are executed by the agent in a confirm turn
// (contracts/agent-stream.md); their result types live here because core decodes and saves them. Every call carries
// the user's own JWT, so the MCP servers derive identity from it (R71). For write tools the client signs a
// confirmation token for the caller's verified subject, the idempotency key and the exact arguments it sends
// (mcpx.WithConfirmation).
//
// Errors: tool errors are returned as *apierr.Error with an MCP code (forbidden, not_found, validation_error,
// conflict, invalid_confirmation, unavailable); transport and protocol failures wrap an *apierr.Error
// (upstream_unavailable, unauthorized, internal). apierr.WriteJSON maps both onto openapi codes.
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

// DefaultBaseURL is MCP_BASE_URL when unset.
const DefaultBaseURL = "http://mcp-mocks:8100/mcp"

// DefaultConfirmationTTL is the lifetime of a confirmation token issued by the client (openapi: exp 5 min).
const DefaultConfirmationTTL = 5 * time.Minute

// Tool names in the "<server>.<tool>" form used by agent drafts.
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

// Config configures a Client.
type Config struct {
	BaseURL            string        // MCP_BASE_URL; empty means DefaultBaseURL
	JWTSecret          []byte        // JWT_SECRET: the subject of a confirmation comes from the verified user token
	ConfirmationSecret []byte        // CONFIRMATION_HMAC_SECRET
	HTTPClient         *http.Client  // nil means http.DefaultTransport without a client timeout (use ctx deadlines)
	ConfirmationTTL    time.Duration // <= 0 means DefaultConfirmationTTL
}

// Client calls MCP tools on behalf of users. Safe for concurrent use.
type Client struct {
	mcp      *mcpx.Client
	verifier *authn.Verifier
	secret   []byte
	ttl      time.Duration
}

// New returns a Client; both secrets are required.
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

// SplitTool splits "<server>.<tool>" into its parts.
func SplitTool(tool string) (server, name string, ok bool) {
	server, name, ok = strings.Cut(tool, ".")
	if !ok || server == "" || name == "" || strings.ContainsAny(name, "./ ") || strings.ContainsAny(server, "/ ") {
		return "", "", false
	}
	return server, name, true
}

// Call invokes a read tool ("<server>.<tool>") with args (nil means {}) and returns its structured result.
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

// Execute invokes a write tool ("<server>.<tool>") with args plus idempotency_key = key and a confirmation token
// signed for the subject of userToken. args must not contain idempotency_key or confirmation_token (they are
// overwritten). A repeated call with the same key returns the stored result (`replayed: true`).
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

// AbsenceRequest is the result of absence.create_request (contracts/schemas/absence.json).
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

// TripRequest is the result of trips.create_request (contracts/schemas/trips.json).
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

// ConsentSignature is the result of consents.sign (contracts/schemas/consents.json).
type ConsentSignature struct {
	ID        string `json:"id"`
	ConsentID string `json:"consent_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	SignedAt  string `json:"signed_at"`
}

// AccessRequest is the result of access.request_access (contracts/schemas/access.json).
type AccessRequest struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	ResourceName    string  `json:"resource_name,omitempty"`
	Status          string  `json:"status"`
	ApproverID      *string `json:"approver_id,omitempty"`
	ChecklistItemID *string `json:"checklist_item_id,omitempty"`
	Environment     *string `json:"environment,omitempty"` // dev, stage, prod for a database (checkpoint 11.11)
	Level           string  `json:"level,omitempty"`       // read, write, admin
	CreatedAt       string  `json:"created_at"`
	Replayed        bool    `json:"replayed,omitempty"`
}

// Approval is an item of approvals.list_pending and the result of approvals.decide (contracts/schemas/approvals.json).
type Approval struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	RefID                string   `json:"ref_id"`
	RequesterID          string   `json:"requester_id"`
	RequesterDisplayName string   `json:"requester_display_name,omitempty"`
	Summary              string   `json:"summary"`
	Status               string   `json:"status"`
	Comment              string   `json:"comment,omitempty"`
	Stage                string   `json:"stage,omitempty"` // absence: lead, then hr_admin
	CreatedAt            string   `json:"created_at"`
	TeamAbsent           []string `json:"team_absent,omitempty"`
	Replayed             bool     `json:"replayed,omitempty"`
}

// FAQ is the result of kb.add_faq.
type FAQ struct {
	DocID      string `json:"doc_id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	Replayed   bool   `json:"replayed,omitempty"`
}

// Decode unmarshals a tool result into dst; a malformed result is an internal error.
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

// ListPendingApprovals calls approvals.list_pending with the lead's token.
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

// DecideApproval calls approvals.decide with args {approval_id, decision[, comment]} (comment only when non-empty)
// and idempotency_key = key (the request's Idempotency-Key header).
func (c *Client) DecideApproval(ctx context.Context, userToken, approvalID, decision, comment, key string) (Approval, error) {
	args := map[string]any{"approval_id": approvalID, "decision": decision}
	if comment != "" {
		args["comment"] = comment
	}
	return execTyped[Approval](ctx, c, userToken, ToolDecideApproval, args, key)
}

// AddFAQ calls kb.add_faq with the mentor's token; idempotencyKey is the escalation id. It implements
// escalations.FAQWriter.
func (c *Client) AddFAQ(ctx context.Context, mentorToken, title, body, scope, idempotencyKey string) error {
	args := map[string]any{"title": title, "body": body}
	if scope == "all" {
		args["scope"] = "all"
	}
	_, err := execTyped[FAQ](ctx, c, mentorToken, ToolAddFAQ, args, idempotencyKey)
	return err
}
