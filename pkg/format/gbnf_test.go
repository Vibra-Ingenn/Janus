package format

import (
	"strings"
	"testing"
)

func TestSchemaToGBNF_objectRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
		},
		"required": []any{"name", "age"},
	}
	g := SchemaToGBNF(schema)
	if !strings.Contains(g, "root ::=") {
		t.Fatalf("missing root rule: %s", g)
	}
	if !strings.Contains(g, `\"name\"`) || !strings.Contains(g, `\"age\"`) {
		t.Fatalf("expected required keys in grammar: %s", g)
	}
	if !strings.Contains(g, "integer") {
		t.Fatalf("expected integer rule ref: %s", g)
	}
}

func TestSchemaToGBNF_enum(t *testing.T) {
	schema := map[string]any{
		"type": "string",
		"enum": []any{"read_file", "write_file"},
	}
	c := &gbnfConverter{}
	body := c.schemaBody("root-action-type", schema)
	if !strings.Contains(body, `\"read_file\"`) || !strings.Contains(body, `\"write_file\"`) {
		t.Fatalf("enum grammar: %s", body)
	}
}

func TestSchemaToGBNF_arrayMinItems(t *testing.T) {
	schema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
		},
		"minItems": 1,
	}
	c := &gbnfConverter{}
	body := c.schemaBody("root-tasks", schema)
	if strings.Contains(body, ")?") {
		t.Fatalf("minItems 1 should not make contents optional: %s", body)
	}
}

func TestJSONObjectGrammar(t *testing.T) {
	g := JSONObjectGrammar()
	if !strings.HasPrefix(g, "root ::= object") {
		t.Fatalf("json_object grammar: %s", g)
	}
	if !strings.Contains(g, "value  ::= object") {
		t.Fatalf("missing generic rules: %s", g)
	}
}

func TestResolveGrammar_priority(t *testing.T) {
	raw := "root ::= object\n" + genericGBNFRules
	g, err := ResolveGrammar("root ::= object", &ResponseFormat{Type: "json_object"})
	if err != nil || g != raw[:len("root ::= object")] {
		if err != nil || !strings.HasPrefix(g, "root ::= object") {
			t.Fatalf("grammar field should win: err=%v g=%q", err, g)
		}
	}
	g2, err := ResolveGrammar("", &ResponseFormat{Type: "json_object"})
	if err != nil || !strings.HasPrefix(g2, "root ::= object") {
		t.Fatalf("json_object: err=%v", err)
	}
}
