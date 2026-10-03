package tcc

import (
	"errors"
	"testing"

	"ontology/ledger"
)

// 拒绝优先级：参数非法 > ErrClock > 状态类 > 资源类（Capacity > Insufficient）。
func TestRejectPriority(t *testing.T) {
	rm, _, _ := newEnv(t, 10, 2, map[string]int64{"a": 2})
	must(t, rm.Try("x", "1", "a", 1, 10))
	must(t, rm.Try("x", "0", "a", 1, 10)) // 两条占满 N=2

	// 参数非法压过时钟回拨与容量不足。
	if err := rm.Try("", "2", "a", 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid vs clock: %v", err)
	}
	// 时钟回拨压过状态/资源类。
	if err := rm.Try("x", "2", "a", 1, 9); !errors.Is(err, ErrClock) {
		t.Fatalf("clock vs capacity: %v", err)
	}
	// 容量满（资源类先于 Insufficient）：b 无余额但应报 Capacity。
	if err := rm.Try("x", "2", "b", 1, 10); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity vs insufficient: %v", err)
	}
	// 空回滚在容量满时同样 Capacity，且不写标记。
	if err := rm.Cancel("y", "2", 10); !errors.Is(err, ErrCapacity) {
		t.Fatalf("empty rollback full: %v", err)
	}
	// 已有 Cancelled 记录上的迟到 Try：状态类压过容量类。
	// 容量已满，新建 Cancel 会被拒；用满容量内的一条已 Tried 记录先 Cancel。
	// 容量已满时空回滚被拒，无法新增 Cancelled 标记；
	// 直接 Cancel 一条 Tried 记录（状态类路径），随后迟到 Try 得 ErrHanging。
	must(t, rm.Cancel("x", "0", 10))
	if err := rm.Try("x", "0", "a", 1, 10); !errors.Is(err, ErrHanging) {
		t.Fatalf("hanging vs capacity: %v", err)
	}
	// 可用额恰等通过。
	rm2, _, _ := newEnv(t, 10, 10, map[string]int64{"c": 50})
	if err := rm2.Try("x", "1", "c", 50, 0); err != nil {
		t.Fatalf("exact avail: %v", err)
	}
}

// 被拒绝操作不改变时钟与记录数。
func TestRejectNoSideEffect(t *testing.T) {
	rm, lg, _ := newEnv(t, 10, 10, map[string]int64{"a": 100})
	must(t, rm.Try("x", "1", "a", 60, 5))
	before := rm.Len()
	if err := rm.Try("x", "2", "a", 50, 6); !errors.Is(err, ErrInsufficient) {
		t.Fatal(err)
	}
	if rm.Len() != before || lg.Fz("a") != 60 {
		t.Fatalf("reject side effect: len=%d fz=%d", rm.Len(), lg.Fz("a"))
	}
	// 被拒绝不推进时钟：时钟停在 5。
	// 先用 now=5 的成功操作验证时钟仍为 5（幂等 Try 不推进语义不适用，
	// 故改用只读 Avail 确认 now=5 仍被接受且 now=4 被拒）。
	if _, err := rm.Avail("a", 5); err != nil {
		t.Fatalf("now=5 should still be accepted: %v", err)
	}
	if _, err := rm.Avail("a", 4); !errors.Is(err, ErrClock) {
		t.Fatalf("now=4 should be ErrClock, got %v", err)
	}
}

func TestConstructorValidation(t *testing.T) {
	lg := ledger.New()
	for _, c := range []struct{ ttl, N int64 }{
		{0, 10}, {1_000_000_001, 10}, {10, 0}, {10, 1_000_001},
	} {
		if _, err := New(lg, c.ttl, c.N); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ttl=%d N=%d err=%v", c.ttl, c.N, err)
		}
	}
}
