package eva

import (
	"os"
)

func captureAwardFailure(body []byte) {
	dir := os.Getenv("AWARDDESK_AWARD_DEBUG_DIR")
	if dir == "" || os.MkdirAll(dir, 0700) != nil {
		return
	}
	f, err := os.CreateTemp(dir, "award-*.private.html")
	if err != nil {
		return
	}
	_ = f.Chmod(0600)
	_, _ = f.Write(body)
	_ = f.Close()
}
