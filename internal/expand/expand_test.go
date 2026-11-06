package expand

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var vars = map[string]string{
	"API_KEY":  "sk-123",
	"PASSWORD": "p@ss word",
	"RECURSE":  "$$API_KEY",
	"EMPTY":    "",
	"lower":    "lc",
}

func TestString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"$$API_KEY", "sk-123"},
		{"Bearer $$API_KEY", "Bearer sk-123"},
		{"postgres://u:$$PASSWORD@localhost:5432/db", "postgres://u:p@ss word@localhost:5432/db"},
		{"$$API_KEY:$$PASSWORD", "sk-123:p@ss word"},
		{"$$API_KEY$$API_KEY", "sk-123sk-123"},
		{"$$RECURSE", "$$API_KEY"}, // not recursive
		{"$$lower", "lc"},
		{"$$$$API_KEY", "$$API_KEY"}, // escape
		{"$$$$$$API_KEY", "$$sk-123"},
		{"$$$API_KEY", "$$$API_KEY"}, // "$$" + "$API_KEY": no valid name after "$$"
		{"$API_KEY", "$API_KEY"},
		{"${API_KEY}", "${API_KEY}"},
		{"$$", "$$"},
		{"$$1ABC", "$$1ABC"},
		{"cost $$ 5", "cost $$ 5"},
		{"plain", "plain"},
		{"", ""},
	}
	for _, c := range cases {
		got, err := String(c.in, vars)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestArgsPreservesBoundaries(t *testing.T) {
	got, err := Args([]string{"-p", "$$PASSWORD", "x y"}, vars)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-p", "p@ss word", "x y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMissing(t *testing.T) {
	_, err := Args([]string{"ok", "$$API_KEY", "x$$NOPE"}, vars)
	var me *MissingError
	if !errors.As(err, &me) || me.Key != "NOPE" {
		t.Fatalf("want MissingError NOPE, got %v", err)
	}
	// Case-sensitive.
	if _, err := String("$$api_key", vars); !errors.As(err, &me) {
		t.Errorf("want MissingError for wrong case, got %v", err)
	}
}

func TestEmpty(t *testing.T) {
	_, err := String("$$EMPTY", vars)
	var ee *EmptyError
	if !errors.As(err, &ee) || ee.Key != "EMPTY" {
		t.Fatalf("want EmptyError, got %v", err)
	}
}

func TestErrorsDoNotContainValues(t *testing.T) {
	_, err := Args([]string{"$$API_KEY", "$$NOPE"}, vars)
	if err == nil || strings.Contains(err.Error(), "sk-123") {
		t.Fatalf("bad error: %v", err)
	}
}
