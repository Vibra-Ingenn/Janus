package format

import (
	"fmt"
	"strings"
)

// ResponseFormat mirrors OpenAI response_format on chat completions.
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *JSONSchemaWrap `json:"json_schema,omitempty"`
}

// JSONSchemaWrap holds the nested schema object Vibe Engine sends.
type JSONSchemaWrap struct {
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema"`
}

// ResolveGrammar picks a GBNF grammar from a raw grammar field or response_format.
// Priority: grammar > json_schema > json_object. type text or empty means no constraint.
func ResolveGrammar(rawGrammar string, rf *ResponseFormat) (string, error) {
	if g := strings.TrimSpace(rawGrammar); g != "" {
		return g, nil
	}
	if rf == nil {
		return "", nil
	}
	typ := strings.TrimSpace(rf.Type)
	switch typ {
	case "", "text":
		return "", nil
	case "json_object":
		return JSONObjectGrammar(), nil
	case "json_schema":
		if rf.JSONSchema == nil || rf.JSONSchema.Schema == nil {
			return "", fmt.Errorf("json_schema response_format missing schema")
		}
		return SchemaToGBNF(rf.JSONSchema.Schema), nil
	default:
		return "", nil
	}
}
