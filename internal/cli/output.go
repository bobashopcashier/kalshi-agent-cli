package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"kalshi-agent-cli/internal/contract"
	"kalshi-agent-cli/internal/sanitize"
)

func render(w io.Writer, env *contract.Envelope, opts options, collection string) error {
	if env.Data != nil {
		clean, count := sanitize.Value(env.Data)
		env.Data = clean
		env.Meta.SanitizedCount += count
	}
	if env.Error != nil {
		env.Error.Code, _ = sanitize.String(env.Error.Code)
		env.Error.Message, _ = sanitize.String(env.Error.Message)
		if env.Error.Details != nil {
			clean, count := sanitize.Value(env.Error.Details)
			env.Error.Details = clean
			env.Meta.SanitizedCount += count
		}
	}
	var payload []byte
	var err error
	if opts.Format == "ndjson" {
		payload, err = boundedNDJSON(env, collection, opts.MaxBytes)
	} else {
		payload, err = boundedJSON(env, collection, opts.Pretty, opts.MaxBytes)
	}
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func boundedJSON(env *contract.Envelope, collection string, pretty bool, maxBytes int) ([]byte, error) {
	encode := func() ([]byte, error) {
		var raw []byte
		var err error
		if pretty {
			raw, err = json.MarshalIndent(env, "", "  ")
		} else {
			raw, err = json.Marshal(env)
		}
		if err == nil {
			raw = append(raw, '\n')
		}
		return raw, err
	}
	raw, err := encode()
	if err != nil || len(raw) <= maxBytes {
		return raw, err
	}
	items, ok := collectionItems(env.Data, collection)
	addReason(&env.Meta.Truncation, "max_bytes")
	if ok {
		low, high := 0, len(items)
		for low < high {
			mid := (low + high + 1) / 2
			setCollection(env.Data, collection, items[:mid])
			if env.Meta.Pagination != nil {
				env.Meta.Pagination.ItemsReturned = mid
			}
			candidate, err := encode()
			if err == nil && len(candidate) <= maxBytes {
				low = mid
			} else {
				high = mid - 1
			}
		}
		setCollection(env.Data, collection, items[:low])
		if env.Meta.Pagination != nil {
			env.Meta.Pagination.ItemsReturned = low
		}
		raw, err = encode()
	}
	if !ok || len(raw) > maxBytes {
		env.Data = map[string]any{"omitted": true, "reason": "max_bytes"}
		raw, err = encode()
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, errors.New("max-bytes is too small for the stable envelope")
	}
	return raw, nil
}

func boundedNDJSON(env *contract.Envelope, collection string, maxBytes int) ([]byte, error) {
	items, ok := collectionItems(env.Data, collection)
	if !ok {
		copyEnv := *env
		copyEnv.RecordType = "result"
		raw, err := json.Marshal(copyEnv)
		if err != nil {
			return nil, err
		}
		raw = append(raw, '\n')
		if len(raw) <= maxBytes {
			return raw, nil
		}
		addReason(&env.Meta.Truncation, "max_bytes")
		env.Data = map[string]any{"omitted": true, "reason": "max_bytes"}
		copyEnv = *env
		copyEnv.RecordType = "result"
		raw, err = json.Marshal(copyEnv)
		if err != nil || len(raw)+1 > maxBytes {
			return nil, errors.New("max-bytes is too small for the stable envelope")
		}
		return append(raw, '\n'), nil
	}
	encode := func(count int) ([]byte, error) {
		var out bytes.Buffer
		for i := 0; i < count; i++ {
			seq := i
			itemEnv := *env
			itemEnv.RecordType = "item"
			itemEnv.Data = items[i]
			itemEnv.Meta.Sequence = &seq
			itemEnv.Meta.Pagination = nil
			raw, err := json.Marshal(itemEnv)
			if err != nil {
				return nil, err
			}
			out.Write(raw)
			out.WriteByte('\n')
		}
		summary := *env
		summary.RecordType = "summary"
		summary.Data = map[string]any{"collection_field": collection}
		summary.Meta.Sequence = nil
		if summary.Meta.Pagination != nil {
			copyPage := *summary.Meta.Pagination
			copyPage.ItemsReturned = count
			summary.Meta.Pagination = &copyPage
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return nil, err
		}
		out.Write(raw)
		out.WriteByte('\n')
		return out.Bytes(), nil
	}
	raw, err := encode(len(items))
	if err == nil && len(raw) <= maxBytes {
		return raw, nil
	}
	addReason(&env.Meta.Truncation, "max_bytes")
	low, high := 0, len(items)
	for low < high {
		mid := (low + high + 1) / 2
		candidate, candidateErr := encode(mid)
		if candidateErr == nil && len(candidate) <= maxBytes {
			low = mid
		} else {
			high = mid - 1
		}
	}
	raw, err = encode(low)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, errors.New("max-bytes is too small for the NDJSON summary")
	}
	return raw, nil
}

func collectionItems(data any, collection string) ([]any, bool) {
	if collection == "" {
		return nil, false
	}
	obj, ok := data.(map[string]any)
	if !ok {
		return nil, false
	}
	items, ok := obj[collection].([]any)
	return items, ok
}

func setCollection(data any, collection string, items []any) {
	if obj, ok := data.(map[string]any); ok {
		obj[collection] = items
	}
}

func addReason(truncation *contract.Truncation, reason string) {
	for _, existing := range truncation.Reasons {
		if existing == reason {
			truncation.Truncated = true
			return
		}
	}
	truncation.Truncated = true
	truncation.Reasons = append(truncation.Reasons, reason)
}
