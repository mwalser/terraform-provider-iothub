// Package twinpatch is the pure-Go engine behind the twin resources: it
// implements the leaf-path ownership model of CONCEPT.md §6.3 on top of the
// JSON merge-patch semantics of PATCH /twins/{id} (RFC 7386, verified live:
// a null removes a key and keeps its siblings, removing the last key leaves
// {}, arrays are replaced whole).
//
// Documents are JSON objects decoded with json.Decoder.UseNumber, so numbers
// are json.Number and compare by value ("1", "1.0" and "1e0" are equal).
//
// Vocabulary:
//   - a leaf is a value that is not a non-empty object: scalars, arrays and
//     the empty object {} (which stands for "this key is an object" without
//     owning anything inside it);
//   - the owned set of a document is the set of its leaf paths;
//   - Project reads the owned leaves out of a remote document, Diff renders
//     the merge patch that moves the owned leaves from one document to
//     another, nulling removed leaves at the highest ancestor nobody else
//     uses.
package twinpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Path addresses a value inside a document. Twin keys can never contain "."
// (the service rejects it), so the dotted form is unambiguous.
type Path []string

// String renders the dotted form.
func (p Path) String() string { return strings.Join(p, ".") }

// HasPrefix reports whether q is p itself or an ancestor of p.
func (p Path) HasPrefix(q Path) bool {
	if len(q) > len(p) {
		return false
	}
	for i := range q {
		if p[i] != q[i] {
			return false
		}
	}
	return true
}

func (p Path) child(key string) Path {
	c := make(Path, len(p)+1)
	copy(c, p)
	c[len(p)] = key
	return c
}

// Leaf is an owned leaf: its path and the value the owner declared.
type Leaf struct {
	Path  Path
	Value any
}

// Decode parses a JSON object. Numbers stay json.Number.
func Decode(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid JSON: trailing data after the object")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, got %s", kind(v))
	}
	return obj, nil
}

// Encode renders a document compactly with sorted keys.
func Encode(doc map[string]any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if doc == nil {
		doc = map[string]any{}
	}
	_ = enc.Encode(doc) // maps of JSON values cannot fail to encode
	return strings.TrimSuffix(buf.String(), "\n")
}

// Leaves returns the leaves of doc keyed by their dotted path. A nil or
// empty doc has no leaves.
func Leaves(doc map[string]any) map[string]Leaf {
	out := map[string]Leaf{}
	var walk func(p Path, v any)
	walk = func(p Path, v any) {
		obj, ok := v.(map[string]any)
		if !ok || len(obj) == 0 { // leaf
			out[p.String()] = Leaf{Path: p, Value: v}
			return
		}
		for k, c := range obj {
			walk(p.child(k), c)
		}
	}
	for k, v := range doc {
		walk(Path{k}, v)
	}
	return out
}

// Get returns the value at p in doc.
func Get(doc map[string]any, p Path) (any, bool) {
	var cur any = doc
	for _, k := range p {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Set writes v at p in doc, creating (or replacing non-object) ancestors.
func Set(doc map[string]any, p Path, v any) {
	cur := doc
	for _, k := range p[:len(p)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[p[len(p)-1]] = v
}

// Project returns the part of remote that the owned leaves cover: for every
// owned leaf that exists remotely, the remote value at its path (deep
// copied); owned leaves missing remotely are absent from the projection. A
// leaf declared as {} projects to {} whenever the remote value is an object,
// whatever it contains — the owner asserted "an object lives here", nothing
// about its content.
func Project(remote map[string]any, owned map[string]Leaf) map[string]any {
	out := map[string]any{}
	for _, leaf := range owned {
		v, ok := Get(remote, leaf.Path)
		if !ok {
			continue
		}
		if _, isObj := v.(map[string]any); isObj && isEmptyObject(leaf.Value) {
			v = map[string]any{}
		}
		Set(out, leaf.Path, Clone(v))
	}
	return out
}

// Diff renders the merge patch that changes the owned leaves from prev to
// next; remote is the current remote document (needed to null removed leaves
// at the right level). It returns nil when there is nothing to send.
//
//   - leaves of next that prev lacks or holds with a different value are set,
//     unless remote already holds that value;
//   - leaves of prev that next lacks are removed: for each, the highest
//     ancestor (or the leaf itself) that exists remotely and whose remote
//     subtree consists only of leaves being removed — and under which next
//     declares nothing — is nulled; a leaf that already vanished remotely,
//     or whose remote subtree has foreign content, is left alone. This is what
//     keeps a null from touching siblings written by others, and what makes
//     removing `firmware = {…}` delete the whole `firmware` object when
//     Terraform was its only writer. A removed leaf below a next leaf that is
//     a scalar or array needs no null: setting that value replaces the
//     subtree. (Below a next leaf declared as {} it does: {} merges without
//     touching content.)
func Diff(prev, next, remote map[string]any) map[string]any {
	prevLeaves, nextLeaves, remoteLeaves := Leaves(prev), Leaves(next), Leaves(remote)
	patch := map[string]any{}

	// set added / changed leaves
	for key, leaf := range nextLeaves {
		if old, ok := prevLeaves[key]; ok && Equal(old.Value, leaf.Value) {
			continue
		}
		if cur, ok := Get(remote, leaf.Path); ok && Equal(cur, leaf.Value) {
			continue // already there (adoption after import, or a concurrent identical write)
		}
		Set(patch, leaf.Path, Clone(leaf.Value))
	}

	// removed leaves
	removed := map[string]bool{}
	for key := range prevLeaves {
		if _, ok := nextLeaves[key]; !ok {
			removed[key] = true
		}
	}
	underNext := func(a Path) bool { // a next leaf lies at or below a: a cannot be nulled
		for _, l := range nextLeaves {
			if l.Path.HasPrefix(a) {
				return true
			}
		}
		return false
	}
	replacedByNext := func(a Path) bool { // a next scalar/array strictly above a replaces the whole subtree
		for _, l := range nextLeaves {
			if len(l.Path) < len(a) && a.HasPrefix(l.Path) && !isEmptyObject(l.Value) {
				return true
			}
		}
		return false
	}
	nulled := map[string]bool{}
	for key := range removed {
		leaf := prevLeaves[key]
		if replacedByNext(leaf.Path) {
			continue
		}
		for depth := 1; depth <= len(leaf.Path); depth++ {
			anc := leaf.Path[:depth]
			if _, exists := Get(remote, anc); !exists {
				break // nothing remote from here down; a null would only create empty ancestors
			}
			if allRemovedUnder(anc, remoteLeaves, removed) && !underNext(anc) {
				if k := anc.String(); !nulled[k] {
					nulled[k] = true
					Set(patch, anc, nil)
				}
				break
			}
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

// allRemovedUnder reports whether every remote leaf at or below anc is one
// of the leaves being removed.
func allRemovedUnder(anc Path, remoteLeaves map[string]Leaf, removed map[string]bool) bool {
	for key, l := range remoteLeaves {
		if l.Path.HasPrefix(anc) && !removed[key] {
			return false
		}
	}
	return true
}

// Equal compares two JSON values structurally, numbers by value.
func Equal(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, ok := bv[k]
			if !ok || !Equal(x, y) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !Equal(av[i], bv[i]) {
				return false
			}
		}
		return true
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		if av == bv {
			return true
		}
		x, okx := new(big.Rat).SetString(string(av))
		y, oky := new(big.Rat).SetString(string(bv))
		return okx && oky && x.Cmp(y) == 0
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}

// Clone deep-copies a JSON value.
func Clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, c := range t {
			out[k] = Clone(c)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, c := range t {
			out[i] = Clone(c)
		}
		return out
	default:
		return v
	}
}

// StripSystem returns doc without the service's top-level `$metadata` /
// `$version` keys (twin desired/reported sections carry them).
func StripSystem(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		if strings.HasPrefix(k, "$") {
			continue
		}
		out[k] = v
	}
	return out
}

func isEmptyObject(v any) bool {
	obj, ok := v.(map[string]any)
	return ok && len(obj) == 0
}

func kind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// SortedPaths returns the dotted paths of a leaf set in a stable order (for
// messages and tests).
func SortedPaths(leaves map[string]Leaf) []string {
	out := make([]string, 0, len(leaves))
	for k := range leaves {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
