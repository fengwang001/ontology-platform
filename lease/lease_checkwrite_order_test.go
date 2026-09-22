package lease

import (
	"errors"
	"testing"
	"time"

	"ontology/fence"
)

// 回归测试：钉住写入校验的顺序契约——先判定租约是否仍然有效
// （过期即无人持有，返回 ErrNotHeld），再交给围栏按水位判定；
// 且被拒的写入不得产生任何副作用，尤其不得移动围栏水位。
func TestCheckWriteExpiredReturnsNotHeldAndKeepsWatermark(t *testing.T) {
	c, l := setup()
	tok, err := l.Acquire("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// alice 持有期间用一个大令牌写入，把水位抬高。
	const high = fence.Token(100)
	if err := l.CheckWrite(high); err != nil {
		t.Fatal(err)
	}
	// alice 过期且资源尚未被他人重占：旧令牌写入必须是 ErrNotHeld，
	// 而不是 ErrStaleToken（两者必须可区分）。
	c.Advance(2 * time.Minute)
	err = l.CheckWrite(tok)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("expired write should fail with ErrNotHeld, got %v", err)
	}
	if errors.Is(err, fence.ErrStaleToken) {
		t.Fatal("expired write must not be reported as stale-token")
	}
	// 无人持有时被拒的写入不得移动水位。
	if err := l.CheckWrite(high + 50); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("not-held write should fail with ErrNotHeld, got %v", err)
	}
	if got := l.fence.Max(); got != high {
		t.Fatalf("rejected write moved watermark to %d, want %d", got, high)
	}
	// 新持有者拿到合法令牌后写入必须被接受（水位未被污染）。
	tok2, err := l.Acquire("bob", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.CheckWrite(tok2); err != nil {
		t.Fatalf("new holder's write should be accepted, got %v", err)
	}
}
