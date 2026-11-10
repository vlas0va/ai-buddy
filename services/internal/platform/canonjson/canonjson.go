// Package canonjson produces canonical JSON: the byte-exact form that core, the MCP servers and the Python agent
// hash and sign (contracts/mcp-tools.md: "keys sorted, no whitespace, UTF-8").
//
// The output is identical to Python's
//
//	json.dumps(json.loads(text), sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
//
// for every value made of objects, arrays, strings, integers, booleans and null:
//   - object keys are sorted by Unicode code point (byte order of UTF-8, which is the same order);
//     duplicate keys in JSON text keep the last value, like Python;
//   - no whitespace between tokens;
//   - strings are raw UTF-8; only '"', '\\' and control characters U+0000–U+001F are escaped:
//     \b \f \n \r \t use the short form, the rest \u00XX with lowercase hex; '/', '<', '>', '&', U+007F,
//     U+2028 and U+2029 are written as is;
//   - invalid UTF-8 in Go strings is replaced by U+FFFD (as encoding/json does).
//
// Numbers: number text is preserved as given (json.Number, json.RawMessage) or as encoding/json formats Go
// numbers. Integers therefore match Python exactly (any size); the only rewrite is "-0" → "0", as in Python.
// Non-integer numbers are NOT normalised: Python re-formats floats with repr ("1.0", "1e+16", 1e2 → "100.0"),
// Go keeps "1", "1e2" or whatever the producer wrote. Do not put non-integer numbers into signed arguments;
// the current write-tool schemas contain none.
//
// Invariant: Marshal(v) == Marshal(json.RawMessage(json.Marshal(v))) for every v encoding/json accepts.
package canonjson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// Marshal returns the canonical JSON encoding of v. v may be any value accepted by encoding/json (maps, structs
// with json tags, slices, json.Marshaler implementations, json.RawMessage, json.Number). A []byte is encoded as a
// base64 string, as encoding/json does; wrap JSON text in json.RawMessage instead.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonjson: marshal: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("canonjson: decode: %w", err)
	}
	var buf bytes.Buffer
	buf.Grow(len(raw))
	if err := encode(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SHA256Hex returns the lowercase hex SHA-256 of Marshal(v), the form of `args_hash` and `draft_hash`.
func SHA256Hex(v any) (string, error) {
	b, err := Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func encode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		if x == "-0" {
			x = "0"
		}
		buf.WriteString(string(x))
	case string:
		writeString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encode(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := encode(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonjson: unexpected decoded type %T", v)
	}
	return nil
}

const hexDigits = "0123456789abcdef"

// writeString writes s as a JSON string. s is valid UTF-8 (it comes from encoding/json), and every byte of a
// multi-byte sequence is >= 0x80, so escaping byte by byte is safe.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		buf.WriteString(s[start:i])
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			buf.WriteString(`\u00`)
			buf.WriteByte(hexDigits[c>>4])
			buf.WriteByte(hexDigits[c&0xf])
		}
		start = i + 1
	}
	buf.WriteString(s[start:])
	buf.WriteByte('"')
}
