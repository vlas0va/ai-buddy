// Package directory implements the mock "directory" MCP server: who the caller is and whom to ask (S2, CL-02, CL-03).
// Data is synthetic (R68). Identity comes only from the verified JWT (R71).
package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/period"
)

// askAbout holds the fixed Russian topics to raise with each kind of contact.
var askAbout = map[string][]string{
	"mentor":  {"задачи и код-ревью", "доступы и инструменты команды", "план адаптации"},
	"lead":    {"отгул и отсутствия", "цели стажировки"},
	"hrbp":    {"кадровые вопросы"},
	"curator": {"методичка наставника", "экватор и выпускной", "документы для вуза"},
}

// Register binds every tool of the directory contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	return register(srv, pool, time.Now)
}

// register is Register with an injectable clock (tests).
func register(srv *mcpx.Server, pool *pgxpool.Pool, now func() time.Time) error {
	if srv == nil || pool == nil {
		return errors.New("directory: server and pool are required")
	}
	srv.Handle("whoami", func(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
		return whoami(ctx, pool, c, now())
	})
	srv.Handle("get_my_contacts", func(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
		return contacts(ctx, pool, c)
	})
	return nil
}

type whoamiOut struct {
	EmployeeID    string `json:"employee_id"`
	DisplayName   string `json:"display_name"`
	Role          string `json:"role"`
	TeamID        string `json:"team_id"`
	TeamName      string `json:"team_name,omitempty"`
	CohortStart   string `json:"cohort_start,omitempty"`
	CurrentPeriod string `json:"current_period,omitempty"`
}

func whoami(ctx context.Context, pool *pgxpool.Pool, c mcpx.Caller, now time.Time) (any, error) {
	var (
		out   whoamiOut
		start *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT e.id, e.display_name, e.role, e.team_id, coalesce(t.name, ''), e.cohort_start
		FROM directory.employees e LEFT JOIN directory.teams t ON t.id = e.team_id
		WHERE e.id = $1`, c.Sub).Scan(&out.EmployeeID, &out.DisplayName, &out.Role, &out.TeamID, &out.TeamName, &start)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.Forbidden()
	}
	if err != nil {
		return nil, fmt.Errorf("loading employee: %w", err)
	}
	var cohort time.Time
	switch {
	case start != nil:
		cohort = *start
	case c.CohortStart != "":
		if d, perr := period.ParseDate(c.CohortStart); perr == nil {
			cohort = d
		}
	}
	if !cohort.IsZero() {
		out.CohortStart = cohort.Format("2006-01-02")
		out.CurrentPeriod = string(period.Current(cohort, now))
	}
	return out, nil
}

type contact struct {
	Role        string   `json:"role"`
	EmployeeID  string   `json:"employee_id"`
	DisplayName string   `json:"display_name"`
	Channel     string   `json:"channel,omitempty"`
	AskAbout    []string `json:"ask_about"`
}

func contacts(ctx context.Context, pool *pgxpool.Pool, c mcpx.Caller) (any, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.role, p.id, p.display_name, coalesce(p.channel, '')
		FROM directory.employees e
		CROSS JOIN LATERAL (VALUES (1, 'mentor', e.mentor_id), (2, 'lead', e.lead_id),
			(3, 'hrbp', e.hrbp_id), (4, 'curator', e.curator_id)) AS r(ord, role, pid)
		JOIN directory.employees p ON p.id = r.pid
		WHERE e.id = $1
		ORDER BY r.ord`, c.Sub)
	if err != nil {
		return nil, fmt.Errorf("loading contacts: %w", err)
	}
	defer rows.Close()
	items := []contact{}
	for rows.Next() {
		var ct contact
		if err := rows.Scan(&ct.Role, &ct.EmployeeID, &ct.DisplayName, &ct.Channel); err != nil {
			return nil, fmt.Errorf("scanning contact: %w", err)
		}
		ct.AskAbout = askAbout[ct.Role]
		items = append(items, ct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading contacts: %w", err)
	}
	return map[string]any{"items": items}, nil
}
