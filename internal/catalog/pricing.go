package catalog

import "fmt"

func PriceLabel(mi ModelInfo) (string, bool) {
	switch {
	case mi.Free || mi.Provider == "ollama":
		return "free", false
	case mi.In > 0:
		return fmt.Sprintf("paid · $%g in / $%g out per 1M tokens", mi.In, mi.Out), true
	}
	return "paid · billed by " + mi.Provider, true
}
