// Пакет apierr — тип ошибки, общий для HTTP API (contracts/openapi.yaml, схема Error) и результатов инструментов MCP
// (contracts/mcp-tools.md, «Errors»). Сообщения на русском, для пользователя, нейтральные; никогда не раскрывают,
// существует ли чужой ресурс (R71).
package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Code — машиночитаемый код ошибки.
type Code string

// Коды HTTP API (openapi Error.code).
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

// Коды, которые есть только в результатах инструментов MCP (forbidden, not_found, validation_error и conflict — общие).
const (
	CodeInvalidConfirmation Code = "invalid_confirmation"
	CodeUnavailable         Code = "unavailable"
)

// Error — ошибка API или инструмента. Status — HTTP-статус, который использует WriteJSON; не сериализуется.
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

// New возвращает ошибку с указанным кодом; пустое сообщение означает русское сообщение кода по умолчанию. Status и
// Retryable берутся из кода (неизвестные коды: 500, без повтора).
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

// Unauthorized возвращает ошибку для отсутствующего или некорректного JWT (401).
func Unauthorized() *Error { return New(CodeUnauthorized, "") }

// Forbidden возвращает ошибку для действия или ресурса, к которому у вызывающего нет доступа, включая чужие данные
// (403).
func Forbidden() *Error { return New(CodeForbidden, "") }

// NotFound возвращает ошибку для отсутствующего ресурса вызывающего (404).
func NotFound() *Error { return New(CodeNotFound, "") }

// Validation возвращает ошибку для некорректных данных запроса или аргументов инструмента (400); обычно дальше идёт
// WithMessage.
func Validation() *Error { return New(CodeValidation, "") }

// Conflict возвращает ошибку для конфликта состояния, например уже решённого согласования (409).
func Conflict() *Error { return New(CodeConflict, "") }

// Expired возвращает ошибку для истёкшего или отменённого черновика (409).
func Expired() *Error { return New(CodeExpired, "") }

// RateLimited возвращает ошибку для слишком частых запросов (429, можно повторить).
func RateLimited() *Error { return New(CodeRateLimited, "") }

// UpstreamUnavailable возвращает ошибку для агента или MCP-сервера, который не ответил (502, можно повторить).
func UpstreamUnavailable() *Error { return New(CodeUpstreamUnavailable, "") }

// Internal возвращает ошибку для неожиданного сбоя сервера (500).
func Internal() *Error { return New(CodeInternal, "") }

// InvalidConfirmation возвращает ошибку инструмента для записи без корректного токена подтверждения (403 при отправке
// по HTTP).
func InvalidConfirmation() *Error { return New(CodeInvalidConfirmation, "") }

// Unavailable возвращает ошибку инструмента для MCP-сервера, у которого недоступна зависимость (503 при отправке по
// HTTP, можно повторить).
func Unavailable() *Error { return New(CodeUnavailable, "") }

// Error реализует error.
func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Is сообщает, является ли target ошибкой *Error с тем же кодом, чтобы работал errors.Is(err, apierr.Forbidden()).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// WithMessage возвращает копию e с другим сообщением для пользователя.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

// As возвращает *Error из цепочки err, если она есть.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

// From превращает любую ошибку в *Error: *Error из цепочки возвращается как есть, дедлайн или отмена контекста
// становятся UpstreamUnavailable, всё остальное — Internal. From(nil) — это nil.
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

// HasCode сообщает, является ли err ошибкой *Error с указанным кодом (или оборачивает её).
func HasCode(err error, code Code) bool {
	e, ok := As(err)
	return ok && e.Code == code
}

// ToAPI переводит коды, которые есть только в MCP, в значения openapi Error.code, сохраняя сообщение и retryable:
// unavailable → upstream_unavailable (502), invalid_confirmation → expired (409). Остальные ошибки возвращаются как
// есть.
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

// ToMCP переводит коды, которые есть только в API, в коды ошибок инструментов MCP, сохраняя сообщение и retryable:
// unauthorized → forbidden, expired → invalid_confirmation, rate_limited/upstream_unavailable/internal и неизвестные
// коды → unavailable. Общие коды возвращаются как есть.
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

// WriteJSON пишет err как тело openapi Error с его HTTP-статусом (коды только MCP переводятся через ToAPI). Ошибки,
// которые не *Error, логируются через slog и отдаются как Internal, чтобы подробности не утекали клиентам.
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
	body, _ := json.Marshal(e) // поля — строки и bool: ошибки быть не может
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
