package registry

import "sort"

type Effect struct {
	Class                string `json:"class"`
	Network              bool   `json:"network"`
	Mutation             bool   `json:"mutation"`
	AuthRequired         bool   `json:"auth_required"`
	AuthOptional         bool   `json:"auth_optional,omitempty"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	Idempotency          string `json:"idempotency,omitempty"`
	Reconciliation       string `json:"reconciliation,omitempty"`
}

type Field struct {
	Type        string   `json:"type"`
	Location    string   `json:"x-location"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Minimum     *int64   `json:"minimum,omitempty"`
	Maximum     *int64   `json:"maximum,omitempty"`
	MinLength   int      `json:"minLength,omitempty"`
	MaxLength   int      `json:"maxLength,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type Schema struct {
	Dialect              string           `json:"$schema"`
	Type                 string           `json:"type"`
	AdditionalProperties bool             `json:"additionalProperties"`
	Properties           map[string]Field `json:"properties"`
	Required             []string         `json:"required,omitempty"`
}

type ResponseField struct {
	Type        string `json:"type"`
	Format      string `json:"format,omitempty"`
	Description string `json:"description,omitempty"`
}

type ResponseSchema struct {
	Dialect              string                   `json:"$schema"`
	Type                 string                   `json:"type"`
	AdditionalProperties bool                     `json:"additionalProperties"`
	Properties           map[string]ResponseField `json:"properties"`
	Required             []string                 `json:"required,omitempty"`
	CollectionField      string                   `json:"x-collection-field,omitempty"`
	CursorField          string                   `json:"x-cursor-field,omitempty"`
	ProjectableFields    []string                 `json:"x-projectable-fields,omitempty"`
	RequiredFields       []string                 `json:"x-required-fields,omitempty"`
	RequiredFieldTypes   map[string]string        `json:"x-required-field-types,omitempty"`
	ProjectedContracts   map[string]ResponseField `json:"x-projected-field-contracts"`
	CursorAliases        []string                 `json:"x-cursor-aliases"`
}

type Command struct {
	SchemaVersion         string         `json:"schema_version"`
	OutputContractVersion string         `json:"output_contract_version"`
	Name                  string         `json:"name"`
	CLIPath               []string       `json:"cli_path"`
	Summary               string         `json:"summary"`
	Method                string         `json:"method"`
	Path                  string         `json:"path"`
	Effect                Effect         `json:"effect"`
	ParamsSchema          Schema         `json:"params_schema"`
	ResponseSchema        ResponseSchema `json:"response_schema"`
	DocsURL               string         `json:"docs_url"`
	Paginated             bool           `json:"x-paginated,omitempty"`
	LocalFilter           string         `json:"x-local-filter,omitempty"`
}

func ptr(v int64) *int64 { return &v }

func objectSchema(properties map[string]Field, required ...string) Schema {
	return Schema{
		Dialect:              "https://json-schema.org/draft/2020-12/schema",
		Type:                 "object",
		AdditionalProperties: false,
		Properties:           properties,
		Required:             required,
	}
}

func str(location, description string) Field {
	return Field{Type: "string", Location: location, Description: description}
}

func integer(location, description string, min, max int64) Field {
	return Field{Type: "integer", Location: location, Description: description, Minimum: ptr(min), Maximum: ptr(max)}
}

func boolean(location, description string) Field {
	return Field{Type: "boolean", Location: location, Description: description}
}

func responseObject(properties map[string]ResponseField, required ...string) ResponseSchema {
	return ResponseSchema{
		Dialect:              "https://json-schema.org/draft/2020-12/schema",
		Type:                 "object",
		AdditionalProperties: true,
		Properties:           properties,
		Required:             required,
		ProjectedContracts:   map[string]ResponseField{},
		CursorAliases:        []string{},
	}
}

func responseField(kind, description string) ResponseField {
	return ResponseField{Type: kind, Description: description}
}

func responseFieldWithFormat(kind, format, description string) ResponseField {
	return ResponseField{Type: kind, Format: format, Description: description}
}

func paginatedResponse(collection string, extra map[string]ResponseField) ResponseSchema {
	properties := map[string]ResponseField{collection: responseField("array", "Items returned by the upstream API."), "cursor": responseField("string", "Opaque next-page cursor; empty when complete.")}
	for key, value := range extra {
		properties[key] = value
	}
	schema := responseObject(properties, collection, "cursor")
	schema.CollectionField, schema.CursorField = collection, "cursor"
	schema.CursorAliases = []string{"next_cursor"}
	return schema
}

func collectionResponse(collection string) ResponseSchema {
	schema := responseObject(map[string]ResponseField{collection: responseField("array", "Items returned by the upstream API.")}, collection)
	schema.CollectionField = collection
	return schema
}

func withProjectable(schema ResponseSchema, fields ...string) ResponseSchema {
	schema.ProjectableFields = append([]string(nil), fields...)
	sort.Strings(schema.ProjectableFields)
	return schema
}

func withRequiredStringFields(schema ResponseSchema, fields ...string) ResponseSchema {
	schema.RequiredFields = append([]string(nil), fields...)
	sort.Strings(schema.RequiredFields)
	schema.RequiredFieldTypes = make(map[string]string, len(fields))
	for _, field := range fields {
		schema.RequiredFieldTypes[field] = "string"
	}
	return schema
}

func withProjectedContracts(schema ResponseSchema, contracts map[string]ResponseField) ResponseSchema {
	schema.ProjectedContracts = contracts
	return schema
}

func prefixedFields(prefix string, fields []string) []string {
	out := make([]string, 1, len(fields)+1)
	out[0] = prefix
	for _, field := range fields {
		out = append(out, prefix+"."+field)
	}
	return out
}
