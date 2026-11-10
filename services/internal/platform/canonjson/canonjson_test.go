package canonjson

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
)

type vector struct {
	Input     string `json:"input"`
	Canonical string `json:"canonical"`
	SHA256    string `json:"sha256"`
}

// Golden vectors are produced by Python (testdata/gen_vectors.py) so the agent and Go agree byte for byte.
func TestMarshal_PythonGoldenVectors_R71(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 10 {
		t.Fatalf("expected at least 10 vectors, got %d", len(vectors))
	}
	for _, v := range vectors {
		t.Run(v.Input, func(t *testing.T) {
			got, err := Marshal(json.RawMessage(v.Input))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != v.Canonical {
				t.Fatalf("canonical mismatch\n got: %s\nwant: %s", got, v.Canonical)
			}
			h, err := SHA256Hex(json.RawMessage(v.Input))
			if err != nil {
				t.Fatalf("SHA256Hex: %v", err)
			}
			if h != v.SHA256 {
				t.Fatalf("hash mismatch: got %s want %s", h, v.SHA256)
			}
		})
	}
}

type draftArgs struct {
	Type      string `json:"type"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Comment   string `json:"comment"`
}

func TestMarshal_GoValues(t *testing.T) {
	n := 7
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, `null`},
		{"struct uses json tags and sorts", draftArgs{"day_off", "2026-11-14", "2026-11-14", ""},
			`{"comment":"","end_date":"2026-11-14","start_date":"2026-11-14","type":"day_off"}`},
		{"map with nested raw message", map[string]any{"b": json.RawMessage(` { "y" : 1, "x" : [ ] } `), "a": "<&>"},
			`{"a":"<&>","b":{"x":[],"y":1}}`},
		{"json.Number preserved", map[string]any{"n": json.Number("12345678901234567890"), "f": json.Number("1.50")},
			`{"f":1.50,"n":12345678901234567890}`},
		{"negative zero integer", json.Number("-0"), `0`},
		{"go ints", []any{int64(-5), uint8(3), 0}, `[-5,3,0]`},
		{"go floats keep encoding/json form", []any{1.0, 0.5, 1e21}, `[1,0.5,1e+21]`},
		{"pointer", &n, `7`},
		{"invalid utf8 replaced", "a\xffb", "\"a�b\""},
		{"control chars", "\x01\x7f ", `"\u0001` + "\x7f " + `"`},
		{"empty containers", map[string]any{"a": []any{}, "b": map[string]any{}}, `{"a":[],"b":{}}`},
		{"bool false", false, `false`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(tt.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

// Canonical form of a value equals the canonical form of its encoding/json text, so a struct hashed in core
// and the raw arguments hashed by an MCP server agree (R79 idempotency relies on stable hashes).
func TestMarshal_ValueAndTextAgree_R79(t *testing.T) {
	values := []any{
		draftArgs{"day_off", "2026-11-14", "2026-11-15", "Семейные дела"},
		map[string]any{"resource_id": "space-delivery", "reason": "", "n": 3, "f": 2.5},
		[]any{"x", true, nil, map[string]any{"я": "а"}},
	}
	for _, v := range values {
		text, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Marshal(json.RawMessage(text))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("value %s and text %s differ", a, b)
		}
		ha, _ := SHA256Hex(v)
		hb, _ := SHA256Hex(json.RawMessage(text))
		if ha != hb || len(ha) != 64 {
			t.Fatalf("hashes differ or wrong length: %s %s", ha, hb)
		}
	}
}

func TestMarshal_Deterministic(t *testing.T) {
	m := map[string]any{}
	for _, k := range []string{"k1", "k9", "k3", "k7", "k5", "k2", "k8", "k4", "k6", "k0"} {
		m[k] = map[string]any{"z": k, "a": []any{k, k}}
	}
	first, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, _ := Marshal(m)
		if !bytes.Equal(got, first) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestMarshal_Errors(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"NaN", math.NaN()},
		{"Inf", math.Inf(1)},
		{"channel", make(chan int)},
		{"invalid raw message", json.RawMessage(`{"a":`)},
		{"invalid number", json.Number("1x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Marshal(tt.in); err == nil {
				t.Fatal("expected error")
			}
			if _, err := SHA256Hex(tt.in); err == nil {
				t.Fatal("expected error from SHA256Hex")
			}
		})
	}
}

func TestEncode_UnexpectedType(t *testing.T) {
	var buf bytes.Buffer
	if err := encode(&buf, []any{struct{}{}}); err == nil {
		t.Fatal("expected error for unexpected type in array")
	}
	if err := encode(&buf, map[string]any{"a": 1.5}); err == nil {
		t.Fatal("expected error for unexpected type in object")
	}
}
