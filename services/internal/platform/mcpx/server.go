// Package mcpx builds the MCP servers of mcp-mocks from the contracts and calls them from core
// (docs/backend/IMPLEMENTATION.md §4, contracts/mcp-tools.md).
//
// Server side: NewServer loads contracts/schemas/<name>.json, Handle binds a plain Go func to every tool, and
// Handler serves the MCP streamable HTTP transport (stateless, JSON responses) behind JWT verification.
// For every tools/call mcpx enforces, in this order, before the handler runs:
//  1. role allow-list of the tool (`roles`) → forbidden
//  2. write tools: confirmation_token and idempotency_key present and non-empty → else invalid_confirmation
//  3. inputSchema (unknown fields such as employee_id, types, enums, patterns, required; format date,
//     date-time, uuid of top-level arguments) after applying schema defaults → validation_error
//  4. write tools: confirm.Verify(sub = caller, key = idempotency_key, args as received) → invalid_confirmation
//
// Identity reaches handlers through the go-sdk mechanism for bearer auth: Handler verifies the JWT, wraps it in an
// auth.TokenInfo (auth.RequireBearerToken), and the streamable transport copies it into CallToolRequest.Extra.
//
// Handler errors of type *apierr.Error become tool results with isError=true and structuredContent
// {code, message, retryable} (MCP codes, see apierr.ToMCP); other errors and panics are logged and reported as a
// generic, non-retryable error. Successful results must match the tool's outputSchema; they are sent as
// structuredContent plus the same JSON as text content. tools/list shows only the tools the caller's role may use.
package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/confirm"
)

// Caller is the authenticated user of an MCP request: verified JWT claims plus the raw bearer token.
// Identity used by tool handlers comes only from here, never from tool arguments (R71).
type Caller struct {
	authn.Claims
	Token string
}

// HandlerFunc implements a tool. args are the validated tool arguments as JSON (including idempotency_key and
// confirmation_token for write tools; schema defaults applied). The result must marshal to a JSON object that
// matches the tool's outputSchema. Return *apierr.Error for expected failures.
type HandlerFunc func(ctx context.Context, c Caller, args json.RawMessage) (any, error)

// Config holds the secrets and logger of a Server.
type Config struct {
	JWTSecret          []byte       // JWT_SECRET: verifies the caller's bearer token
	ConfirmationSecret []byte       // CONFIRMATION_HMAC_SECRET: verifies write confirmations
	Logger             *slog.Logger // nil means slog.Default()
}

// Server is one mock MCP server (absence, approvals, access, checklist, directory, kb).
type Server struct {
	name     string
	cfg      Config
	log      *slog.Logger
	order    []string // tool names in contract order
	tools    map[string]*tool
	handlers map[string]HandlerFunc
	errs     []error
}

// NewServer returns a server whose tools are defined by contracts/schemas/<name>.json. It fails when the schema
// is missing or invalid. Bind every tool with Handle, then call Handler (or Mount).
func NewServer(name string, cfg Config) (*Server, error) {
	defs, err := LoadTools(name)
	if err != nil {
		return nil, err
	}
	return newServer(name, defs, cfg)
}

func newServer(name string, defs []ToolDef, cfg Config) (*Server, error) {
	if name == "" || strings.ContainsAny(name, "/ ") {
		return nil, fmt.Errorf("mcpx: invalid server name %q", name)
	}
	s := &Server{name: name, cfg: cfg, log: cfg.Logger, tools: map[string]*tool{}, handlers: map[string]HandlerFunc{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	for _, d := range defs {
		t, err := compile(d)
		if err != nil {
			return nil, fmt.Errorf("server %q: %w", name, err)
		}
		if _, dup := s.tools[d.Name]; dup {
			return nil, fmt.Errorf("mcpx: server %q: duplicate tool %q", name, d.Name)
		}
		s.tools[d.Name] = t
		s.order = append(s.order, d.Name)
	}
	return s, nil
}

// Name returns the server name; it is served at /mcp/<name>.
func (s *Server) Name() string { return s.name }

// Tools returns the tool definitions in contract order.
func (s *Server) Tools() []ToolDef {
	out := make([]ToolDef, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.tools[n].def)
	}
	return out
}

// Handle binds h to the tool called name. Unknown names, duplicates and nil handlers are reported by Validate.
func (s *Server) Handle(name string, h HandlerFunc) {
	switch {
	case s.tools[name] == nil:
		s.errs = append(s.errs, fmt.Errorf("mcpx: server %q: tool %q is not in the contract", s.name, name))
	case h == nil:
		s.errs = append(s.errs, fmt.Errorf("mcpx: server %q: nil handler for %q", s.name, name))
	case s.handlers[name] != nil:
		s.errs = append(s.errs, fmt.Errorf("mcpx: server %q: tool %q registered twice", s.name, name))
	default:
		s.handlers[name] = h
	}
}

// Validate reports every registration problem: unknown or duplicate tools, contract tools without a handler,
// missing secrets. Handler calls it, so a misconfigured server fails at startup.
func (s *Server) Validate() error {
	errs := slices.Clone(s.errs)
	for _, n := range s.order {
		if s.handlers[n] == nil {
			errs = append(errs, fmt.Errorf("mcpx: server %q: no handler for contract tool %q", s.name, n))
		}
	}
	if len(s.cfg.JWTSecret) == 0 {
		errs = append(errs, fmt.Errorf("mcpx: server %q: empty JWT secret", s.name))
	}
	if len(s.cfg.ConfirmationSecret) == 0 && slices.ContainsFunc(s.order, func(n string) bool { return s.tools[n].def.Write }) {
		errs = append(errs, fmt.Errorf("mcpx: server %q: empty confirmation secret", s.name))
	}
	return errors.Join(errs...)
}

// Invoke runs tool for c through the same checks as an MCP tools/call (role, schema, confirmation, handler,
// output schema) without HTTP. It returns the structured result or a tool error; mock servers use it in tests.
func (s *Server) Invoke(ctx context.Context, c Caller, name string, args any) (json.RawMessage, *apierr.Error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, apierr.ToMCP(apierr.Validation().WithMessage("Аргументы инструмента не кодируются в JSON"))
	}
	t := s.tools[name]
	if t == nil || s.handlers[name] == nil {
		return nil, apierr.ToMCP(apierr.NotFound().WithMessage(fmt.Sprintf("Инструмент %s.%s не найден", s.name, name)))
	}
	return s.run(ctx, c, t, raw)
}

func (s *Server) run(ctx context.Context, c Caller, t *tool, raw json.RawMessage) (json.RawMessage, *apierr.Error) {
	if !t.allows(c.Role) {
		return nil, apierr.Forbidden()
	}
	if t.def.Write && !hasConfirmationFields(raw) {
		// The rights matrix wants invalid_confirmation (not validation_error) for a write without a token.
		return nil, apierr.InvalidConfirmation()
	}
	args, aerr := s.checkArgs(t, raw)
	if aerr != nil {
		return nil, aerr
	}
	if t.def.Write {
		key, _ := args[confirm.KeyIdempotency].(string)
		token, _ := args[confirm.KeyConfirmation].(string)
		if err := confirm.Verify(s.cfg.ConfirmationSecret, token, c.Sub, key, raw, time.Now()); err != nil {
			s.log.InfoContext(ctx, "mcp confirmation rejected", "server", s.name, "tool", t.def.Name, "sub", c.Sub, "reason", err)
			return nil, apierr.InvalidConfirmation()
		}
	}
	withDefaults, err := json.Marshal(args)
	if err != nil {
		return nil, s.internal(ctx, t, fmt.Errorf("re-encode args: %w", err))
	}
	out, err := s.call(ctx, c, t, withDefaults)
	if err != nil {
		if e, ok := apierr.As(err); ok {
			return nil, apierr.ToMCP(e)
		}
		if ctx.Err() != nil {
			return nil, apierr.ToMCP(apierr.From(ctx.Err()))
		}
		return nil, s.internal(ctx, t, err)
	}
	result, err := s.checkOutput(t, out)
	if err != nil {
		return nil, s.internal(ctx, t, err)
	}
	return result, nil
}

// checkArgs decodes the arguments, applies schema defaults and validates them.
func (s *Server) checkArgs(t *tool, raw json.RawMessage) (map[string]any, *apierr.Error) {
	args := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, apierr.Validation().WithMessage("Аргументы инструмента должны быть JSON-объектом")
		}
	}
	if err := t.input.ApplyDefaults(&args); err != nil {
		return nil, apierr.Validation().WithMessage("Некорректные аргументы инструмента: " + err.Error())
	}
	if err := t.input.Validate(&args); err != nil {
		return nil, apierr.Validation().WithMessage("Некорректные аргументы инструмента: " + err.Error())
	}
	if err := t.checkFormats(args); err != nil {
		return nil, apierr.Validation().WithMessage("Некорректные аргументы инструмента: " + err.Error())
	}
	return args, nil
}

// hasConfirmationFields reports whether raw is an object with non-empty string confirmation_token and
// idempotency_key.
func hasConfirmationFields(raw json.RawMessage) bool {
	var f struct {
		Token *string `json:"confirmation_token"`
		Key   *string `json:"idempotency_key"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return false
	}
	return f.Token != nil && *f.Token != "" && f.Key != nil && *f.Key != ""
}

// call runs the handler, turning a panic into an error.
func (s *Server) call(ctx context.Context, c Caller, t *tool, args json.RawMessage) (out any, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v\n%s", v, debug.Stack())
		}
	}()
	return s.handlers[t.def.Name](ctx, c, args)
}

func (s *Server) checkOutput(t *tool, out any) (json.RawMessage, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("result is not a JSON object: %s", truncate(raw))
	}
	if t.output != nil {
		if err := t.output.Validate(&obj); err != nil {
			return nil, fmt.Errorf("result violates outputSchema: %w", err)
		}
	}
	return raw, nil
}

func (s *Server) internal(ctx context.Context, t *tool, err error) *apierr.Error {
	s.log.ErrorContext(ctx, "mcp tool failed", "server", s.name, "tool", t.def.Name, "err", err)
	return apierr.ToMCP(apierr.Internal())
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// DecodeArgs unmarshals validated tool arguments into dst; a failure is a validation_error.
func DecodeArgs(args json.RawMessage, dst any) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return apierr.Validation().WithMessage("Некорректные аргументы инструмента: " + err.Error())
	}
	return nil
}

// WithConfirmation returns a copy of args with idempotency_key = key and a confirmation_token signed for sub,
// key and the hash of args (core uses it for approvals.decide and kb.add_faq, where the button press is the
// confirmation; tests use it for write tools).
func WithConfirmation(secret []byte, sub, key string, args map[string]any, ttl time.Duration) (map[string]any, error) {
	hash, err := confirm.ArgsHash(args)
	if err != nil {
		return nil, err
	}
	token, err := confirm.Issue(secret, sub, key, hash, ttl)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	out[confirm.KeyIdempotency] = key
	out[confirm.KeyConfirmation] = token
	return out, nil
}
