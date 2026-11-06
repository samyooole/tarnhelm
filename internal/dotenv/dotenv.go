// Package dotenv loads a dotenv file for a single tarnhelm invocation.
//
// Errors returned from this package never contain file contents: the
// underlying parser embeds raw source text in its messages, so they are
// mapped to fixed, safe descriptions.
package dotenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// Load reads and parses the dotenv file at path.
func Load(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("env file not found: %s", path)
		case errors.Is(err, fs.ErrPermission):
			return nil, fmt.Errorf("env file not readable: %s", path)
		default:
			return nil, fmt.Errorf("cannot read env file: %s", path)
		}
	}
	vars, err := godotenv.UnmarshalBytes(data)
	if err != nil {
		return nil, fmt.Errorf("invalid dotenv syntax in %s: %s", path, safeReason(err))
	}
	return vars, nil
}

// safeReason converts a parser error into a description that contains no
// file content.
func safeReason(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unterminated quoted value"):
		return "unterminated quoted value"
	case strings.HasPrefix(msg, "unexpected character"):
		return "unexpected character in variable name"
	case strings.HasPrefix(msg, "zero length string"):
		return "missing value assignment"
	default:
		return "malformed line"
	}
}

// SecretValues returns every non-empty value in vars, de-duplicated and
// sorted. All of these are treated as secrets for redaction.
func SecretValues(vars map[string]string) []string {
	seen := make(map[string]bool, len(vars))
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
