package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Windows rejects timestamp writes on a renamed running exe. The sidecar is
// the fallback that keeps the rollback copy alive for 24h in that case.
func TestStampRollbackFallsBackToSidecar(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "monctl.old")

	if err := stampRollback(old, time.Now()); err != nil {
		t.Fatalf("stampRollback: %v", err)
	}
	if _, err := os.Stat(old + ".stamp"); err != nil {
		t.Fatalf("sidecar stamp was not written: %v", err)
	}
}
