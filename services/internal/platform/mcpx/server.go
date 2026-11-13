// Пакет mcpx собирает MCP-серверы mcp-mocks из контрактов и вызывает их из ядра (docs/backend/IMPLEMENTATION.md §4,
// contracts/mcp-tools.md).
//
// Серверная сторона: NewServer загружает contracts/schemas/<name>.json, Handle привязывает обычную функцию Go к каждому
// инструменту, а Handler обслуживает streamable HTTP-транспорт MCP (без состояния, JSON-ответы) за проверкой JWT. Для
// каждого tools/call mcpx до запуска обработчика проверяет по порядку:
//  1. белый список ролей инструмента (`roles`) → forbidden
//  2. инструменты записи: confirmation_token и idempotency_key есть и не пустые → иначе invalid_confirmation
//  3. inputSchema (неизвестные поля, например employee_id, типы, перечисления, шаблоны, required; формат date,
//     date-time, uuid у аргументов верхнего уровня) после применения значений по умолчанию → validation_error
//  4. инструменты записи: confirm.Verify(sub = вызывающий, key = idempotency_key, args как получены) →
//     invalid_confirmation
//
// Личность доходит до обработчиков через механизм bearer-авторизации go-sdk: Handler проверяет JWT, оборачивает его в
// auth.TokenInfo (auth.RequireBearerToken), а streamable-транспорт копирует его в CallToolRequest.Extra.
//
// Ошибки обработчика типа *apierr.Error становятся результатами инструмента с isError=true и structuredContent {code,
// message, retryable} (коды MCP, см. apierr.ToMCP); прочие ошибки и паники логируются и отдаются как общая ошибка без
// повтора. Успешные результаты должны соответствовать outputSchema инструмента; они отправляются как structuredContent
// плюс тот же JSON в текстовом содержимом. tools/list показывает только инструменты, доступные роли вызывающего.
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

// Caller — аутентифицированный пользователь MCP-запроса: проверенные claims JWT плюс исходный bearer-токен. Личность в
// обработчиках инструментов берётся только отсюда, никогда из аргументов инструмента (R71).
type Caller struct {
	authn.Claims
	Token string
}

// HandlerFunc реализует инструмент. args — проверенные аргументы инструмента в JSON (с idempotency_key и
// confirmation_token для инструментов записи; значения по умолчанию из схемы применены). Результат должен
// сериализоваться в JSON-объект, соответствующий outputSchema инструмента. Для ожидаемых сбоев возвращайте
// *apierr.Error.
type HandlerFunc func(ctx context.Context, c Caller, args json.RawMessage) (any, error)

// Config хранит секреты и логгер Server.
type Config struct {
	JWTSecret          []byte       // JWT_SECRET: проверяет bearer-токен вызывающего
	ConfirmationSecret []byte       // CONFIRMATION_HMAC_SECRET: проверяет подтверждения записи
	Logger             *slog.Logger // nil означает slog.Default()
}

// Server — один мок-сервер MCP (absence, approvals, access, checklist, directory, kb).
type Server struct {
	name     string
	cfg      Config
	log      *slog.Logger
	order    []string // имена инструментов в порядке контракта
	tools    map[string]*tool
	handlers map[string]HandlerFunc
	errs     []error
}

// NewServer возвращает сервер, инструменты которого определены в contracts/schemas/<name>.json. Падает, если схемы нет
// или она некорректна. Привяжите каждый инструмент через Handle, затем вызовите Handler (или Mount).
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

// Name возвращает имя сервера; он обслуживается по /mcp/<name>.
func (s *Server) Name() string { return s.name }

// Tools возвращает определения инструментов в порядке контракта.
func (s *Server) Tools() []ToolDef {
	out := make([]ToolDef, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.tools[n].def)
	}
	return out
}

// Handle привязывает h к инструменту с именем name. О неизвестных именах, дубликатах и nil-обработчиках сообщает
// Validate.
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

// Validate сообщает обо всех проблемах регистрации: неизвестные или повторные инструменты, инструменты контракта без
// обработчика, отсутствующие секреты. Handler вызывает его, поэтому неправильно настроенный сервер падает при старте.
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

// Invoke выполняет tool для c через те же проверки, что и MCP tools/call (роль, схема, подтверждение, обработчик,
// выходная схема), без HTTP. Возвращает структурированный результат или ошибку инструмента; мок-серверы используют его
// в тестах.
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
		// Матрица прав требует invalid_confirmation (а не validation_error) для записи без токена.
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

// checkArgs декодирует аргументы, применяет значения по умолчанию из схемы и проверяет их.
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

// hasConfirmationFields сообщает, является ли raw объектом с непустыми строками confirmation_token и idempotency_key.
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

// call запускает обработчик и превращает панику в ошибку.
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

// DecodeArgs разбирает проверенные аргументы инструмента в dst; сбой — validation_error.
func DecodeArgs(args json.RawMessage, dst any) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return apierr.Validation().WithMessage("Некорректные аргументы инструмента: " + err.Error())
	}
	return nil
}

// WithConfirmation возвращает копию args с idempotency_key = key и confirmation_token, подписанным на sub, key и хеш
// args (ядро использует его для approvals.decide и kb.add_faq, где подтверждением служит нажатие кнопки; тесты — для
// инструментов записи).
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
