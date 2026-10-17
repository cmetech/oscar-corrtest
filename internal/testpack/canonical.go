package testpack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CanonicalJSON implements the kit's Python canonical contract, including LF.
// It does not change the generic OSCAR JSON codec. Only safe integers are allowed.
func CanonicalJSON(raw []byte) ([]byte, error) { return canonicalJSON(raw) }
func canonicalJSON(raw []byte) ([]byte, error) {
	v, e := parseJSON(raw)
	if e != nil {
		return nil, e
	}
	return encodeCanonical(v), nil
}
func parseJSON(raw []byte) (any, error) {
	if len(raw) > maxBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid or oversized UTF-8 JSON")
	}
	// encoding/json replaces unpaired UTF-16 escapes rather than refusing them.
	// Validate escapes before decoding, without treating literal backslash-u as escapes.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for ; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				break
			}
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return nil, fmt.Errorf("short Unicode escape")
			}
			n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if e != nil {
				return nil, e
			}
			i += 4
			if n >= 0xd800 && n <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return nil, fmt.Errorf("unpaired surrogate")
				}
				low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if e != nil || low < 0xdc00 || low > 0xdfff {
					return nil, fmt.Errorf("unpaired surrogate")
				}
				i += 6
			} else if n >= 0xdc00 && n <= 0xdfff {
				return nil, fmt.Errorf("unpaired surrogate")
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := jsonValue(d, 0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return v, nil
}
func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("JSON nesting exceeds bound")
	}
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("invalid key")
				}
				if _, ok = m[s]; ok {
					return nil, fmt.Errorf("duplicate key")
				}
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			if t, e = d.Token(); e != nil || t != json.Delim('}') {
				return nil, fmt.Errorf("invalid object")
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			if t, e = d.Token(); e != nil || t != json.Delim(']') {
				return nil, fmt.Errorf("invalid array")
			}
			return a, nil
		}
		return nil, fmt.Errorf("invalid delimiter")
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return nil, fmt.Errorf("integers only")
		}
		n, e := strconv.ParseInt(string(x), 10, 64)
		if e != nil || n < -9007199254740991 || n > 9007199254740991 {
			return nil, fmt.Errorf("unsafe integer")
		}
		return n, nil
	case nil, bool, string:
		return x, nil
	}
	return nil, fmt.Errorf("unsupported JSON value")
}
func encodeCanonical(v any) []byte {
	var b bytes.Buffer
	writeCanonical(&b, v)
	b.WriteByte('\n')
	return b.Bytes()
}
func writeCanonical(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case string:
		quoteCanonical(b, x)
	case []any:
		b.WriteByte('[')
		for i, v := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, v)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			quoteCanonical(b, k)
			b.WriteByte(':')
			writeCanonical(b, x[k])
		}
		b.WriteByte('}')
	default:
		panic("internal noncanonical type")
	}
}
func quoteCanonical(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 32 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteRune(c)
			}
		}
	}
	b.WriteByte('"')
}
func object(v any, keys ...string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != len(keys) {
		return nil, fmt.Errorf("unexpected object fields")
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return nil, fmt.Errorf("missing field %s", k)
		}
	}
	return m, nil
}
