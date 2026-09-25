package dwr

import (
	"fmt"
	"testing"
)

// writeSeq 是表驱动用例：一串写 + 期望。
type writeSeq struct {
	name string
	ops  func(e *Engine) error // 依次执行的有效写
}

func seqs() []writeSeq {
	return []writeSeq{
		{"seven-writes", func(e *Engine) error {
			for _, f := range []func() error{
				func() error { return e.Put("a", "a1", 1) },
				func() error { return e.Put("b", "b1", 2) },
				func() error { return e.Put("c", "c1", 3) },
				func() error { return e.PutOne(0, "a", "a2", 4) },
				func() error { return e.DelOne(1, "c", 5) },
				func() error { return e.DelOne(0, "b", 6) },
				func() error { return e.PutOne(0, "d", "d1", 7) },
			} {
				if err := f(); err != nil {
					return err
				}
			}
			return nil
		}},
		{"tombstone-wins", func(e *Engine) error {
			if err := e.Put("x", "x1", 1); err != nil {
				return err
			}
			return e.DelOne(1, "x", 2)
		}},
		{"double-write-heals", func(e *Engine) error {
			if err := e.PutOne(0, "x", "x1", 1); err != nil {
				return err
			}
			return e.Put("x", "x2", 2)
		}},
	}
}

// TestCheckedKeysBounded 钉住：对账只遍历脏集合，检查个数不随总键数 m 增长。
func TestCheckedKeysBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		e := New()
		for i := 0; i < m; i++ {
			if err := e.Put(fmt.Sprintf("k%d", i), "v", int64(i+1)); err != nil {
				t.Fatalf("m=%d put: %v", m, err)
			}
		}
		if err := e.PutOne(0, "k0", "v2", int64(m+1)); err != nil {
			t.Fatalf("m=%d putone: %v", m, err)
		}
		e.Reconcile()
		if e.checked > 2 {
			t.Errorf("m=%d: checked=%d, want <= 2 (与 m 无关的小常数)", m, e.checked)
		}
	}
}

// TestReplicasConverge 钉住不变量2：对账后 A、B 逐键相同，脏集合清空。
func TestReplicasConverge(t *testing.T) {
	for _, tc := range seqs() {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			if err := tc.ops(e); err != nil {
				t.Fatal(err)
			}
			e.Reconcile()
			if !e.Consistent() {
				t.Errorf("A、B 未收敛: a=%v b=%v", e.a, e.b)
			}
			if len(e.dirty) != 0 {
				t.Errorf("脏集合未清空: %v", e.dirty)
			}
		})
	}
}

// TestReconcileIdempotent 钉住不变量3：两次对账之间无写，第二次不改变任何状态。
func TestReconcileIdempotent(t *testing.T) {
	for _, tc := range seqs() {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			if err := tc.ops(e); err != nil {
				t.Fatal(err)
			}
			e.Reconcile()
			snapA := fmt.Sprint(e.a)
			snapB := fmt.Sprint(e.b)
			e.Reconcile()
			if e.checked != 0 {
				t.Errorf("第二次对账仍检查了 %d 个键", e.checked)
			}
			if fmt.Sprint(e.a) != snapA || fmt.Sprint(e.b) != snapB {
				t.Errorf("第二次对账改变了状态")
			}
		})
	}
}
