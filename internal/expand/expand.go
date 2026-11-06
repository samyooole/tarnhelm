// Package expand substitutes $$KEY placeholders in command arguments.
//
// Syntax, scanned left to right:
//
//	$$$$      -> literal "$$" (escape; the following text is not resolved)
//	$$NAME    -> value of NAME, where NAME matches [A-Za-z_][A-Za-z0-9_]*
//	anything else (including a single "$KEY") is copied unchanged
//
// Replacement values are never re-scanned, so expansion is not recursive.
package expand

import (
	"fmt"
	"strings"
)

// MissingError reports a placeholder whose key is not defined.
type MissingError struct{ Key string }

func (e *MissingError) Error() string {
	return fmt.Sprintf("secret %q is not defined in the env file", e.Key)
}

// EmptyError reports a placeholder whose key is defined with an empty value.
type EmptyError struct{ Key string }

func (e *EmptyError) Error() string {
	return fmt.Sprintf("secret %q is empty in the env file", e.Key)
}

// Args returns a copy of args with every placeholder resolved against vars.
// It validates all arguments before returning, and on failure returns an
// error naming the key only (never a value).
func Args(args []string, vars map[string]string) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, err := String(a, vars)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// String expands placeholders in a single argument.
func String(s string, vars map[string]string) (string, error) {
	if !strings.Contains(s, "$$") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], "$$") {
			b.WriteByte(s[i])
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "$$$$") {
			b.WriteString("$$")
			i += 4
			continue
		}
		n := nameLen(s[i+2:])
		if n == 0 {
			// "$$" not followed by a valid name: keep literally.
			b.WriteString("$$")
			i += 2
			continue
		}
		key := s[i+2 : i+2+n]
		val, ok := vars[key]
		if !ok {
			return "", &MissingError{Key: key}
		}
		if val == "" {
			return "", &EmptyError{Key: key}
		}
		b.WriteString(val)
		i += 2 + n
	}
	return b.String(), nil
}

// nameLen returns the length of the identifier at the start of s, or 0.
func nameLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		alpha := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		digit := c >= '0' && c <= '9'
		if !(alpha || (digit && i > 0)) {
			return i
		}
	}
	return len(s)
}
