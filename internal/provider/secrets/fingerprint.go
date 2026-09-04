package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters — OWASP's minimum configuration (19 MiB, two
// passes, one lane). A refresh, a write and a plan each hash once.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	hashLen      = 32
	saltLen      = 16
)

// Fingerprint identifies the values behind a document's placeholders
// without holding them (CONCEPT.md §6.7): one slow salted hash over the
// (path, value) pairs the hub holds at the placeholder paths, under a random
// salt kept for the life of the resource. Rendered as one opaque string, it
// lives in the deployment's state: a refresh or write takes it from the
// hub's document, a plan derives it from the configured values and
// compares, so a rotated secret and a deployment recreated outside
// Terraform with another value are both seen without reading the hub at
// plan time. The paths go into the hash, so a value moved to another path
// does not look the same, and equal hubs give equal strings.
type Fingerprint struct {
	salt, hash []byte
}

// ParseFingerprint reads a state value; "" or an undecodable string is the
// empty fingerprint, which matches nothing.
func ParseFingerprint(s string) Fingerprint {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != saltLen+hashLen {
		return Fingerprint{}
	}
	return Fingerprint{salt: b[:saltLen], hash: b[saltLen:]}
}

// String renders the fingerprint for state; "" when empty.
func (f Fingerprint) String() string {
	if len(f.hash) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(slices.Concat(f.salt, f.hash))
}

// Take returns the fingerprint of the strings hub holds at the paths of
// refs, under f's salt — a fresh one when f has none. A path without a
// string on the hub has no part in it; no strings at all give the empty
// fingerprint.
func (f Fingerprint) Take(refs []Ref, hub map[string]any) Fingerprint {
	pairs := map[string]string{}
	for _, r := range refs {
		if s, ok := r.segs.stringAt(hub); ok {
			pairs[r.segs.key()] = s
		}
	}
	if len(pairs) == 0 {
		return Fingerprint{}
	}
	if len(f.salt) == 0 {
		f.salt = make([]byte, saltLen)
		rand.Read(f.salt) // cannot fail since Go 1.24
	}
	return Fingerprint{salt: f.salt, hash: f.hashOf(pairs)}
}

// Matches reports whether the values configured for refs are what f was
// taken from. The empty fingerprint matches nothing.
func (f Fingerprint) Matches(refs []Ref, values map[string]string) bool {
	if len(f.hash) == 0 {
		return false
	}
	pairs := make(map[string]string, len(refs))
	for _, r := range refs {
		pairs[r.segs.key()] = values[r.Name]
	}
	return bytes.Equal(f.hash, f.hashOf(pairs))
}

// hashOf hashes the pairs in canonical form: [key, value] in key order.
func (f Fingerprint) hashOf(pairs map[string]string) []byte {
	canonical := make([][2]string, 0, len(pairs))
	for _, k := range slices.Sorted(maps.Keys(pairs)) {
		canonical = append(canonical, [2]string{k, pairs[k]})
	}
	b, _ := json.Marshal(canonical) // strings cannot fail to encode
	return argon2.IDKey(b, f.salt, argonTime, argonMemory, argonThreads, hashLen)
}
