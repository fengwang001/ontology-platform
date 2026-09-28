package lwwset

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestIncrementalEqualsFullMerge：分多轮增量合并，与一次性整份合并结果一致。
func TestIncrementalEqualsFullMerge(t *testing.T) {
	src, _ := NewReplica(2, 1000)
	dstInc, _ := NewReplica(1, 1000)
	dstFull, _ := NewReplica(3, 1000)

	rounds := []func(){
		func() {
			must(t, src.Add("x", 1))
			must(t, src.Add("y", 2))
		},
		func() {
			must(t, src.Remove("x", 3))
			must(t, src.Add("z", 4))
		},
		func() {
			must(t, src.Remove("y", 2)) // 并列：y 被删除
			must(t, src.Add("x", 3))    // 并列添加，仍偏删除
			must(t, src.Add("x", 10))
		},
		func() {
			must(t, src.Remove("z", 9)) // 旧删除乱序
			must(t, src.Remove("z", 11))
		},
	}

	for i, round := range rounds {
		round()
		must(t, dstInc.MergeChanges(src))
		pos, _ := dstInc.MergePosition(2)
		t.Logf("[round %d] incremental pos=%d members=%v", i+1, pos, dstInc.Elements())
		if pos != src.Seq() {
			t.Fatalf("merge position want %d, got %d", src.Seq(), pos)
		}
	}
	must(t, dstFull.Merge(src))

	if eq, msg := recordsEqual(dstInc, dstFull); !eq {
		t.Fatalf("incremental != full:\n%s", msg)
	}
	if fmt.Sprint(dstInc.Elements()) != fmt.Sprint(dstFull.Elements()) {
		t.Fatalf("member sets differ: %v vs %v", dstInc.Elements(), dstFull.Elements())
	}
	dump(t, "incremental dst final", dstInc)
	dump(t, "full dst final", dstFull)

	// 增量合并且幂等：无新内容时什么都不做。
	before := dstInc.Checksum()
	must(t, dstInc.MergeChanges(src))
	if dstInc.Checksum() != before {
		t.Fatal("idempotent incremental merge changed state")
	}

	// 外部变更流路径（ChangesSince + ApplyChanges）与整份合并一致。
	dstExt, _ := NewReplica(4, 1000)
	changes, head, err := src.ChangesSince(0)
	if err != nil || head != src.Seq() {
		t.Fatalf("ChangesSince: err=%v head=%d want %d", err, head, src.Seq())
	}
	must(t, dstExt.ApplyChanges(2, changes))
	if eq, msg := recordsEqual(dstExt, dstFull); !eq {
		t.Fatalf("ApplyChanges != full:\n%s", msg)
	}

	if _, _, err := src.ChangesSince(-1); !errors.Is(err, ErrInvalidSince) {
		t.Fatalf("want ErrInvalidSince, got %v", err)
	}

	// 合并位置查询的参数校验。
	if _, err := dstInc.MergePosition(0); !errors.Is(err, ErrInvalidReplicaID) {
		t.Fatalf("want ErrInvalidReplicaID, got %v", err)
	}
}

// TestIncrementalSequenceValidation：序号不连续 / 非法条目必须整体拒绝、不留痕。
func TestIncrementalSequenceValidation(t *testing.T) {
	dst, _ := NewReplica(1, 1000)

	// 直接使用 ApplyChanges 构造异常流。
	good := []Change{
		{Seq: 1, Element: "a", AddTime: 1},
		{Seq: 2, Element: "a", RemoveTime: 5},
	}
	must(t, dst.ApplyChanges(7, good))

	sumBefore := dst.Checksum()
	posBefore, _ := dst.MergePosition(7)

	tryStream := func(name string, sourceID int64, stream []Change, want error) {
		t.Helper()
		err := dst.ApplyChanges(sourceID, stream)
		if !errors.Is(err, want) {
			t.Fatalf("%s: want %v got %v", name, want, err)
		}
		if dst.Checksum() != sumBefore {
			t.Fatalf("%s: records changed after rejection", name)
		}
		pos, _ := dst.MergePosition(7)
		if pos != posBefore {
			t.Fatalf("%s: merge position changed %d -> %d", name, posBefore, pos)
		}
		t.Logf("[reject-stream] %s -> %v; state untouched (pos=%d)", name, err, pos)
	}

	tryStream("gap", 7, []Change{{Seq: 4, Element: "b", AddTime: 1}}, ErrSequenceGap)
	tryStream("empty element", 7, []Change{{Seq: 3, Element: "", AddTime: 1}}, ErrEmptyElement)
	tryStream("no timestamp", 7, []Change{{Seq: 3, Element: "b"}}, ErrMissingTimestamp)
	tryStream("both timestamps", 7, []Change{{Seq: 3, Element: "b", AddTime: 1, RemoveTime: 2}}, ErrMissingTimestamp)
	tryStream("bad source id", 0, nil, ErrInvalidReplicaID)

	// 拒绝后正确的续流仍可无缝接上，最终结果与“无失败尝试”一致。
	must(t, dst.ApplyChanges(7, []Change{{Seq: 3, Element: "b", AddTime: 6}}))
	rec, ok := dst.Lookup("b")
	if !ok || !rec.present() || rec.AddTime != 6 {
		t.Fatalf("resumed stream produced wrong record: %+v ok=%v", rec, ok)
	}
	dump(t, "after rejected streams then resume at seq 3", dst)
}

// TestAssociativity：不同合并顺序收敛到同一结果，并与朴素参照一致。
func TestAssociativity(t *testing.T) {
	a, _ := NewReplica(1, 1000)
	b, _ := NewReplica(2, 1000)
	c, _ := NewReplica(3, 1000)

	must(t, a.Add("x", 5))
	must(t, a.Remove("x", 5)) // 并列删除
	must(t, b.Add("x", 6))
	must(t, b.Add("y", 1))
	must(t, c.Remove("y", 2))
	must(t, c.Add("z", 9))

	left, _ := NewReplica(10, 1000)
	right, _ := NewReplica(11, 1000)
	must(t, left.Merge(a))
	must(t, left.Merge(b))
	must(t, left.Merge(c))
	must(t, right.Merge(c))
	must(t, right.Merge(b))
	must(t, right.Merge(a))

	if eq, msg := recordsEqual(left, right); !eq {
		t.Fatalf("merge order not associative/commutative:\n%s", msg)
	}
	if left.Checksum() != right.Checksum() {
		t.Fatal("checksums differ across merge orders")
	}
	t.Logf("[assoc] members=%v checksum=%s", left.Elements(), left.Checksum())

	ref := naive{}
	ref.merge(snapshotRef(a))
	ref.merge(snapshotRef(b))
	ref.merge(snapshotRef(c))
	if fmt.Sprint(ref.members()) != fmt.Sprint(left.Elements()) {
		t.Fatalf("naive ref %v != crdt %v", ref.members(), left.Elements())
	}
}

func TestMergeErrorsAtomicity(t *testing.T) {
	a, _ := NewReplica(1, 1000)

	if err := a.Merge(a); !errors.Is(err, ErrSelfMerge) {
		t.Fatalf("self merge want ErrSelfMerge, got %v", err)
	}
	var nilReplica *Replica
	if err := a.Merge(nilReplica); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil peer want ErrNilReplica, got %v", err)
	}
	if err := a.MergeChanges(nilReplica); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil incremental peer want ErrNilReplica, got %v", err)
	}

	// 合并引入的新元素数超过目标上限：整体拒绝，目标记录与合并位置不变。
	small, _ := NewReplica(5, 1)
	must(t, small.Add("k", 1))
	big, _ := NewReplica(6, 1000)
	must(t, big.Add("k", 2))
	must(t, big.Add("n1", 1))
	must(t, big.Add("n2", 1))

	sumBefore := small.Checksum()
	if err := small.Merge(big); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("want ErrLimitExceeded, got %v", err)
	}
	if small.Checksum() != sumBefore {
		t.Fatal("full merge must not partially apply when over limit")
	}
	if pos, _ := small.MergePosition(6); pos != 0 {
		t.Fatalf("merge position advanced on failed merge: %d", pos)
	}
	if err := small.MergeChanges(big); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("incremental want ErrLimitExceeded, got %v", err)
	}
	if small.Checksum() != sumBefore {
		t.Fatal("incremental merge must not partially apply when over limit")
	}
	dump(t, "small target after rejected merges", small)
}

// TestConcurrentMutationsAndMerges：多执行体并发增删/查询/双向合并，
// 互逆方向合并同时进行不得死锁；全部收敛后所有副本记录一致。
func TestConcurrentMutationsAndMerges(t *testing.T) {
	const n = 4
	reps := make([]*Replica, n)
	for i := range reps {
		reps[i], _ = mustReplica(int64(i + 1))
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := reps[i]
			for ts := int64(1); ts <= 200; ts++ {
				elem := fmt.Sprintf("e%d", ts%12)
				if ts%3 == 0 {
					_ = r.Remove(elem, ts)
				} else {
					_ = r.Add(elem, ts)
				}
				_ = r.Contains(elem)
				_ = r.Elements()
				// 与左右邻居做互逆方向合并，制造锁序竞争。
				_ = r.MergeChanges(reps[(i+1)%n])
				_ = reps[(i+1)%n].MergeChanges(r)
				_ = r.Checksum()
			}
		}()
	}
	wg.Wait()

	// 全连通反熵：两两整份合并直到收敛。
	for pass := 0; pass < n; pass++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i != j {
					must(t, reps[i].Merge(reps[j]))
				}
			}
		}
	}

	want := reps[0].Checksum()
	for i := 1; i < n; i++ {
		if reps[i].Checksum() != want {
			t.Fatalf("replica %d did not converge", i+1)
		}
	}

	// 与朴素参照一致：合并所有副本的最大时间戳。
	ref := naive{}
	for _, r := range reps {
		ref.merge(snapshotRef(r))
	}
	if fmt.Sprint(ref.members()) != fmt.Sprint(reps[0].Elements()) {
		t.Fatalf("naive ref %v != converged %v", ref.members(), reps[0].Elements())
	}
	t.Logf("[concurrent] all %d replicas converged; members=%v", n, reps[0].Elements())
	dump(t, "converged replica 1", reps[0])
}

func mustReplica(id int64) (*Replica, error) {
	return NewReplica(id, 1000)
}
