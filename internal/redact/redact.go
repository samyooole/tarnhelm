// Package redact replaces exact occurrences of known secret values.
package redact

import (
	"bytes"
	"sort"
)

// Placeholder is the text substituted for every redacted region.
const Placeholder = "[REDACTED]"

// Redactor replaces occurrences of a fixed set of secrets.
type Redactor struct {
	secrets [][]byte
}

// New returns a Redactor for the given secrets. Empty strings are ignored.
func New(secrets []string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if s != "" {
			r.secrets = append(r.secrets, []byte(s))
		}
	}
	// Longest first; not required for correctness (regions are merged) but
	// keeps behaviour obvious.
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

type span struct{ start, end int }

// Bytes returns a redacted copy of b.
//
// Every occurrence of every secret is located first, overlapping or adjacent
// regions are merged, and each merged region is replaced by a single
// Placeholder. This guarantees no byte belonging to any secret occurrence
// survives, even when secrets overlap one another.
func (r *Redactor) Bytes(b []byte) []byte {
	var spans []span
	for _, s := range r.secrets {
		for off := 0; ; {
			i := bytes.Index(b[off:], s)
			if i < 0 {
				break
			}
			spans = append(spans, span{off + i, off + i + len(s)})
			off += i + 1 // allow self-overlapping matches
		}
	}
	if len(spans) == 0 {
		return append([]byte(nil), b...)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	out := make([]byte, 0, len(b))
	pos := 0
	cur := spans[0]
	flush := func(sp span) {
		out = append(out, b[pos:sp.start]...)
		out = append(out, Placeholder...)
		pos = sp.end
	}
	for _, sp := range spans[1:] {
		if sp.start <= cur.end {
			if sp.end > cur.end {
				cur.end = sp.end
			}
			continue
		}
		flush(cur)
		cur = sp
	}
	flush(cur)
	return append(out, b[pos:]...)
}

// String returns a redacted copy of s.
func (r *Redactor) String(s string) string {
	return string(r.Bytes([]byte(s)))
}
