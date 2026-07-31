package cli

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseFields(t *testing.T) {
	fields, err := parseFields("ticker, title,price.yes")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields, []string{"ticker", "title", "price.yes"}) {
		t.Fatalf("fields=%#v", fields)
	}
	for _, tc := range []string{"", "ticker,", "ticker,ticker", "ticker,ticker.value", "bad$name", "price..yes", "bad\nname"} {
		if _, err := parseFields(tc); err == nil {
			t.Errorf("parseFields(%q) succeeded", tc)
		}
	}
	if _, err := parseFields(strings.Repeat("field,", maxFieldsCount) + "last"); err == nil {
		t.Error("too many fields succeeded")
	}
	if _, err := parseFields("command.response_schema.$schema,command.response_schema.x-projectable-fields"); err != nil {
		t.Fatalf("JSON Schema extension fields should be selectable: %v", err)
	}
	if _, err := parseFields(strings.Repeat("a", maxFieldsBytes+1)); err == nil {
		t.Error("oversized selector succeeded")
	}
	if _, err := parseFields("a.b.c.d.e.f.g.h.i"); err == nil {
		t.Error("over-depth selector succeeded")
	}
	for _, unsafe := range []string{"ticker," + string(rune(0x202e)) + "title", "ticker," + string(utf8.RuneError)} {
		if _, err := parseFields(unsafe); err == nil {
			t.Errorf("unsafe selector %q succeeded", unsafe)
		}
	}
}

func TestProjectCollectionPreservesCursor(t *testing.T) {
	data := map[string]any{
		"markets": []any{
			map[string]any{"ticker": "A", "title": "Alpha", "rules": "large"},
			map[string]any{"ticker": "B", "title": "Beta", "rules": "large"},
		},
		"cursor":  "next",
		"sidecar": strings.Repeat("omit", 20),
	}
	projected, err := projectData(data, []string{"ticker", "title"}, "markets", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"markets": []any{
			map[string]any{"ticker": "A", "title": "Alpha"},
			map[string]any{"ticker": "B", "title": "Beta"},
		},
		"cursor": "next",
	}
	if !reflect.DeepEqual(projected, want) {
		t.Fatalf("projected=%#v", projected)
	}
}

func TestProjectNestedRootAndArray(t *testing.T) {
	data := map[string]any{
		"registry_version": "v1",
		"commands": []any{
			map[string]any{"name": "a", "summary": "A", "schema": "omit"},
			map[string]any{"name": "b", "summary": "B", "schema": "omit"},
		},
		"global_options": map[string]any{"omit": true},
	}
	projected, err := projectData(data, []string{"registry_version", "commands.name", "commands.summary"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projected["global_options"]; ok {
		t.Fatalf("global options leaked: %#v", projected)
	}
	commands := projected["commands"].([]any)
	if !reflect.DeepEqual(commands[0], map[string]any{"name": "a", "summary": "A"}) {
		t.Fatalf("commands=%#v", commands)
	}
}

func TestProjectionMissingFieldFailsUnlessCollectionEmpty(t *testing.T) {
	data := map[string]any{"markets": []any{map[string]any{"ticker": "A"}}, "cursor": ""}
	if _, err := projectData(data, []string{"unknown"}, "markets", "cursor"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err=%v", err)
	}
	empty := map[string]any{"markets": []any{}, "cursor": ""}
	if _, err := projectData(empty, []string{"unknown"}, "markets", "cursor"); err != nil {
		t.Fatalf("empty projection failed: %v", err)
	}
}

func TestProjectionRejectsNonObjectCollectionItems(t *testing.T) {
	data := map[string]any{"markets": []any{"bad"}, "cursor": "next"}
	if _, err := projectData(data, []string{"ticker"}, "markets", "cursor"); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("err=%v", err)
	}
}
