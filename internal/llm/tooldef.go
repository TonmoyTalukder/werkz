package llm

type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	// Extra holds other top-level keys of the input schema ($defs, additionalProperties…), as MCP
	// servers send them; nil for werkz's own tools.
	Extra map[string]any
}
