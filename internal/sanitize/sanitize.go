package sanitize

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func String(s string) (string, int) {
	var b strings.Builder
	count := 0
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError {
			b.WriteString(`\uFFFD`)
			count++
			s = s[1:]
			continue
		}
		if unsafeRune(r) {
			if r <= 0xffff {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				fmt.Fprintf(&b, `\U%08X`, r)
			}
			count++
		} else {
			b.WriteRune(r)
		}
		s = s[n:]
	}
	return b.String(), count
}

func unsafeRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
		r == 0x061c || r == 0x200e || r == 0x200f || r == 0xfeff
}

func Value(v any) (any, int) {
	switch x := v.(type) {
	case string:
		return String(x)
	case []any:
		out := make([]any, len(x))
		total := 0
		for i, item := range x {
			clean, n := Value(item)
			out[i] = clean
			total += n
		}
		return out, total
	case map[string]any:
		out := make(map[string]any, len(x))
		total := 0
		for k, item := range x {
			sk, n := String(k)
			total += n
			clean, n := Value(item)
			out[sk] = clean
			total += n
		}
		return out, total
	default:
		return v, 0
	}
}

func ContainsUnsafe(s string) bool {
	for _, r := range s {
		if unsafeRune(r) {
			return true
		}
	}
	return !utf8.ValidString(s)
}
