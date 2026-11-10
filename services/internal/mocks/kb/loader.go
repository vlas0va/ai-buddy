package kb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

var (
	docIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	itemIDRe = regexp.MustCompile(`^CL-[0-9]{2}$`)
	known    = []string{"intern", "mentor", "lead", "hrbp", "alumni"}
)

type frontmatter struct {
	DocID  string `yaml:"doc_id"`
	Title  string `yaml:"title"`
	Source string `yaml:"source_type"`
	ACL    struct {
		Roles []string `yaml:"roles"`
		Teams []string `yaml:"teams"`
	} `yaml:"acl"`
	ChecklistItemID string    `yaml:"checklist_item_id"`
	URL             string    `yaml:"url"`
	Updated         yaml.Node `yaml:"updated"` // any scalar; only a YYYY-MM-DD date is used
}

type document struct {
	frontmatter
	body    string
	updated *time.Time
}

// LoadDir upserts every *.md file of dir (except README.md) into kb.documents (kind doc, no owner) and returns how
// many were loaded. Files need YAML frontmatter with doc_id, title, source_type: test (R69) and acl {roles, teams};
// checklist_item_id is optional; other frontmatter fields (checklist, tags, updated, origin, ...) are ignored, except
// that a valid YYYY-MM-DD `updated` becomes the document date. Any invalid file fails the whole load with an error
// naming it, and nothing is written. Other files are ignored. A missing dir returns 0 and no error.
func LoadDir(ctx context.Context, pool *pgxpool.Pool, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading kb dir %s: %w", dir, err)
	}
	var docs []document
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") || strings.EqualFold(e.Name(), "README.md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return 0, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		d, err := parseDoc(raw)
		if err != nil {
			return 0, fmt.Errorf("kb file %s: %w", e.Name(), err)
		}
		if prev, dup := seen[d.DocID]; dup {
			return 0, fmt.Errorf("kb file %s: doc_id %q already used by %s", e.Name(), d.DocID, prev)
		}
		seen[d.DocID] = e.Name()
		docs = append(docs, d)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("starting kb load: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, d := range docs {
		if err := upsert(ctx, tx, d); err != nil {
			return 0, fmt.Errorf("kb file %s: %w", seen[d.DocID], err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing kb load: %w", err)
	}
	return len(docs), nil
}

func upsert(ctx context.Context, tx pgx.Tx, d document) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO kb.documents (doc_id, title, body, source_type, acl_roles, acl_teams, kind, checklist_item_id, url, updated)
		VALUES ($1, $2, $3, 'test', $4, $5, 'doc', nullif($6, ''), nullif($7, ''), coalesce($8::date, current_date))
		ON CONFLICT (doc_id) DO UPDATE SET title = EXCLUDED.title, body = EXCLUDED.body,
			acl_roles = EXCLUDED.acl_roles, acl_teams = EXCLUDED.acl_teams,
			checklist_item_id = EXCLUDED.checklist_item_id, url = EXCLUDED.url, updated = EXCLUDED.updated
		WHERE kb.documents.kind = 'doc' AND kb.documents.owner_id IS NULL`,
		d.DocID, d.Title, d.body, d.ACL.Roles, d.ACL.Teams, d.ChecklistItemID, d.URL, d.updated)
	if err != nil {
		return fmt.Errorf("upserting %s: %w", d.DocID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("doc_id %q belongs to a non-document record", d.DocID)
	}
	return nil
}

func parseDoc(raw []byte) (document, error) {
	var d document
	raw = bytes.ReplaceAll(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return d, errors.New("no YAML frontmatter (file must start with ---)")
	}
	rest := raw[4:]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return d, errors.New("frontmatter is not closed with ---")
	}
	if err := yaml.Unmarshal(rest[:end], &d.frontmatter); err != nil {
		return d, fmt.Errorf("invalid frontmatter: %w", err)
	}
	d.body = strings.TrimSpace(string(rest[end+4:]))
	var missing []string
	for name, empty := range map[string]bool{
		"doc_id": d.DocID == "", "title": strings.TrimSpace(d.Title) == "", "source_type": d.Source == "",
		"acl.roles": len(d.ACL.Roles) == 0, "acl.teams": len(d.ACL.Teams) == 0, "body": d.body == "",
	} {
		if empty {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return d, fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	if d.Source != "test" {
		return d, fmt.Errorf("source_type must be \"test\", got %q (R69)", d.Source)
	}
	if !docIDRe.MatchString(d.DocID) {
		return d, fmt.Errorf("invalid doc_id %q", d.DocID)
	}
	for _, r := range d.ACL.Roles {
		if !slices.Contains(known, r) {
			return d, fmt.Errorf("unknown role %q in acl.roles", r)
		}
	}
	if d.ChecklistItemID != "" && !itemIDRe.MatchString(d.ChecklistItemID) {
		return d, fmt.Errorf("invalid checklist_item_id %q", d.ChecklistItemID)
	}
	if d.Updated.Kind == yaml.ScalarNode {
		if t, err := time.Parse("2006-01-02", strings.TrimSpace(d.Updated.Value)); err == nil {
			d.updated = &t
		}
	}
	return d, nil
}
