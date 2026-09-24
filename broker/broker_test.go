package broker

import (
	"errors"
	"testing"

	"ontology/seqstate"
)

// epoch 升级时的「清空全部分区状态」必须是惰性失效：
// 检查/修改的 (pid,分区) 状态条目数不随分区数 m 增长。
func TestEpochUpgradeIsLazy(t *testing.T) {
	const limit = 4 // 与 m 无关的小常数
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := New(m, 4)
		for p := 0; p < m; p++ {
			if _, _, err := b.Produce(1, 0, p, 0, p); err != nil {
				t.Fatalf("m=%d fill p=%d: %v", m, p, err)
			}
		}
		if _, _, err := b.Produce(1, 1, 0, 0, 0); err != nil {
			t.Fatalf("m=%d upgrade: %v", m, err)
		}
		if b.checked > limit {
			t.Fatalf("m=%d: upgrade checked %d entries", m, b.checked)
		}
		if _, _, err := b.Produce(1, 1, m-1, 0, 0); err != nil {
			t.Fatalf("m=%d part m-1 must be accepted: %v", m, err)
		}
		if b.checked > limit {
			t.Fatalf("m=%d: part m-1 checked %d entries", m, b.checked)
		}
		if _, _, err := b.Produce(1, 1, 1, 1, 0); !errors.Is(err, seqstate.ErrOutOfOrder) {
			t.Fatalf("m=%d: part 1 seq 1 must be out-of-order, got %v", m, err)
		}
		if b.checked > limit {
			t.Fatalf("m=%d: part 1 checked %d entries", m, b.checked)
		}
	}
}
