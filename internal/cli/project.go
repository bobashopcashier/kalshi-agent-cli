package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"kalshi-cli/internal/registry"
	"kalshi-cli/internal/sanitize"
)

const (
	maxFieldsBytes = 4 << 10
	maxFieldsCount = 64
	maxFieldDepth  = 8
)

var fieldSegmentPattern = regexp.MustCompile(`^(?:\$schema|[A-Za-z_][A-Za-z0-9_-]*)$`)

type selectorNode struct {
	leaf     string
	children map[string]*selectorNode
}

type missingProjectionFieldsError struct {
	Fields []string
}

func (e *missingProjectionFieldsError) Error() string {
	return "response did not contain selected field(s): " + strings.Join(e.Fields, ", ")
}

func parseFields(raw string) ([]string, error) {
	if raw == "" {
		return nil, errors.New("--fields must not be empty")
	}
	if len(raw) > maxFieldsBytes {
		return nil, errors.New("--fields exceeds 4 KiB")
	}
	if !utf8.ValidString(raw) || sanitize.ContainsUnsafe(raw) || strings.ContainsRune(raw, utf8.RuneError) {
		return nil, errors.New("--fields contains terminal, control, invalid UTF-8, or bidi characters")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxFieldsCount {
		return nil, fmt.Errorf("--fields accepts at most %d paths", maxFieldsCount)
	}
	seen := map[string]bool{}
	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path == "" {
			return nil, errors.New("--fields contains an empty path")
		}
		segments := strings.Split(path, ".")
		if len(segments) > maxFieldDepth {
			return nil, fmt.Errorf("--fields path %q exceeds %d segments", path, maxFieldDepth)
		}
		for _, segment := range segments {
			if !fieldSegmentPattern.MatchString(segment) {
				return nil, fmt.Errorf("--fields path %q has an invalid segment", path)
			}
		}
		if seen[path] {
			return nil, fmt.Errorf("--fields path %q was repeated", path)
		}
		seen[path] = true
		fields = append(fields, path)
	}
	for i, left := range fields {
		for j, right := range fields {
			if i != j && strings.HasPrefix(right, left+".") {
				return nil, fmt.Errorf("--fields paths %q and %q overlap", left, right)
			}
		}
	}
	return fields, nil
}

func projectData(data map[string]any, fields []string, collection, cursorField string) (map[string]any, error) {
	return projectDataWithPresence(data, fields, collection, cursorField, true, false)
}

func projectResponseData(data map[string]any, fields []string, collection, cursorField string) (map[string]any, error) {
	return projectDataWithPresence(data, fields, collection, cursorField, false, true)
}

func projectDataWithPresence(data map[string]any, fields []string, collection, cursorField string, requirePresence, materializeMissing bool) (map[string]any, error) {
	if len(fields) == 0 {
		return data, nil
	}
	selector := buildSelector(fields)
	if collection == "" {
		matches := map[string]bool{}
		projected := projectValue(data, selector, matches, materializeMissing)
		if requirePresence {
			if err := missingProjectionFields(fields, matches); err != nil {
				return nil, err
			}
		}
		result, ok := projected.(map[string]any)
		if !ok {
			return nil, errors.New("response data is not an object")
		}
		return result, nil
	}

	rawItems, ok := data[collection].([]any)
	if !ok {
		return nil, fmt.Errorf("response collection %q is missing or not an array", collection)
	}
	projectedItems := make([]any, len(rawItems))
	missingSet := map[string]bool{}
	for i, item := range rawItems {
		if _, ok := item.(map[string]any); !ok {
			return nil, fmt.Errorf("response collection %q item %d is not an object", collection, i)
		}
		matches := map[string]bool{}
		projectedItems[i] = projectValue(item, selector, matches, materializeMissing)
		if requirePresence {
			for _, field := range missingProjectionFieldNames(fields, matches) {
				missingSet[field] = true
			}
		}
	}
	if len(missingSet) > 0 {
		missing := make([]string, 0, len(missingSet))
		for field := range missingSet {
			missing = append(missing, field)
		}
		sort.Strings(missing)
		return nil, &missingProjectionFieldsError{Fields: missing}
	}
	result := map[string]any{collection: projectedItems}
	if cursorField != "" {
		if cursor, exists := data[cursorField]; exists {
			result[cursorField] = cursor
		}
	}
	return result, nil
}

func validateResponseProjection(cmd registry.Command, fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(cmd.ResponseSchema.ProjectableFields))
	for _, field := range cmd.ResponseSchema.ProjectableFields {
		allowed[field] = true
	}
	var unknown []string
	for _, field := range fields {
		if !allowed[field] {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("field(s) not projectable for %s: %s; inspect command.response_schema.x-projectable-fields with `kalshi commands describe %s`", cmd.Name, strings.Join(unknown, ", "), cmd.Name)
}

func projectionMap(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeStrictJSON(raw)
	if err != nil {
		return nil, err
	}
	result, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("projection source is not an object")
	}
	return result, nil
}

func buildSelector(fields []string) *selectorNode {
	root := &selectorNode{children: map[string]*selectorNode{}}
	for _, path := range fields {
		node := root
		for _, segment := range strings.Split(path, ".") {
			if node.children == nil {
				node.children = map[string]*selectorNode{}
			}
			child := node.children[segment]
			if child == nil {
				child = &selectorNode{children: map[string]*selectorNode{}}
				node.children[segment] = child
			}
			node = child
		}
		node.leaf = path
	}
	return root
}

func projectValue(value any, selector *selectorNode, matches map[string]bool, materializeMissing bool) any {
	if selector.leaf != "" {
		matches[selector.leaf] = true
		return value
	}
	switch source := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for name, child := range selector.children {
			if selected, ok := source[name]; ok {
				out[name] = projectValue(selected, child, matches, materializeMissing)
			} else if materializeMissing {
				out[name] = nil
			}
		}
		return out
	case []any:
		if len(source) == 0 {
			markSelectorLeaves(selector, matches)
		}
		out := make([]any, len(source))
		for i, item := range source {
			out[i] = projectValue(item, selector, matches, materializeMissing)
		}
		return out
	default:
		return nil
	}
}

func markSelectorLeaves(selector *selectorNode, matches map[string]bool) {
	if selector.leaf != "" {
		matches[selector.leaf] = true
	}
	for _, child := range selector.children {
		markSelectorLeaves(child, matches)
	}
}

func missingProjectionFields(fields []string, matches map[string]bool) error {
	missing := missingProjectionFieldNames(fields, matches)
	if len(missing) == 0 {
		return nil
	}
	return &missingProjectionFieldsError{Fields: missing}
}

func missingProjectionFieldNames(fields []string, matches map[string]bool) []string {
	var missing []string
	for _, field := range fields {
		if !matches[field] {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}
