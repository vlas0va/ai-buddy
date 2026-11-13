// Пакет canonjson выдаёт канонический JSON: побайтно точную форму, которую хешируют и подписывают ядро, MCP-серверы и
// агент на Python (contracts/mcp-tools.md: «ключи отсортированы, без пробелов, UTF-8»).
//
// Результат совпадает с Python
//
//	json.dumps(json.loads(text), sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
//
// для любого значения из объектов, массивов, строк, целых чисел, булевых значений и null:
//   - ключи объектов отсортированы по кодовым точкам Unicode (это тот же порядок, что байтовый порядок UTF-8);
//     при дубликатах ключей в тексте JSON остаётся последнее значение, как в Python;
//   - между токенами нет пробелов;
//   - строки — «сырой» UTF-8; экранируются только '"', '\\' и управляющие символы U+0000–U+001F:
//     \b \f \n \r \t — в короткой форме, остальные — \u00XX со строчными hex; '/', '<', '>', '&', U+007F,
//     U+2028 и U+2029 пишутся как есть;
//   - некорректный UTF-8 в строках Go заменяется на U+FFFD (как делает encoding/json).
//
// Числа: текст числа сохраняется как передан (json.Number, json.RawMessage) или как encoding/json форматирует числа Go.
// Поэтому целые совпадают с Python в точности (любого размера); единственная правка — "-0" → "0", как в Python. Нецелые
// числа НЕ нормализуются: Python переформатирует float через repr ("1.0", "1e+16", 1e2 → "100.0"), Go оставляет "1",
// "1e2" или то, что записал производитель. Не кладите нецелые числа в подписываемые аргументы; в текущих схемах
// инструментов записи их нет.
//
// Инвариант: Marshal(v) == Marshal(json.RawMessage(json.Marshal(v))) для любого v, которое принимает encoding/json.
package canonjson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// Marshal возвращает каноническое JSON-представление v. v может быть любым значением, которое принимает encoding/json
// (map, структуры с json-тегами, срезы, реализации json.Marshaler, json.RawMessage, json.Number). []byte кодируется
// строкой base64, как в encoding/json; текст JSON вместо этого оборачивайте в json.RawMessage.
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

// SHA256Hex возвращает SHA-256 от Marshal(v) строчным hex — форма `args_hash` и `draft_hash`.
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

// writeString пишет s как строку JSON. s — корректный UTF-8 (приходит из encoding/json), и каждый байт многобайтовой
// последовательности >= 0x80, поэтому экранировать побайтно безопасно.
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
