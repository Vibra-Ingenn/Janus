package format

import (
	"fmt"
	"sort"
	"strings"
)

const genericGBNFRules = `value  ::= object | array | string | number | ("true" | "false" | "null") ws
object ::= "{" ws ( string ":" ws value ("," ws string ":" ws value)* )? "}" ws
array  ::= "[" ws ( value ("," ws value)* )? "]" ws
string ::= "\"" ( [^"\\\x7F\x00-\x1F] | "\\" (["\\bfnrt] | "u" [0-9a-fA-F]{4}) )* "\"" ws
number ::= ("-"? ([0-9] | [1-9] [0-9]{0,15})) ("." [0-9]+)? ([eE] [-+]? [0-9] [1-9]{0,15})? ws
integer ::= "-"? ([0-9] | [1-9] [0-9]{0,15}) ws
ws ::= | " " | "\n" [ \t]{0,20}
`

// JSONObjectGrammar is the GBNF used for response_format type json_object.
func JSONObjectGrammar() string {
	return "root ::= object\n" + genericGBNFRules
}

// SchemaToGBNF converts a JSON Schema (supported subset) into a llama.cpp GBNF grammar.
func SchemaToGBNF(schema map[string]any) string {
	c := &gbnfConverter{seen: map[string]bool{}}
	c.emitSchema("root", schema)
	return strings.Join(c.lines, "\n") + "\n" + genericGBNFRules
}

type gbnfConverter struct {
	lines []string
	seen  map[string]bool
}

func (c *gbnfConverter) emitSchema(path string, schema map[string]any) string {
	name := ruleName(path)
	if c.seen[name] {
		return name
	}
	body := c.schemaBody(path, schema)
	if body == "" {
		return "value"
	}
	if body == name || isGenericRef(body) {
		return body
	}
	c.seen[name] = true
	c.lines = append(c.lines, name+" ::= "+body)
	return name
}

func isGenericRef(s string) bool {
	switch s {
	case "value", "object", "array", "string", "number", "integer":
		return true
	default:
		return false
	}
}

func (c *gbnfConverter) schemaBody(path string, schema map[string]any) string {
	typ := schemaString(schema, "type")
	switch typ {
	case "object":
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			return "object"
		}
		required := schemaStringSlice(schema["required"])
		reqSet := map[string]bool{}
		for _, k := range required {
			reqSet[k] = true
		}

		var reqParts []string
		for _, key := range required {
			prop, ok := props[key]
			if !ok {
				continue
			}
			child, _ := prop.(map[string]any)
			if child == nil {
				child = map[string]any{}
			}
			childRule := c.emitSchema(path+"-"+sanitizeKey(key), child)
			reqParts = append(reqParts, gbnfKey(key)+" ws \":\" ws "+childRule)
		}

		var optParts []string
		keys := make([]string, 0, len(props))
		for k := range props {
			if !reqSet[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			prop, _ := props[key].(map[string]any)
			if prop == nil {
				prop = map[string]any{}
			}
			childRule := c.emitSchema(path+"-"+sanitizeKey(key), prop)
			optParts = append(optParts, `( "," ws `+gbnfKey(key)+` ws ":" ws `+childRule+` )?`)
		}

		body := `"{" ws `
		if len(reqParts) == 0 {
			body += strings.Join(optParts, " ")
		} else {
			body += reqParts[0]
			for i := 1; i < len(reqParts); i++ {
				body += ` "," ws ` + reqParts[i]
			}
			if len(optParts) > 0 {
				body += " " + strings.Join(optParts, " ")
			}
		}
		body += ` "}" ws`
		return body

	case "string":
		if enum := schema["enum"]; enum != nil {
			vals := schemaAnySlice(enum)
			if len(vals) == 0 {
				return "string"
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = gbnfQuotedString(fmt.Sprint(v))
			}
			return "(" + strings.Join(parts, " | ") + ") ws"
		}
		return "string"

	case "number":
		return "number"

	case "integer":
		return "integer"

	case "boolean":
		return `("true" | "false") ws`

	case "array":
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			items = map[string]any{}
		}
		itemRule := c.emitSchema(path+"-item", items)
		minItems := schemaInt(schema, "minItems")
		if minItems >= 1 {
			return `"[" ws ` + itemRule + ` ("," ws ` + itemRule + `)* "]" ws`
		}
		return `"[" ws (` + itemRule + ` ("," ws ` + itemRule + `)* )? "]" ws`

	default:
		return "value"
	}
}

func ruleName(path string) string {
	path = strings.ToLower(path)
	if path == "" {
		return "root"
	}
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "root"
	}
	return out
}

func sanitizeKey(key string) string {
	return ruleName(key)
}

func gbnfKey(key string) string {
	return `"\"` + escapeGBNF(key) + `\""`
}

func gbnfQuotedString(s string) string {
	return `"\"` + escapeGBNF(s) + `\""`
}

func escapeGBNF(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func schemaString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func schemaInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

func schemaStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func schemaAnySlice(v any) []any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	return raw
}
