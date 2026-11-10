// Package httpx holds HTTP helpers shared by core and mcp-mocks: JSON request/response bodies, middleware
// (request id, panic recovery, access log, CORS) and Server-Sent Events writing and reading (contracts/agent-stream.md).
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aibuddy/services/internal/platform/apierr"
)

// DefaultMaxBody is the request body limit of ReadJSON (1 MiB).
const DefaultMaxBody int64 = 1 << 20

// ReadJSON decodes the request body into dst with DefaultMaxBody; see ReadJSONLimit.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return ReadJSONLimit(w, r, dst, DefaultMaxBody)
}

// ReadJSONLimit decodes exactly one JSON value from the request body into dst. Unknown object fields, trailing
// data, an empty body and a body over limit bytes are rejected. Every failure is an *apierr.Error with code
// validation_error and a Russian message.
func ReadJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if r.Body == nil {
		return apierr.Validation().WithMessage("Пустое тело запроса")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return decodeError(err)
		}
		return apierr.Validation().WithMessage("Лишние данные после JSON в теле запроса")
	}
	return nil
}

func decodeError(err error) *apierr.Error {
	var (
		tooBig  *http.MaxBytesError
		typeErr *json.UnmarshalTypeError
	)
	switch {
	case errors.Is(err, io.EOF):
		return apierr.Validation().WithMessage("Пустое тело запроса")
	case errors.As(err, &tooBig):
		return apierr.Validation().WithMessage("Слишком большой запрос")
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return apierr.Validation().WithMessage(fmt.Sprintf("Неверный тип поля %q", typeErr.Field))
		}
		return apierr.Validation().WithMessage("Неверный тип данных в теле запроса")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return apierr.Validation().WithMessage("Неизвестное поле " + field)
	default:
		return apierr.Validation().WithMessage("Некорректный JSON в теле запроса")
	}
}

// WriteJSON writes v as JSON with the given status. If v cannot be encoded, an internal error is written instead.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("httpx: encode response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
