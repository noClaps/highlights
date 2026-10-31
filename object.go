package highlights

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Object is a JSON object that keeps its key order, as JavaScript objects
// do. Theme styles and syntax scopes use it so that output that iterates
// keys, such as ToCSS, follows the theme file.
//
// Values are decoded JSON: string, float64, bool, nil, []any, or *Object.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject builds an object from alternating keys and values.
func NewObject(pairs ...any) *Object {
	o := &Object{}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Keys returns the keys in insertion order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

// Get returns a value and whether the key exists.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.values[key]
	return v, ok
}

// Value returns a value, or nil when the key is absent.
func (o *Object) Value(key string) any {
	v, _ := o.Get(key)
	return v
}

// Set adds or replaces a value. A new key goes last.
func (o *Object) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Len returns the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// UnmarshalJSON decodes a JSON object and keeps its key order.
func (o *Object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return err
	}
	obj, ok := v.(*Object)
	if !ok {
		return fmt.Errorf("expected a JSON object")
	}
	*o = *obj
	return nil
}

// MarshalJSON encodes the object in key order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		val, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &Object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			var arr []any
			for dec.More() {
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if arr == nil {
				arr = []any{}
			}
			return arr, nil
		}
	case json.Number:
		return t.Float64()
	}
	return tok, nil
}

// jsTrim trims what String.prototype.trim treats as whitespace.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
}

// jsNumber converts a decoded JSON value as JavaScript's Number() does.
func jsNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case nil:
		return 0
	case string:
		s := jsTrim(x)
		if s == "" {
			return 0
		}
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			if n, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
				return float64(n)
			}
			return math.NaN()
		}
		if s == "Infinity" || s == "+Infinity" {
			return math.Inf(1)
		}
		if s == "-Infinity" {
			return math.Inf(-1)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || strings.ContainsAny(s, "_infINFaA") {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

// jsRound rounds half up, as Math.round does.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// jsString converts a decoded JSON value as JavaScript template strings do.
func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return formatJSNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func formatJSNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == math.Trunc(f) && math.Abs(f) < 1e21:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
