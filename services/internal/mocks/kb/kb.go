// Package kb implements the mock "kb" MCP server: the synthetic knowledge base with ACL applied before anything
// reaches the LLM (R71). Every document is a test instruction of the prototype (R69).
package kb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

const (
	defaultK   = 5
	maxSnippet = 300
)

// Register binds every tool of the kb contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	if srv == nil || pool == nil {
		return errors.New("kb: server and pool are required")
	}
	srv.Handle("search", func(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
		var in struct {
			Query string `json:"query"`
			K     int    `json:"k"`
		}
		if err := mcpx.DecodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.K <= 0 {
			in.K = defaultK
		}
		return search(ctx, pool, c, in.Query, in.K)
	})
	srv.Handle("add_faq", func(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
		var in struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Key   string `json:"idempotency_key"`
		}
		if err := mcpx.DecodeArgs(args, &in); err != nil {
			return nil, err
		}
		return addFAQ(ctx, pool, c, in.Title, in.Body, in.Key)
	})
	return nil
}

type hit struct {
	DocID      string  `json:"doc_id"`
	Title      string  `json:"title"`
	Snippet    string  `json:"snippet"`
	SourceType string  `json:"source_type"`
	URL        string  `json:"url"`
	Score      float64 `json:"score"`
}

// aclRole maps the HR admin to the HR BP audience of the test documents (the HR admin decides absence requests and
// reads the same HR instructions).
func aclRole(role string) string {
	if role == "hr_admin" {
		return "hrbp"
	}
	return role
}

// search returns documents the caller may see: role and team ACL, and private notes of the caller only.
func search(ctx context.Context, pool *pgxpool.Pool, c mcpx.Caller, query string, k int) (any, error) {
	rows, err := pool.Query(ctx, `
		SELECT d.doc_id, d.title, d.source_type, coalesce(d.url, ''),
		       ts_headline('russian', d.body, q.q, 'StartSel="", StopSel="", MinWords=12, MaxWords=35, ShortWord=2'),
		       ts_rank(d.tsv, q.q)::float8
		FROM kb.documents d, websearch_to_tsquery('russian', $1) AS q(q)
		WHERE d.tsv @@ q.q
		  AND $2 = ANY(d.acl_roles)
		  AND ('*' = ANY(d.acl_teams) OR $3 = ANY(d.acl_teams))
		  AND (d.owner_id IS NULL OR d.owner_id = $4)
		ORDER BY 6 DESC, d.doc_id
		LIMIT $5`, query, aclRole(c.Role), c.TeamID, c.Sub, k)
	if err != nil {
		return nil, fmt.Errorf("searching knowledge base: %w", err)
	}
	defer rows.Close()
	items := []hit{}
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.DocID, &h.Title, &h.SourceType, &h.URL, &h.Snippet, &h.Score); err != nil {
			return nil, fmt.Errorf("scanning search hit: %w", err)
		}
		h.Snippet = truncateRunes(h.Snippet, maxSnippet)
		items = append(items, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading search hits: %w", err)
	}
	return map[string]any{"items": items}, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

type faqOut struct {
	DocID      string `json:"doc_id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	Replayed   bool   `json:"replayed"`
}

// addFAQ stores a mentor answer as an FAQ document of the mentor's team; idempotent by key (R79).
func addFAQ(ctx context.Context, pool *pgxpool.Pool, c mcpx.Caller, title, body, key string) (any, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("generating doc id: %w", err)
	}
	docID := fmt.Sprintf("faq-%s-%s", c.TeamID, hex.EncodeToString(suffix))
	var out faqOut
	err := pool.QueryRow(ctx, `
		INSERT INTO kb.documents (doc_id, title, body, source_type, acl_roles, acl_teams, kind, created_by, idempotency_key)
		VALUES ($1, $2, $3, 'test', ARRAY['intern','mentor','lead'], ARRAY[$4::text], 'faq', $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING doc_id, title, source_type`, docID, title, body, c.TeamID, c.Sub, key).
		Scan(&out.DocID, &out.Title, &out.SourceType)
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("inserting faq: %w", err)
	}
	// The key was used before: replay the stored result when it is the same request, otherwise conflict.
	var createdBy, storedBody string
	err = pool.QueryRow(ctx, `SELECT doc_id, title, body, coalesce(created_by, '') FROM kb.documents WHERE idempotency_key = $1`, key).
		Scan(&out.DocID, &out.Title, &storedBody, &createdBy)
	if err != nil {
		return nil, fmt.Errorf("loading stored faq: %w", err)
	}
	if createdBy != c.Sub || out.Title != title || storedBody != body {
		return nil, apierr.Conflict().WithMessage("Этот ключ идемпотентности уже использован с другими данными")
	}
	out.SourceType = "test"
	out.Replayed = true
	return out, nil
}
