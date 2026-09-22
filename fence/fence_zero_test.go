package fence

import (
	"errors"
	"testing"
)

// 回归测试：钉住「0 是无令牌的零值，不是合法令牌」的契约——
// Check(0) 必须返回可判定错误（errors.Is ErrZeroToken），且不得改变水位。
// 修复前新围栏上 Check(0) 被静默接受（0 < 0 不成立）。
func TestCheckZeroTokenRejected(t *testing.T) {
	f := New()
	err := f.Check(0)
	if !errors.Is(err, ErrZeroToken) {
		t.Fatalf("zero token must be rejected with ErrZeroToken, got %v", err)
	}
	if errors.Is(err, ErrStaleToken) {
		t.Fatal("zero-token error must be distinguishable from stale-token")
	}
	if got := f.Max(); got != 0 {
		t.Fatalf("rejected zero token must not move watermark, got %d", got)
	}

	// 水位抬高后，零值令牌同样要被拒绝且类别不变。
	issued := f.Issue()
	if err := f.Check(issued); err != nil {
		t.Fatal(err)
	}
	if err := f.Check(0); !errors.Is(err, ErrZeroToken) {
		t.Fatalf("zero token must stay rejected after watermark rose, got %v", err)
	}
	if got := f.Max(); got != issued {
		t.Fatalf("watermark regressed after zero-token check: got %d, want %d", got, issued)
	}
}
