package fence

import (
	"errors"
	"testing"
)

// 回归测试：钉住「0 是无令牌的零值，不是合法令牌」这条契约——
// Check(0) 必须返回可判定的错误（ErrZeroToken，且与 ErrStaleToken 可区分），
// 且无论水位高低都不得改变水位。
func TestCheckRejectsZeroToken(t *testing.T) {
	f := New()
	err := f.Check(0)
	if err == nil {
		t.Fatal("Check(0) on a fresh fence must fail")
	}
	if !errors.Is(err, ErrZeroToken) {
		t.Fatalf("expected ErrZeroToken, got %v", err)
	}
	if errors.Is(err, ErrStaleToken) {
		t.Fatal("zero-token error must be distinguishable from stale-token")
	}
	if got := f.Max(); got != 0 {
		t.Fatalf("rejected zero token must not move watermark, got %d", got)
	}

	// 水位抬高后，Check(0) 同样必须被拒且水位不变。
	f.Issue() // 水位抬到 1
	if err := f.Check(0); !errors.Is(err, ErrZeroToken) {
		t.Fatalf("expected ErrZeroToken with raised watermark, got %v", err)
	}
	if got := f.Max(); got != 1 {
		t.Fatalf("rejected zero token must not move watermark, got %d", got)
	}
}
