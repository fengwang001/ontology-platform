package imaging

import (
	"os"
	"testing"
)

var diffLogging = os.Getenv("DIFF_LOG") == "1"

// TestDifferentialSmoke 小规模烟雾差分，保证常规 go test 也覆盖到随机对照。
func TestDifferentialSmoke(t *testing.T) {
	runDiff(t, 42, 120)
	runDiff(t, 7, 120)
}
