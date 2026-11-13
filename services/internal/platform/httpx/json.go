// Пакет httpx хранит HTTP-помощники, общие для ядра и mcp-mocks: JSON-тела запросов и ответов, middleware (id запроса,
// перехват паник, журнал доступа, CORS) и запись и чтение Server-Sent Events (contracts/agent-stream.md).
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

// DefaultMaxBody — ограничение тела запроса в ReadJSON (1 МиБ).
const DefaultMaxBody int64 = 1 << 20

// ReadJSON декодирует тело запроса в dst с DefaultMaxBody; см. ReadJSONLimit.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return ReadJSONLimit(w, r, dst, DefaultMaxBody)
}

// ReadJSONLimit декодирует ровно одно JSON-значение из тела запроса в dst. Неизвестные поля объекта, лишние данные в
// конце, пустое тело и тело больше limit байт отклоняются. Любая неудача — *apierr.Error с кодом validation_error и
// русским сообщением.
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

// WriteJSON пишет v как JSON с указанным статусом. Если v не кодируется, вместо него пишется внутренняя ошибка.
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
