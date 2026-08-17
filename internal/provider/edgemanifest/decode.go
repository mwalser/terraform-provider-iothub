package edgemanifest

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// toGo converts a wholly known Terraform value into plain Go: objects and
// maps become map[string]any, tuples, lists and sets []any, strings string,
// numbers *big.Float, bools bool and null nil.
func toGo(v tftypes.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}
	if !v.IsKnown() {
		return nil, fmt.Errorf("unknown value")
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.String):
		var s string
		if err := v.As(&s); err != nil {
			return nil, err
		}
		return s, nil
	case t.Is(tftypes.Number):
		var f *big.Float
		if err := v.As(&f); err != nil {
			return nil, err
		}
		return f, nil
	case t.Is(tftypes.Bool):
		var b bool
		if err := v.As(&b); err != nil {
			return nil, err
		}
		return b, nil
	case t.Is(tftypes.Object{}) || t.Is(tftypes.Map{}):
		var m map[string]tftypes.Value
		if err := v.As(&m); err != nil {
			return nil, err
		}
		out := make(map[string]any, len(m))
		for k, e := range m {
			g, err := toGo(e)
			if err != nil {
				return nil, err
			}
			out[k] = g
		}
		return out, nil
	case t.Is(tftypes.Tuple{}) || t.Is(tftypes.List{}) || t.Is(tftypes.Set{}):
		var l []tftypes.Value
		if err := v.As(&l); err != nil {
			return nil, err
		}
		out := make([]any, 0, len(l))
		for _, e := range l {
			g, err := toGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value type %s", t)
}

// kindOf names a decoded value for error messages.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case *big.Float:
		return "a number"
	case bool:
		return "a bool"
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	}
	return fmt.Sprintf("%T", v)
}

// problems collects validation failures as "path: message" lines.
type problems []string

func (p *problems) add(path, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if path == "" {
		*p = append(*p, msg)
		return
	}
	*p = append(*p, path+": "+msg)
}

// object reads one decoded object strictly: every getter checks the type of
// what it finds, a null attribute counts as absent, and finish reports keys
// nobody asked for. Getters report problems and return ok=false rather than
// failing fast, so one run lists everything that is wrong.
type object struct {
	path string
	m    map[string]any
	seen map[string]bool
	p    *problems
}

// asObject returns v as an object at path, or reports the problem.
func asObject(p *problems, path string, v any) (*object, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		p.add(path, "must be an object, got %s", kindOf(v))
		return nil, false
	}
	return &object{path: path, m: m, seen: map[string]bool{}, p: p}, true
}

func (o *object) child(key string) string {
	if o.path == "" {
		return key
	}
	return o.path + "." + key
}

// entry returns the value of key if it is present and not null.
func (o *object) entry(key string) (any, bool) {
	o.seen[key] = true
	v, ok := o.m[key]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

func (o *object) str(key string) (string, bool) {
	v, ok := o.entry(key)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	if !isStr {
		o.p.add(o.child(key), "must be a string, got %s", kindOf(v))
		return "", false
	}
	return s, true
}

// nonBlank is str for values that must contain something.
func (o *object) nonBlank(key string) (string, bool) {
	s, ok := o.str(key)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(s) == "" {
		o.p.add(o.child(key), "must not be empty")
		return "", false
	}
	return s, true
}

// enum is str restricted to the given values.
func (o *object) enum(key string, values ...string) (string, bool) {
	s, ok := o.str(key)
	if !ok {
		return "", false
	}
	for _, v := range values {
		if s == v {
			return s, true
		}
	}
	o.p.add(o.child(key), "must be one of %s, got %q", quoteList(values), s)
	return "", false
}

// integer reads a whole number in [lo, hi].
func (o *object) integer(key string, lo, hi int64) (int64, bool) {
	v, ok := o.entry(key)
	if !ok {
		return 0, false
	}
	f, isNum := v.(*big.Float)
	if !isNum {
		o.p.add(o.child(key), "must be a number, got %s", kindOf(v))
		return 0, false
	}
	n, acc := f.Int64()
	if !f.IsInt() || acc != big.Exact {
		o.p.add(o.child(key), "must be a whole number between %d and %d, got %s", lo, hi, f.Text('f', -1))
		return 0, false
	}
	if n < lo || n > hi {
		o.p.add(o.child(key), "must be between %d and %d, got %d", lo, hi, n)
		return 0, false
	}
	return n, true
}

func (o *object) boolean(key string) (bool, bool) {
	v, ok := o.entry(key)
	if !ok {
		return false, false
	}
	b, isBool := v.(bool)
	if !isBool {
		o.p.add(o.child(key), "must be true or false, got %s", kindOf(v))
		return false, false
	}
	return b, true
}

// require reports every key that is absent or null.
func (o *object) require(keys ...string) {
	for _, key := range keys {
		if v, ok := o.m[key]; !ok || v == nil {
			o.p.add(o.child(key), "required")
		}
	}
}

// object reads a nested object.
func (o *object) object(key string) (*object, bool) {
	v, ok := o.entry(key)
	if !ok {
		return nil, false
	}
	return asObject(o.p, o.child(key), v)
}

// entries reads a nested object whose keys are user-chosen names (a map);
// each entry is returned with its path.
func (o *object) entries(key string) (map[string]any, string, bool) {
	v, ok := o.entry(key)
	if !ok {
		return nil, "", false
	}
	m, isMap := v.(map[string]any)
	if !isMap {
		o.p.add(o.child(key), "must be a map, got %s", kindOf(v))
		return nil, "", false
	}
	return m, o.child(key), true
}

// finish reports every key that no getter asked for.
func (o *object) finish() {
	var unknown []string
	for k := range o.m {
		if !o.seen[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return
	}
	sort.Strings(unknown)
	accepted := make([]string, 0, len(o.seen))
	for k := range o.seen {
		accepted = append(accepted, k)
	}
	sort.Strings(accepted)
	for _, k := range unknown {
		o.p.add(o.child(k), "unknown key; accepted: %s", strings.Join(accepted, ", "))
	}
}

// entryPath is the path of a named entry inside a map: parent["name"].
func entryPath(parent, name string) string {
	return parent + "[" + strconv.Quote(name) + "]"
}

func quoteList(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = strconv.Quote(v)
	}
	return strings.Join(q, ", ")
}

// jsonValue converts a decoded value into what encoding/json should see:
// numbers become json.Number in their shortest decimal form, so 2 and 2.0
// both encode as 2 and no float formatting sneaks in.
func jsonValue(v any) any {
	switch t := v.(type) {
	case *big.Float:
		return numberOf(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = jsonValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = jsonValue(e)
		}
		return out
	}
	return v
}

func numberOf(f *big.Float) json.Number {
	return json.Number(f.Text('f', -1))
}
