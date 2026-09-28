package lwwset

import (
	"fmt"
	"math/rand"
	"testing"
)

// op 是随机调度的一步操作。
type op struct {
	rep    int
	remove bool
	elem   string
	ts     int64
}

// TestRandomDifferential：对同一随机历史，分别用
//   - 朴素参照
//   - 仅整份合并收敛
//   - 每轮增量合并收敛
//
// 三种方式计算，最终记录必须完全一致。
func TestRandomDifferential(t *testing.T) {
	seed := timeNowSeed()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("[random] seed=%d", seed)

	const replicas = 4
	const rounds = 60

	history := make([]op, 0, rounds)
	elements := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	// 朴素参照：每个副本一份独立状态，整份合并即逐元素取最大。
	refFull := make([]naive, replicas)
	refInc := make([]naive, replicas)
	for i := range refFull {
		refFull[i] = naive{}
		refInc[i] = naive{}
	}

	full := make([]*Replica, replicas)
	inc := make([]*Replica, replicas)
	for i := range full {
		full[i], _ = NewReplica(int64(i+1), 500)
		inc[i], _ = NewReplica(int64(i+1), 500)
	}

	applyOp := func(o op) {
		for _, group := range [][]*Replica{full, inc} {
			r := group[o.rep]
			if o.remove {
				_ = r.Remove(o.elem, o.ts)
			} else {
				_ = r.Add(o.elem, o.ts)
			}
		}
		for _, ref := range [][]naive{refFull, refInc} {
			n := ref[o.rep]
			if o.remove {
				n.apply(o.elem, 0, o.ts)
			} else {
				n.apply(o.elem, o.ts, 0)
			}
		}
	}

	for round := 0; round < rounds; round++ {
		o := op{
			rep:    rng.Intn(replicas),
			remove: rng.Intn(2) == 0,
			elem:   elements[rng.Intn(len(elements))],
			// 时间戳刻意允许乱序与并列。
			ts: int64(1 + rng.Intn(15)),
		}
		history = append(history, o)
		applyOp(o)
		t.Logf("[random round %d] rep=%d remove=%v elem=%s ts=%d",
			round, o.rep+1, o.remove, o.elem, o.ts)

		// 每轮随机挑一对副本同步：inc 走增量，full 组延迟到末尾整份合并。
		x, y := rng.Intn(replicas), rng.Intn(replicas)
		if x != y {
			if err := inc[x].MergeChanges(inc[y]); err != nil {
				t.Fatalf("incremental merge %d<-1$%d: %v", x+1, y+1, err)
			}
			refInc[x].merge(refInc[y])
		}
	}

	// full 组：仅在最后做全连通整份合并。
	for pass := 0; pass < replicas; pass++ {
		for i := 0; i < replicas; i++ {
			for j := 0; j < replicas; j++ {
				if i != j {
					if err := full[i].Merge(full[j]); err != nil {
						t.Fatal(err)
					}
					refFull[i].merge(refFull[j])
					if err := inc[i].Merge(inc[j]); err != nil {
						t.Fatal(err)
					}
					refInc[i].merge(refInc[j])
				}
			}
		}
	}

	for i := 0; i < replicas; i++ {
		if fmt.Sprint(full[i].Elements()) != fmt.Sprint(refFull[i].members()) {
			t.Fatalf("rep %d full vs naive: %v != %v", i+1, full[i].Elements(), refFull[i].members())
		}
		if fmt.Sprint(inc[i].Elements()) != fmt.Sprint(refInc[i].members()) {
			t.Fatalf("rep %d incremental vs naive: %v != %v", i+1, inc[i].Elements(), refInc[i].members())
		}
		if eq, msg := recordsEqual(full[i], inc[i]); !eq {
			t.Fatalf("rep %d full vs incremental mismatch:\n%s", i+1, msg)
		}
		if full[i].Checksum() != inc[i].Checksum() {
			t.Fatalf("rep %d checksum mismatch", i+1)
		}
	}

	root := full[0]
	for i := 1; i < replicas; i++ {
		if eq, msg := recordsEqual(root, full[i]); !eq {
			t.Fatalf("convergence failure at rep %d: %s", i+1, msg)
		}
	}
	t.Logf("[random] converged members=%v history=%d ops", root.Elements(), len(history))
	dump(t, "random converged", root)
}
