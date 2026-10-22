package web

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
	if b, e := exec.CommandContext(ctx, node, "--test", "testdata/http.test.cjs", "testdata/registration.test.cjs", "testdata/accounts.test.cjs").CombinedOutput(); e != nil {
		t.Fatalf("browser request-helper checks failed: %s", b)
	}
}
