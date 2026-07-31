package sanitize

import (
	"strings"
	"testing"
)

func TestStringEscapesControlANSIAndBidi(t *testing.T) {
	input := "safe\x1b[31m\n" + string(rune(0x202e)) + "txt"
	got, count := String(input)
	if count != 3 {
		t.Fatalf("count=%d, want 3; output=%q", count, got)
	}
	for _, forbidden := range []string{"\x1b", "\n", string(rune(0x202e))} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("output retained unsafe content %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, `\u001B`) || !strings.Contains(got, `\u202E`) {
		t.Fatalf("missing visible escapes: %q", got)
	}
}

func TestValueRecurses(t *testing.T) {
	clean, count := Value(map[string]any{"x": []any{"a\x00b"}})
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
	got := clean.(map[string]any)["x"].([]any)[0]
	if got != `a\u0000b` {
		t.Fatalf("got %q", got)
	}
}
