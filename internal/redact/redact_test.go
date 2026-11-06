package redact

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	r := New([]string{"glpat-example123", "sk-example456", "abcdef", "defxyz", "", "aa"})
	cases := []struct{ in, want string }{
		{"Authentication failed for glpat-example123", "Authentication failed for [REDACTED]"},
		{"API_KEY=sk-example456", "API_KEY=[REDACTED]"},
		{"sk-example456 sk-example456\nsk-example456", "[REDACTED] [REDACTED]\n[REDACTED]"},
		{"a=glpat-example123,b=sk-example456.", "a=[REDACTED],b=[REDACTED]."},
		{"(sk-example456)", "([REDACTED])"},
		{"xxsk-example456yy", "xx[REDACTED]yy"},
		// Overlapping secrets: no fragment of either survives.
		{"abcdefxyz", "[REDACTED]"},
		{"zzabcdefxyzzz", "zz[REDACTED]zz"},
		// Self-overlapping secret.
		{"aaa", "[REDACTED]"},
		{"hello world", "hello world"},
		{"", ""},
	}
	for _, c := range cases {
		if got := r.String(c.in); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestLongerSecretWins(t *testing.T) {
	r := New([]string{"secret", "secret-long"})
	if got := r.String("x secret-long y secret z"); got != "x [REDACTED] y [REDACTED] z" {
		t.Errorf("got %q", got)
	}
}

func TestNoSecrets(t *testing.T) {
	in := []byte("unchanged")
	if got := New(nil).Bytes(in); string(got) != "unchanged" {
		t.Errorf("got %q", got)
	}
}

func TestLarge(t *testing.T) {
	r := New([]string{"sk-example456"})
	in := strings.Repeat("noise ", 100000) + "sk-example456" + strings.Repeat(" noise", 100000)
	out := r.String(in)
	if strings.Contains(out, "sk-example456") || !strings.Contains(out, Placeholder) {
		t.Error("secret not redacted in large input")
	}
}
