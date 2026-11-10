package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aibuddy/services/internal/platform/apierr"
)

type sendMessage struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

func TestReadJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		limit   int64
		wantErr string // substring of the Russian message; "" = success
	}{
		{"valid", `{"text":"хочу отгул","count":2}`, 0, ""},
		{"trailing whitespace ok", `{"text":"a"}` + "\n\t ", 0, ""},
		{"unknown field", `{"text":"a","employee_id":"intern.boris"}`, 0, `Неизвестное поле "employee_id"`},
		{"empty body", ``, 0, "Пустое тело запроса"},
		{"trailing data", `{"text":"a"} {"text":"b"}`, 0, "Лишние данные"},
		{"type error", `{"count":"two"}`, 0, `Неверный тип поля "count"`},
		{"type error top level", `"text"`, 0, "Неверный тип данных"},
		{"syntax error", `{"text":`, 0, "Некорректный JSON"},
		{"bad token", `{"text" 1}`, 0, "Некорректный JSON"},
		{"too large", `{"text":"` + strings.Repeat("я", 100) + `"}`, 32, "Слишком большой запрос"},
		{"too large after value", `{"text":"a"}` + strings.Repeat(" ", 40000) + "x", 32768, "Слишком большой запрос"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			var dst sendMessage
			var err error
			if tt.limit > 0 {
				err = ReadJSONLimit(rec, req, &dst, tt.limit)
			} else {
				err = ReadJSON(rec, req, &dst)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			e, ok := apierr.As(err)
			if !ok || e.Code != apierr.CodeValidation || e.Status != http.StatusBadRequest {
				t.Fatalf("want validation_error, got %v", err)
			}
			if !strings.Contains(e.Message, tt.wantErr) {
				t.Fatalf("message %q does not contain %q", e.Message, tt.wantErr)
			}
		})
	}
}

func TestReadJSON_NilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = nil
	if err := ReadJSON(httptest.NewRecorder(), req, &sendMessage{}); !apierr.HasCode(err, apierr.CodeValidation) {
		t.Fatalf("got %v", err)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"id": "a1", "title": "Отгул <14.11>"})
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got["title"] != "Отгул <14.11>" {
		t.Fatalf("body %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"internal"`) {
		t.Fatalf("unencodable: %d %s", rec.Code, rec.Body.String())
	}
}
