package agent

import (
	"testing"

	"github.com/TonmoyTalukder/werkz/internal/config"
)

// A context window limit keeps auto-compaction inside it.
func TestCompactThresholdFollowsWindowLimit(t *testing.T) {
	cfg := &config.Config{}
	if at, hard := CompactThreshold(cfg, 1_000_000); at != 300_000 || hard != 700_000 {
		t.Fatalf("defaults: %d %d", at, hard)
	}
	cfg.ContextWindow = 350_000
	if at, hard := CompactThreshold(cfg, 1_000_000); at != 280_000 || hard != 322_000 {
		t.Fatalf("350k window: %d %d", at, hard)
	}
	cfg.CompactAt = 150_000
	if at, _ := CompactThreshold(cfg, 1_000_000); at != 150_000 {
		t.Fatalf("an earlier /compact auto setting still wins: %d", at)
	}
}
