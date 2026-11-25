package llm

type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}
