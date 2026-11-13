// Пакет consents реализует мок-сервер MCP «consents»: согласия сотрудника с их статусами (кейс «Пространство решений»:
// согласия). Данные синтетические (R68). Согласия, подписанные в HR-портале при найме (персональные данные, NDA),
// только показываются; добровольные (фото и видео, опросы) сотрудник из JWT может подписать после явного подтверждения
// (R71). Подписание идемпотентно по ключу (R79).
package consents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

// Register привязывает каждый инструмент контракта consents к srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	if srv == nil || pool == nil {
		return errors.New("consents: server and pool are required")
	}
	if srv.Name() != "consents" {
		return fmt.Errorf("consents: cannot register on server %q", srv.Name())
	}
	s := &service{pool: pool}
	srv.Handle("list", s.list)
	srv.Handle("sign", s.sign)
	return nil
}

type service struct {
	pool *pgxpool.Pool
}

type consent struct {
	ConsentID   string `json:"consent_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Signable    bool   `json:"signable"`
	Required    bool   `json:"required"`
	Status      string `json:"status"`
	SignedVia   string `json:"signed_via,omitempty"`
	SignedAt    string `json:"signed_at,omitempty"`
}

type listOut struct {
	Items []consent `json:"items"`
}

// list возвращает все виды согласий со статусом вызывающего.
func (s *service) list(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	rows, err := s.pool.Query(ctx, `
SELECT t.id, t.title, t.description, t.signable, t.required, g.via, g.signed_at
FROM consents.types t
LEFT JOIN consents.signatures g ON g.consent_id = t.id AND g.employee_id = $1
ORDER BY t.position, t.id`, c.Sub)
	if err != nil {
		return nil, fmt.Errorf("list consents: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (consent, error) {
		var (
			it  consent
			via *string
			at  *time.Time
		)
		err := row.Scan(&it.ConsentID, &it.Title, &it.Description, &it.Signable, &it.Required, &via, &at)
		it.Status = "not_signed"
		if at != nil {
			it.Status, it.SignedVia, it.SignedAt = "signed", *via, at.UTC().Format(time.RFC3339)
		}
		return it, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan consents: %w", err)
	}
	if items == nil {
		items = []consent{}
	}
	return listOut{Items: items}, nil
}

type signIn struct {
	ConsentID      string `json:"consent_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type signature struct {
	ID        string `json:"id"`
	ConsentID string `json:"consent_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	SignedAt  string `json:"signed_at"`
	Replayed  *bool  `json:"replayed,omitempty"`

	employeeID string
	key        string
}

func findSignature(ctx context.Context, q pgx.Tx, where string, args ...any) (signature, bool, error) {
	var (
		g  signature
		at time.Time
	)
	err := q.QueryRow(ctx, `
SELECT g.id, g.consent_id, t.title, g.signed_at, g.employee_id, coalesce(g.idempotency_key::text, '')
FROM consents.signatures g JOIN consents.types t ON t.id = g.consent_id
WHERE `+where, args...).Scan(&g.ID, &g.ConsentID, &g.Title, &at, &g.employeeID, &g.key)
	if errors.Is(err, pgx.ErrNoRows) {
		return signature{}, false, nil
	}
	if err != nil {
		return signature{}, false, fmt.Errorf("query signature: %w", err)
	}
	g.Status, g.SignedAt = "signed", at.UTC().Format(time.RFC3339)
	return g, true, nil
}

// sign записывает добровольное согласие вызывающего. Согласия, которые подписываются только в HR-портале, отклоняются;
// уже подписанное согласие — conflict; тот же ключ возвращает сохранённую подпись с replayed=true.
func (s *service) sign(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in signIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "consents:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	prev, found, err := findSignature(ctx, tx, "g.idempotency_key = $1::uuid", in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if found {
		if prev.employeeID != c.Sub || prev.ConsentID != in.ConsentID {
			return nil, keyConflict()
		}
		replayed := true
		prev.Replayed = &replayed
		return prev, nil
	}
	var (
		title    string
		signable bool
	)
	err = tx.QueryRow(ctx, `SELECT title, signable FROM consents.types WHERE id = $1`, in.ConsentID).Scan(&title, &signable)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.NotFound().WithMessage("Такого согласия нет")
	}
	if err != nil {
		return nil, fmt.Errorf("load consent: %w", err)
	}
	if !signable {
		return nil, apierr.Forbidden().WithMessage("«" + title + "» подписывается только в кадровом портале при оформлении")
	}
	if done, ok, err := findSignature(ctx, tx, "g.employee_id = $1 AND g.consent_id = $2", c.Sub, in.ConsentID); err != nil {
		return nil, err
	} else if ok {
		return nil, apierr.Conflict().WithMessage("Согласие «" + title + "» уже подписано (" + done.ID + ")")
	}
	var id string
	err = tx.QueryRow(ctx, `
INSERT INTO consents.signatures (employee_id, consent_id, via, idempotency_key)
VALUES ($1, $2, 'buddy', $3::uuid) RETURNING id`, c.Sub, in.ConsentID, in.IdempotencyKey).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("insert signature: %w", err)
	}
	out, _, err := findSignature(ctx, tx, "g.id = $1", id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	out.Replayed = new(bool)
	return out, nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
