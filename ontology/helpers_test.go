package ontology

import (
	"fmt"
	"os"
	"testing"
)

// echo 控制测试过程是否把“输入 / 实际输出 / 判定依据”同时打印到 stdout。
// 默认在 -v 模式下经 t.Logf 打印；设置 ONT_TEST_ECHO=1 会额外打到 stdout，
// 便于 `go test` 不带 -v 时直接留存完整判定记录。
func echo(t *testing.T, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	t.Logf("%s", line)
	if os.Getenv("ONT_TEST_ECHO") == "1" {
		fmt.Println(line)
	}
}

func basisString(b VisBasis) string {
	if b.Distance < 0 {
		return fmt.Sprintf("%s(type-default)", b.Vis)
	}
	return fmt.Sprintf("%s(from %s dist=%d seq=%d)", b.Vis, b.Source, b.Distance, b.Seq)
}

func errCode(e *ArbError) string {
	if e == nil {
		return "OK"
	}
	return string(e.Code)
}
