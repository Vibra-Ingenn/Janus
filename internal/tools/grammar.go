// Package tools provides GBNF grammar-constrained tool calling for local LLM inference.
//
// The model is constrained to output valid JSON matching the tool_call schema,
// then the Go framework dispatches to the appropriate tool handler.
package tools

// ToolCallGrammar is the GBNF grammar that constrains model output to a
// valid tool_call JSON object. The model MUST produce output matching:
//
//	{"tool_call": {"name": "<tool_name>", "arguments": {<key_value_pairs>}}}
//
// This grammar guarantees 100% valid JSON — no post-hoc error handling needed.
const ToolCallGrammar = `
root        ::= "{" ws "\"tool_call\"" ws ":" ws tool-obj ws "}"
tool-obj    ::= "{" ws "\"name\"" ws ":" ws tool-name ws "," ws "\"arguments\"" ws ":" ws args-obj ws "}"
tool-name   ::= "\"" [a-z_]+ "\""
args-obj    ::= "{" ws (kv-pair (ws "," ws kv-pair)*)? ws "}"
kv-pair     ::= string ws ":" ws value
value       ::= string | number | bool-val | null-val | array | object
string      ::= "\"" ([^"\\] | "\\" .)* "\""
number      ::= "-"? [0-9]+ ("." [0-9]+)? ([eE] [+-]? [0-9]+)?
bool-val    ::= "true" | "false"
null-val    ::= "null"
array       ::= "[" ws (value (ws "," ws value)*)? ws "]"
object      ::= "{" ws (kv-pair (ws "," ws kv-pair)*)? ws "}"
ws          ::= [ \t\n]*
`

// GrammarRoot is the entry rule for the GBNF grammar.
const GrammarRoot = "root"

// PlainJSONGrammar constrains the model to output any valid JSON object.
// Useful when you want structured output without specific tool_call schema.
const PlainJSONGrammar = `
root   ::= object
object ::= "{" ws (kv (ws "," ws kv)*)? ws "}"
kv     ::= string ws ":" ws value
value  ::= string | number | bool-val | null-val | array | object
string ::= "\"" ([^"\\] | "\\" .)* "\""
number ::= "-"? [0-9]+ ("." [0-9]+)? ([eE] [+-]? [0-9]+)?
bool-val   ::= "true" | "false"
null-val   ::= "null"
array  ::= "[" ws (value (ws "," ws value)*)? ws "]"
ws     ::= [ \t\n]*
`
