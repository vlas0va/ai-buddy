package mcpx

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"aibuddy/contracts"
)

// ToolDef is one tool of contracts/schemas/<server>.json.
type ToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Roles        []string        `json:"roles"`
	Write        bool            `json:"write"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema"`
}

type schemaFile struct {
	Server string    `json:"server"`
	Tools  []ToolDef `json:"tools"`
}

// LoadTools reads the tool definitions of server from contracts.Schemas (schemas/<server>.json).
func LoadTools(server string) ([]ToolDef, error) {
	return loadTools(contracts.Schemas, server)
}

func loadTools(fsys fs.FS, server string) ([]ToolDef, error) {
	raw, err := fs.ReadFile(fsys, "schemas/"+server+".json")
	if err != nil {
		return nil, fmt.Errorf("mcpx: load schema %q: %w", server, err)
	}
	var f schemaFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("mcpx: parse schema %q: %w", server, err)
	}
	if f.Server != server {
		return nil, fmt.Errorf("mcpx: schema file %q declares server %q", server, f.Server)
	}
	return f.Tools, nil
}

// tool is a compiled ToolDef.
type tool struct {
	def     ToolDef
	input   *jsonschema.Resolved
	output  *jsonschema.Resolved // nil when the contract has no output schema
	formats map[string]string    // top-level argument -> format (date, date-time, uuid)
}

func compile(def ToolDef) (*tool, error) {
	if def.Name == "" {
		return nil, fmt.Errorf("mcpx: tool without name")
	}
	if len(def.Roles) == 0 {
		return nil, fmt.Errorf("mcpx: tool %q: no roles", def.Name)
	}
	in, props, err := resolve(def.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcpx: tool %q: input schema: %w", def.Name, err)
	}
	if def.Write {
		for _, k := range []string{"idempotency_key", "confirmation_token"} {
			if _, ok := props[k]; !ok {
				return nil, fmt.Errorf("mcpx: write tool %q: input schema lacks %s", def.Name, k)
			}
		}
	}
	t := &tool{def: def, input: in, formats: map[string]string{}}
	for name, p := range props {
		if p != nil && formatCheckers[p.Format] != nil {
			t.formats[name] = p.Format
		}
	}
	if len(def.OutputSchema) > 0 {
		if t.output, _, err = resolve(def.OutputSchema); err != nil {
			return nil, fmt.Errorf("mcpx: tool %q: output schema: %w", def.Name, err)
		}
	}
	return t, nil
}

func resolve(raw json.RawMessage) (*jsonschema.Resolved, map[string]*jsonschema.Schema, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, nil, err
	}
	if s.Type != "object" {
		return nil, nil, fmt.Errorf(`type must be "object", got %q`, s.Type)
	}
	r, err := s.Resolve(nil)
	if err != nil {
		return nil, nil, err
	}
	return r, s.Properties, nil
}

func (t *tool) allows(role string) bool { return slices.Contains(t.def.Roles, role) }

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// formatCheckers enforce the "format" keywords the contracts use (jsonschema-go treats format as annotation only).
var formatCheckers = map[string]func(string) bool{
	"date":      func(s string) bool { _, err := time.Parse(time.DateOnly, s); return err == nil },
	"date-time": func(s string) bool { _, err := time.Parse(time.RFC3339, s); return err == nil },
	"uuid":      uuidRE.MatchString,
}

var formatNames = map[string]string{
	"date":      "дата в формате ГГГГ-ММ-ДД",
	"date-time": "дата и время в формате RFC 3339",
	"uuid":      "UUID",
}

// checkFormats validates the format of top-level string arguments.
func (t *tool) checkFormats(args map[string]any) error {
	for name, format := range t.formats {
		s, ok := args[name].(string)
		if ok && !formatCheckers[format](s) {
			return fmt.Errorf("поле %s: ожидается %s", name, formatNames[format])
		}
	}
	return nil
}
