package dotenv

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := write(t, `# comment
PLAIN=sk-plain
SINGLE='single quoted'
DOUBLE="double quoted"
SPACES=has some spaces
export EXPORTED=exp-value
EMPTY=
INLINE=val # trailing comment
`)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN":    "sk-plain",
		"SINGLE":   "single quoted",
		"DOUBLE":   "double quoted",
		"SPACES":   "has some spaces",
		"EXPORTED": "exp-value",
		"EMPTY":    "",
		"INLINE":   "val",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.env"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	got, err := Load(write(t, ""))
	if err != nil {
		t.Fatalf("empty file should load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no vars, got %v", got)
	}
}

func TestLoadMalformedDoesNotLeak(t *testing.T) {
	cases := map[string]string{
		"unterminated": "GOOD=x\nBAD=\"sk-leak-unterminated\n",
		"bad name":     "BAD KEY sk-leak-badname=1\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, content))
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), "sk-leak") {
				t.Errorf("error leaks file content: %v", err)
			}
			if !strings.Contains(err.Error(), "invalid dotenv syntax") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSecretValues(t *testing.T) {
	got := SecretValues(map[string]string{"A": "x", "B": "", "C": "y", "D": "x"})
	if want := []string{"x", "y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
