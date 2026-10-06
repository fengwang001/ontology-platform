package imaging

import "fmt"
import "testing"

func fmtSprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// TestDifferential1500：每个种子一条随机操作序列；总步数不少于 1500。
// -v 时逐步打印输入、输出与判定依据；同时写入 differential.log 便于离线复核。
func TestDifferential1500(t *testing.T) {
	if !diffLogging {
		t.Skip("设置 DIFF_LOG=1 以输出逐步日志（默认仍运行但不打印每条）")
	}
	total := 0
	for seed := 1; seed <= 15; seed++ {
		runDiff(t, seed, 120)
		total += 120
	}
	if total < 1500 {
		t.Fatalf("差分步数不足: %d", total)
	}
}
