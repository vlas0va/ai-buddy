// Package checklist implements the mock "checklist" MCP server: the Avito intern checklist template (S1).
package checklist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/period"
)

// Register binds every tool of the checklist contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	if srv == nil || pool == nil {
		return errors.New("checklist: server and pool are required")
	}
	srv.Handle("get_plan_template", func(ctx context.Context, _ mcpx.Caller, args json.RawMessage) (any, error) {
		var in struct {
			Role string `json:"role"`
		}
		if err := mcpx.DecodeArgs(args, &in); err != nil {
			return nil, err
		}
		return template(ctx, pool, in.Role)
	})
	return nil
}

type periodOut struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type itemOut struct {
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Period      string          `json:"period"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Steps       json.RawMessage `json:"steps"`
	Scenarios   []string        `json:"scenarios"`
	HumanOnly   bool            `json:"human_only"`
}

func template(ctx context.Context, pool *pgxpool.Pool, role string) (any, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, role, period, title, coalesce(description, ''), steps, scenarios, human_only
		FROM checklist.items WHERE role IN ($1, 'both') ORDER BY position, id`, role)
	if err != nil {
		return nil, fmt.Errorf("loading checklist items: %w", err)
	}
	defer rows.Close()
	items := []itemOut{}
	for rows.Next() {
		var it itemOut
		var steps []byte
		if err := rows.Scan(&it.ID, &it.Role, &it.Period, &it.Title, &it.Description, &steps, &it.Scenarios, &it.HumanOnly); err != nil {
			return nil, fmt.Errorf("scanning checklist item: %w", err)
		}
		if len(steps) == 0 || string(steps) == "null" {
			steps = []byte("[]")
		}
		it.Steps = steps
		if it.Scenarios == nil {
			it.Scenarios = []string{}
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading checklist items: %w", err)
	}
	periods := []periodOut{}
	for _, p := range period.All() {
		periods = append(periods, periodOut{ID: string(p), Label: period.Label(p)})
	}
	return map[string]any{"periods": periods, "items": items}, nil
}
