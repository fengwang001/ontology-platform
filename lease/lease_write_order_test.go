package lease

import (
	"errors"
	"testing"
	"time"

	"ontology/fence"
)

// 回归测试：钉住写入校验的顺序契约——先判定租约是否仍然有效
// （过期即无人持有，返回 ErrNotHeld），再交给围栏按水位判定；
// 且被拒的写入不得移动围栏水位。修复前顺序相反：过期窗口内拿旧令牌
// 写入会报 ErrStaleToken，且无人持有时的写入会抬高水位。
func TestCheckWriteExpiredReturnsNotHeld(t *testing.T) {
	c, l := setup()
	tok, err := l.Acquire("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// alice 持有期间用一个大令牌写入，把水位抬高。
	if err := l.CheckWrite(tok + 100); err != nil {
		t.Fatal(err)
	}
	high := l.fence.Max()
	c.Advance(2 * time.Minute) // alice 过期，资源尚未被他人重占

	// 旧令牌写入：正确类别是 ErrNotHeld（过期即无人持有），不是 ErrStaleToken。
	err = l.CheckWrite(tok)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("expired write should fail with ErrNotHeld, got %v", err)
	}
	if errors.Is(err, fence.ErrStaleToken) {
		t.Fatal("expired write must not be reported as stale-token")
	}
	// 被拒的写入不得产生副作用：水位保持不动。
	if got := l.fence.Max(); got != high {
		t.Fatalf("rejected write moved watermark: got %d, want %d", got, high)
	}

	// 新持有者拿到更大令牌后写入必须被接受（水位未被污染）。
	tok2, err := l.Acquire("bob", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.CheckWrite(tok2); err != nil {
		t.Fatalf("new holder's write should be accepted: %v", err)
	}
}

// 回归测试：钉住「写入被拒不得产生副作用」的契约——无人持有时
// CheckWrite 返回 ErrNotHeld 且不得抬升水位；之后合法持有者用
// 新令牌写入不得被判陈旧。修复前 ErrNotHeld 的写入也会抬水位。
func TestCheckWriteNotHeldDoesNotMoveWatermark(t *testing.T) {
	_, l := setup()
	err := l.CheckWrite(50)
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("write without holder should fail with ErrNotHeld, got %v", err)
	}
	if got := l.fence.Max(); got != 0 {
		t.Fatalf("rejected write moved watermark to %d", got)
	}
	tok, err := l.Acquire("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.CheckWrite(tok); err != nil {
		t.Fatalf("first holder's token should be accepted: %v", err)
	}
}
