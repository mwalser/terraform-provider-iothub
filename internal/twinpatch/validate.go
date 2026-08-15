package twinpatch

import (
	"fmt"
	"strings"
	"unicode"
)

// Limits the service enforces on twin tags and desired properties, verified
// live (CONCEPT.md Appendix D). Section-level caps (tags 8 KB, desired 32 KB)
// apply to the whole twin including keys Terraform does not own and are
// therefore left to the service.
const (
	MaxKeyBytes    = 1024
	MaxValueBytes  = 4096
	MaxObjectDepth = 10 // objects may nest 10 deep; a scalar may sit inside the 10th
)

// Problem describes one validation failure.
type Problem struct {
	Path    Path
	Message string
}

func (p Problem) String() string {
	if len(p.Path) == 0 {
		return p.Message
	}
	return fmt.Sprintf("%s: %s", p.Path, p.Message)
}

// Validate checks a document against the service's rules for what Terraform
// sends: key charset and length, string value length, object nesting depth
// and the absence of null values (a null in a merge patch deletes a key; to
// stop managing a key, omit it).
func Validate(doc map[string]any) []Problem {
	var out []Problem
	var walk func(p Path, v any)
	walk = func(p Path, v any) {
		switch t := v.(type) {
		case nil:
			out = append(out, Problem{p, "null is not a twin value; omit the key instead (a null in a merge patch deletes the key)"})
		case string:
			if len(t) > MaxValueBytes {
				out = append(out, Problem{p, fmt.Sprintf("string value is %d bytes, the maximum is %d", len(t), MaxValueBytes)})
			}
		case []any:
			for i, e := range t {
				if e == nil {
					out = append(out, Problem{p, fmt.Sprintf("array element %d is null; the service rejects null values", i)})
					continue
				}
				walk(p, e) // arrays are leaves: elements are checked at the array's path
			}
		case map[string]any:
			if len(p) > MaxObjectDepth {
				out = append(out, Problem{p, fmt.Sprintf("objects may nest at most %d levels deep", MaxObjectDepth)})
				return
			}
			for k, c := range t {
				if msg := checkKey(k); msg != "" {
					out = append(out, Problem{p.child(k), msg})
				}
				walk(p.child(k), c)
			}
		}
	}
	for k, v := range doc {
		if msg := checkKey(k); msg != "" {
			out = append(out, Problem{Path{k}, msg})
		}
		walk(Path{k}, v)
	}
	return out
}

func checkKey(k string) string {
	if len(k) > MaxKeyBytes {
		return fmt.Sprintf("key is %d bytes, the maximum is %d", len(k), MaxKeyBytes)
	}
	if strings.ContainsAny(k, ".$# ") {
		return "key must not contain '.', '$', '#' or ' '"
	}
	for _, r := range k {
		if unicode.IsControl(r) {
			return "key must not contain control characters"
		}
	}
	return ""
}
