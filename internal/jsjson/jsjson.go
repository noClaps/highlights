// Package jsjson writes JSON exactly as JavaScript's JSON.stringify does,
// so values produced in Go can be compared byte for byte with values
// recorded from the TypeScript implementation.
package jsjson

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Pair is one key and value of an ordered object.
type Pair struct {
	Key   string
	Value any
}

// Obj is an object whose keys keep their order.
type Obj []Pair

// Raw is pre-encoded JSON.
type Raw string

// Marshal encodes v. Supported values: nil, bool, int, int64, uint32,
// float64, string, Raw, Obj, []any, and slices of those through []any.
func Marshal(v any) string {
	var sb strings.Builder
	write(&sb, v)
	return sb.String()
}

func write(sb *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case int:
		sb.WriteString(strconv.Itoa(x))
	case int64:
		sb.WriteString(strconv.FormatInt(x, 10))
	case uint32:
		sb.WriteString(strconv.FormatUint(uint64(x), 10))
	case float64:
		sb.WriteString(Number(x))
	case string:
		String(sb, x)
	case Raw:
		sb.WriteString(string(x))
	case Obj:
		sb.WriteByte('{')
		for i, p := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			String(sb, p.Key)
			sb.WriteByte(':')
			write(sb, p.Value)
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			write(sb, e)
		}
		sb.WriteByte(']')
	default:
		panic("jsjson: unsupported value")
	}
}

// Number formats a finite number as JavaScript does.
func Number(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// JavaScript writes 1e-7 and 1e+21 without exponent padding
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[0]
		exp = strings.TrimLeft(exp[1:], "0")
		return mant + "e" + string(sign) + exp
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

const hex = "0123456789abcdef"

// String writes a JSON string literal. Valid UTF-8 is written as is;
// malformed bytes are not expected and are written as U+FFFD.
func String(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			switch c {
			case '"':
				sb.WriteString(`\"`)
			case '\\':
				sb.WriteString(`\\`)
			case '\b':
				sb.WriteString(`\b`)
			case '\f':
				sb.WriteString(`\f`)
			case '\n':
				sb.WriteString(`\n`)
			case '\r':
				sb.WriteString(`\r`)
			case '\t':
				sb.WriteString(`\t`)
			default:
				if c < 0x20 {
					sb.WriteString(`\u00`)
					sb.WriteByte(hex[c>>4])
					sb.WriteByte(hex[c&15])
				} else {
					sb.WriteByte(c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			sb.WriteString("�")
		} else {
			sb.WriteString(s[i : i+size])
		}
		i += size
	}
	sb.WriteByte('"')
}

// O builds an object from alternating keys and values.
func O(kv ...any) Obj {
	o := make(Obj, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		o = append(o, Pair{Key: kv[i].(string), Value: kv[i+1]})
	}
	return o
}
