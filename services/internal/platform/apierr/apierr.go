// Package apierr is the error type shared by the HTTP API (contracts/openapi.yaml, schema Error) and the MCP tool
// results (contracts/mcp-tools.md, "Errors"). Messages are Russian, user-facing, neutral; they never reveal whether
// a foreign resource exists (R71).
package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Code is a machine-readable error code.
type Code string

// Codes of the HTTP API (openapi Error.code).
const (
	CodeUnauthorized        Code = "unauthorized"
	CodeForbidden           Code = "forbidden"
	CodeNotFound            Code = "not_found"
	CodeValidation          Code = "validation_error"
	CodeConflict            Code = "conflict"
	CodeExpired             Code = "expired"
	CodeRateLimited         Code = "rate_limited"
	CodeUpstreamUnavailable Code = "upstream_unavailable"
	CodeInternal            Code = "internal"
)

// Codes that exist only in MCP tool results (forbidden, not_found, validation_error and conflict are shared).
const (
	CodeInvalidConfirmation Code = "invalid_confirmation"
	CodeUnavailable         Code = "unavailable"
)

// Error is an API or tool error. Status is the HTTP status used by WriteJSON; it is not serialised.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Status    int    `json:"-"`
}

type spec struct {
	status    int
	retryable bool
	message   string
}

var specs = map[Code]spec{
	CodeUnauthorized:        {http.StatusUnauthorized, false, "Требуется вход в систему"},
	CodeForbidden:           {http.StatusForbidden, false, "Недостаточно прав для этого действия"},
	CodeNotFound:            {http.StatusNotFound, false, "Не найдено"},
	CodeValidation:          {http.StatusBadRequest, false, "Проверьте введённые данные"},
	CodeConflict:            {http.StatusConflict, false, "Действие уже выполнено или недоступно в текущем состоянии"},
	CodeExpired:             {http.StatusConflict, false, "Срок действия истёк, повторите действие"},
	CodeRateLimited:         {http.StatusTooManyRequests, true, "Слишком много запросов, попробуйте чуть позже"},
	CodeUpstreamUnavailable: {http.StatusBadGateway, true, "Сервис временно недоступен, попробуйте ещё раз"},
	CodeInternal:            {http.StatusInternalServerError, false, "Внутренняя ошибка, попробуйте позже"},
	CodeInvalidConfirmation: {http.StatusForbidden, false, "Подтверждение недействительно или устарело, подтвердите действие ещё раз"},
	CodeUnavailable:         {http.StatusServiceUnavailable, true, "Сервис временно недоступен, попробуйте ещё раз"},
}

// New returns an error with the given code; an empty message means the default Russian message of the code.
// Status and Retryable come from the code (unknown codes: 500, not retryable).
func New(code Code, message string) *Error {
	s, ok := specs[code]
	if !ok {
		s = spec{status: http.StatusInternalServerError, message: specs[CodeInternal].message}
	}
	if message == "" {
		message = s.message
	}
	return &Error{Code: code, Message: message, Retryable: s.retryable, Status: s.status}
}

// Unauthorized returns the error for a missing or invalid JWT (401).
func Unauthorized() *Error { return New(CodeUnauthorized, "") }

// Forbidden returns the error for an action or resource the caller may not access, including other users' data (403).
func Forbidden() *Error { return New(CodeForbidden, "") }

// NotFound returns the error for a missing resource of the caller (404).
func NotFound() *Error { return New(CodeNotFound, "") }

// Validation returns the error for invalid request data or tool arguments (400); usually followed by WithMessage.
func Validation() *Error { return New(CodeValidation, "") }

// Conflict returns the error for a state conflict, e.g. an already decided approval (409).
func Conflict() *Error { return New(CodeConflict, "") }

// Expired returns the error for an expired or cancelled proposal (409).
func Expired() *Error { return New(CodeExpired, "") }

// RateLimited returns the error for too many requests (429, retryable).
func RateLimited() *Error { return New(CodeRateLimited, "") }

// UpstreamUnavailable returns the error for an agent or MCP server that did not answer (502, retryable).
func UpstreamUnavailable() *Error { return New(CodeUpstreamUnavailable, "") }

// Internal returns the error for an unexpected server failure (500).
func Internal() *Error { return New(CodeInternal, "") }

// InvalidConfirmation returns the tool error for a write without a valid confirmation token (403 if sent over HTTP).
func InvalidConfirmation() *Error { return New(CodeInvalidConfirmation, "") }

// Unavailable returns the tool error for an MCP server whose dependency is down (503 if sent over HTTP, retryable).
func Unavailable() *Error { return New(CodeUnavailable, "") }

// Error implements error.
func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Is reports whether target is an *Error with the same code, so errors.Is(err, apierr.Forbidden()) works.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// WithMessage returns a copy of e with a different user-facing message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

// As returns the *Error in err's chain, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

// From converts any error to an *Error: an *Error in the chain is returned as is, context deadline or
// cancellation becomes UpstreamUnavailable, anything else becomes Internal. From(nil) is nil.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := As(err); ok {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return UpstreamUnavailable()
	}
	return Internal()
}

// HasCode reports whether err is (or wraps) an *Error with the given code.
func HasCode(err error, code Code) bool {
	e, ok := As(err)
	return ok && e.Code == code
}

// ToAPI maps MCP-only codes onto openapi Error.code values, keeping message and retryable:
// unavailable → upstream_unavailable (502), invalid_confirmation → expired (409). Other errors are returned as is.
func ToAPI(e *Error) *Error {
	switch e.Code {
	case CodeUnavailable:
		return remap(e, CodeUpstreamUnavailable)
	case CodeInvalidConfirmation:
		return remap(e, CodeExpired)
	}
	if _, ok := specs[e.Code]; !ok {
		return Internal()
	}
	return e
}

// ToMCP maps API-only codes onto MCP tool error codes, keeping message and retryable:
// unauthorized → forbidden, expired → invalid_confirmation, rate_limited/upstream_unavailable/internal and
// unknown codes → unavailable. Shared codes are returned as is.
func ToMCP(e *Error) *Error {
	switch e.Code {
	case CodeForbidden, CodeNotFound, CodeValidation, CodeConflict, CodeInvalidConfirmation, CodeUnavailable:
		return e
	case CodeUnauthorized:
		return remap(e, CodeForbidden)
	case CodeExpired:
		return remap(e, CodeInvalidConfirmation)
	default:
		return remap(e, CodeUnavailable)
	}
}

func remap(e *Error, code Code) *Error {
	c := *e
	c.Code = code
	c.Status = specs[code].status
	return &c
}

// WriteJSON writes err as the openapi Error body with its HTTP status (MCP-only codes mapped by ToAPI).
// Errors that are not *Error are logged with slog and answered as Internal, so details never leak to clients.
func WriteJSON(w http.ResponseWriter, err error) {
	e, ok := As(err)
	if !ok {
		e = From(err)
		if err != nil && e.Code == CodeInternal {
			slog.Default().Error("internal error", "err", err)
		}
	}
	if e == nil {
		e = Internal()
	}
	e = ToAPI(e)
	status := e.Status
	if status == 0 {
		status = New(e.Code, "").Status
	}
	body, _ := json.Marshal(e) // fields are strings and a bool: cannot fail
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
