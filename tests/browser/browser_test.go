package browser

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserRequestTokenRecovery(t *testing.T) {
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("Node is optional; browser request-helper checks require Node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if b, e := exec.CommandContext(ctx, node, "--test", "http.test.cjs", "registration.test.cjs", "accounts.test.cjs", "award-mode.test.cjs").CombinedOutput(); e != nil {
		t.Fatalf("browser request-helper checks failed: %s", b)
	}
}
