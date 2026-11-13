// Пакет config читает настройки сервиса из переменных окружения. Env собирает все отсутствующие или некорректные
// переменные, чтобы сервис сразу сообщил обо всех проблемах конфигурации и отказался стартовать (docs/backend §7).
//
//	env := config.New(os.Getenv)
//	cfg := Config{Port: env.Int("CORE_PORT", 8080), JWTSecret: env.Required("JWT_SECRET")}
//	if err := env.Err(); err != nil { return err }
//
// Значения обрезаются по краям; пустое значение считается незаданным (compose часто передаёт VAR= ).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env читает переменные через функцию getenv и записывает проблемы для Err. Не безопасен для конкурентного
// использования.
type Env struct {
	getenv  func(string) string
	missing []string
	invalid []string
}

// New возвращает Env поверх getenv; nil означает os.Getenv.
func New(getenv func(string) string) *Env {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Env{getenv: getenv}
}

func (e *Env) get(key string) string { return strings.TrimSpace(e.getenv(key)) }

// String возвращает значение key или def, если оно не задано.
func (e *Env) String(key, def string) string {
	if v := e.get(key); v != "" {
		return v
	}
	return def
}

// Required возвращает значение key; если оно не задано, ключ записывается как отсутствующий и возвращается "".
func (e *Env) Required(key string) string {
	v := e.get(key)
	if v == "" {
		e.missing = append(e.missing, key)
	}
	return v
}

// Int возвращает key как десятичное целое, def — если не задано; неразбираемое значение записывается как некорректное
// (возвращается def).
func (e *Env) Int(key string, def int) int {
	v := e.get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (want an integer)", key, v))
		return def
	}
	return n
}

// Duration возвращает key как длительность Go ("24h", "90s") или просто число секунд ("30"), def — если не задано;
// неразбираемое или отрицательное значение записывается как некорректное (возвращается def).
func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := e.get(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		secs, aerr := strconv.Atoi(v)
		if aerr != nil {
			e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (want a duration like 30s or 24h)", key, v))
			return def
		}
		d = time.Duration(secs) * time.Second
	}
	if d < 0 {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q (must not be negative)", key, v))
		return def
	}
	return d
}

// List возвращает key, разделённый по запятым, без пустых элементов (например, CORS_ORIGINS), def — если не задано или
// пусто.
func (e *Env) List(key string, def []string) []string {
	var out []string
	for _, part := range strings.Split(e.get(key), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// Err возвращает nil или одну ошибку со списком всех отсутствующих обязательных переменных и всех некорректных
// значений, прочитанных к этому моменту.
func (e *Env) Err() error {
	var parts []string
	if len(e.missing) > 0 {
		parts = append(parts, "missing required environment variables: "+strings.Join(e.missing, ", "))
	}
	if len(e.invalid) > 0 {
		parts = append(parts, "invalid values: "+strings.Join(e.invalid, ", "))
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("config: %s", strings.Join(parts, "; "))
}
