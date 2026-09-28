package lwwset

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

// dumpRecords 打印副本的全部记录，作为每步判定依据。
func dumpRecords(t *testing.T, r *Replica) {
	t.Helper()
	snap := r.Snapshot()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec := snap[k]
		t.Logf("    记录 %q: add=%d del=%d -> 存在=%v（删除不存在或严格小于添加时才存在，相等视为删除）",
			k, rec.AddTime, rec.DelTime, rec.Live())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
}

// TestEqualTimestampPrefersDelete 并列时间戳稳定偏向删除，且与到达顺序无关。
func TestEqualTimestampPrefersDelete(t *testing.T) {
	r1, err := NewReplica("r1")
	must(t, err)

	must(t, r1.Add("x", 5))
	t.Logf("输入 Add(x,5)")
	got, err := r1.Contains("x")
	must(t, err)
	if !got {
		t.Fatalf("add=5 后应存在")
	}

	must(t, r1.Remove("x", 5))
	t.Logf("输入 Remove(x,5)，与添加时间相等")
	dumpRecords(t, r1)
	got, err = r1.Contains("x")
	must(t, err)
	if got {
		t.Fatalf("时间戳相等时必须偏向删除")
	}

	// 调换到达顺序：先删后加，同一时间戳仍应判定为删除。
	r2, _ := NewReplica("r2")
	must(t, r2.Remove("y", 9))
	must(t, r2.Add("y", 9))
	t.Logf("输入 Remove(y,9) 后 Add(y,9)")
	dumpRecords(t, r2)
	got, err = r2.Contains("y")
	must(t, err)
	if got {
		t.Fatalf("先删后加且时间戳相等时仍必须删除")
	}

	must(t, r1.Add("x", 6))
	t.Logf("输入 Add(x,6)，严格大于删除时间后应复活")
	dumpRecords(t, r1)
	got, err = r1.Contains("x")
	must(t, err)
	if !got {
		t.Fatalf("add=6 > del=5 时应存在")
	}
}

// TestOutOfOrderArrival 乱序到达：旧时间戳不得把记录改小。
func TestOutOfOrderArrival(t *testing.T) {
	r, _ := NewReplica("r")

	must(t, r.Remove("a", 10))
	t.Logf("输入 Remove(a,10)（删除先到）")
	must(t, r.Add("a", 7))
	t.Logf("输入 Add(a,7)（较旧的添加后到）")
	dumpRecords(t, r)
	if got, _ := r.Contains("a"); got {
		t.Fatalf("add=7 < del=10，应为删除")
	}

	must(t, r.Add("a", 3))
	t.Logf("输入 Add(a,3)（更旧的添加，不得把 add 改小）")
	rec, exists, _ := r.Get("a")
	if !exists || rec.AddTime != 7 || rec.DelTime != 10 {
		t.Fatalf("旧添加到达后记录应保持 add=7 del=10，实际 %+v", rec)
	}

	must(t, r.Remove("a", 2))
	t.Logf("输入 Remove(a,2)（更旧的删除，不得把 del 改小）")
	rec, _, _ = r.Get("a")
	if rec.DelTime != 10 {
		t.Fatalf("旧删除到达后 del 应保持 10，实际 %d", rec.DelTime)
	}

	must(t, r.Add("a", 12))
	t.Logf("输入 Add(a,12)（更新的添加，元素复活）")
	dumpRecords(t, r)
	if got, _ := r.Contains("a"); !got {
		t.Fatalf("add=12 > del=10，应存在")
	}

	must(t, r.Remove("b", 100))
	t.Logf("输入 Remove(b,100)（对从未添加的元素先做删除，墓碑必须保留）")
	dumpRecords(t, r)
	if _, exists, _ := r.Get("b"); !exists {
		t.Fatalf("无添加先删除时也应留下墓碑记录")
	}
}

// TestMergeOnlyTargetChanges 合并只改目标副本。
func TestMergeOnlyTargetChanges(t *testing.T) {
	a, _ := NewReplica("a")
	b, _ := NewReplica("b")
	must(t, a.Add("x", 1))
	must(t, a.Add("y", 5))
	must(t, a.Remove("y", 6))
	must(t, b.Add("z", 2))
	must(t, b.Add("y", 7))

	before := a.Snapshot()
	must(t, b.MergeFrom(a))
	t.Logf("输入 b.MergeFrom(a) 后 b 的记录：")
	dumpRecords(t, b)

	if fmt.Sprint(a.Snapshot()) != fmt.Sprint(before) {
		t.Fatalf("合并不得修改来源副本 a")
	}
	rec, exists, _ := b.Get("x")
	if !exists || rec.AddTime != 1 || !rec.Live() {
		t.Fatalf("合并后 b 应获得 x 的添加记录")
	}
	rec, _, _ = b.Get("y")
	if rec.AddTime != 7 || rec.DelTime != 6 || !rec.Live() {
		t.Fatalf("合并后 y 应为 add=7 del=6 且存在，实际 %+v", rec)
	}
}

// replicaFromOps 按操作脚本重放构造一个新副本（用于合并律测试）。
type op struct {
	kind string
	elem string
	ts   int64
}

func replicaFromOps(t *testing.T, id string, ops []op) *Replica {
	t.Helper()
	r, err := NewReplica(id)
	must(t, err)
	for _, o := range ops {
		switch o.kind {
		case "add":
			must(t, r.Add(o.elem, o.ts))
		case "del":
			must(t, r.Remove(o.elem, o.ts))
		}
	}
	return r
}

// TestMergeLaws 幂等律、交换律、结合律。
func TestMergeLaws(t *testing.T) {
	opsA := []op{{"add", "x", 3}, {"del", "x", 3}, {"add", "y", 8}}
	opsB := []op{{"add", "x", 4}, {"add", "z", 2}, {"del", "z", 9}}
	opsC := []op{{"add", "y", 6}, {"del", "y", 7}, {"add", "w", 1}}

	// 幂等：连续合并两次结果一致。
	m1 := replicaFromOps(t, "m1", opsA)
	b1 := replicaFromOps(t, "b1", opsB)
	must(t, m1.MergeFrom(b1))
	snap1 := m1.Snapshot()
	must(t, m1.MergeFrom(b1))
	if fmt.Sprint(snap1) != fmt.Sprint(m1.Snapshot()) {
		t.Fatalf("合并不满足幂等律")
	}

	// 交换：A∪B == B∪A。
	ab := replicaFromOps(t, "ab", opsA)
	ba := replicaFromOps(t, "ba", opsB)
	must(t, ab.MergeFrom(replicaFromOps(t, "b", opsB)))
	must(t, ba.MergeFrom(replicaFromOps(t, "a", opsA)))
	if fmt.Sprint(ab.Snapshot()) != fmt.Sprint(ba.Snapshot()) {
		t.Fatalf("合并不满足交换律:\nA∪B=%v\nB∪A=%v", ab.Snapshot(), ba.Snapshot())
	}

	// 结合：(A∪B)∪C == A∪(B∪C)。
	left := replicaFromOps(t, "a1", opsA)
	must(t, left.MergeFrom(replicaFromOps(t, "b1", opsB)))
	must(t, left.MergeFrom(replicaFromOps(t, "c1", opsC)))
	right := replicaFromOps(t, "b2", opsB)
	must(t, right.MergeFrom(replicaFromOps(t, "c2", opsC)))
	aOnly := replicaFromOps(t, "a2", opsA)
	must(t, aOnly.MergeFrom(right))
	if fmt.Sprint(left.Snapshot()) != fmt.Sprint(aOnly.Snapshot()) {
		t.Fatalf("合并不满足结合律:\n左=%v\n右=%v", left.Snapshot(), aOnly.Snapshot())
	}
	t.Log("合并律全部满足，收敛记录：")
	dumpRecords(t, left)
}

// TestIncrementalEqualsFull 多次增量合并的最终结果必须与一次整份合并一致。
func TestIncrementalEqualsFull(t *testing.T) {
	src := replicaFromOps(t, "src", nil)
	full := replicaFromOps(t, "full", nil)
	inc := replicaFromOps(t, "inc", nil)

	stages := [][]op{
		{{"add", "a", 1}, {"add", "b", 2}},
		{{"del", "a", 1}, {"add", "c", 4}}, // a 时间戳相等被删
		{{"add", "a", 3}, {"del", "b", 10}, {"del", "c", 5}},
		{{"add", "b", 11}, {"del", "c", 2}}, // 旧删除不生效
	}
	for i, st := range stages {
		for _, o := range st {
			if o.kind == "add" {
				must(t, src.Add(o.elem, o.ts))
			} else {
				must(t, src.Remove(o.elem, o.ts))
			}
			t.Logf("阶段%d 输入 %s(%q,%d)", i+1, o.kind, o.elem, o.ts)
		}
		beforePos := inc.MergePosition("src")
		must(t, inc.MergeIncremental(src))
		afterPos := inc.MergePosition("src")
		if afterPos <= beforePos {
			t.Fatalf("增量合并后位置应前进: %d -> %d", beforePos, afterPos)
		}
		t.Logf("阶段%d 增量合并位置 src:%d，记录：", i+1, afterPos)
		dumpRecords(t, inc)
	}

	must(t, full.MergeFrom(src))
	t.Log("整份合并结果：")
	dumpRecords(t, full)
	t.Log("增量合并结果：")
	dumpRecords(t, inc)

	if fmt.Sprint(full.Snapshot()) != fmt.Sprint(inc.Snapshot()) {
		t.Fatalf("增量合并 %v 与整份合并 %v 不一致", inc.Snapshot(), full.Snapshot())
	}

	// 无新变更时增量合并是幂等的 no-op。
	pos := inc.MergePosition("src")
	must(t, inc.MergeIncremental(src))
	if inc.MergePosition("src") != pos {
		t.Fatalf("无新变更时合并位置不应变化")
	}
}

// TestInvalidInputsRejected 各类非法输入给出互不相同的错误，且失败不留痕。
func TestInvalidInputsRejected(t *testing.T) {
	ctorCases := []struct {
		name string
		do   func() error
		want error
	}{
		{"空副本编号", func() error { _, e := NewReplica(""); return e }, ErrEmptyReplicaID},
		{"零上限", func() error { _, e := NewReplicaWithLimit("x", 0); return e }, ErrInvalidMaxElements},
		{"负上限", func() error { _, e := NewReplicaWithLimit("x", -3); return e }, ErrInvalidMaxElements},
	}
	for _, c := range ctorCases {
		if !errors.Is(c.do(), c.want) {
			t.Errorf("%s: 错误原因不可区分", c.name)
		}
	}

	r, _ := NewReplicaWithLimit("r", 2)
	must(t, r.Add("a", 1))
	must(t, r.Remove("a", 2))
	must(t, r.Add("b", 1))
	mutCases := []struct {
		name string
		do   func() error
		want error
	}{
		{"空元素添加", func() error { return r.Add("", 1) }, ErrEmptyElement},
		{"空元素删除", func() error { return r.Remove("", 1) }, ErrEmptyElement},
		{"空元素查询", func() error { _, e := r.Contains(""); return e }, ErrEmptyElement},
		{"零时间戳", func() error { return r.Add("b", 0) }, ErrNonPositiveTimestamp},
		{"负时间戳", func() error { return r.Remove("b", -7) }, ErrNonPositiveTimestamp},
		{"元素数超限", func() error { return r.Add("c", 1) }, ErrTooManyElements},
		{"nil 整份合并", func() error { return r.MergeFrom(nil) }, ErrNilSource},
		{"nil 增量合并", func() error { return r.MergeIncremental(nil) }, ErrNilSource},
	}
	for _, c := range mutCases {
		snapBefore := r.Snapshot()
		seqBefore := r.Seq()
		err := c.do()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 期望错误 %v，实际 %v", c.name, c.want, err)
		}
		if fmt.Sprint(r.Snapshot()) != fmt.Sprint(snapBefore) {
			t.Errorf("%s: 拒绝后记录被改变", c.name)
		}
		if r.Seq() != seqBefore {
			t.Errorf("%s: 拒绝后变更序号被改变: %d -> %d", c.name, seqBefore, r.Seq())
		}
		if p := r.MergePosition("nobody"); p != 0 {
			t.Errorf("%s: 拒绝后合并位置被改变: %d", c.name, p)
		}
	}
	t.Log("全部非法输入拒绝后记录：")
	dumpRecords(t, r)

	// 超限的整份/增量合并必须整体拒绝，不得留下半成品。
	small, _ := NewReplicaWithLimit("small", 1)
	big, _ := NewReplicaWithLimit("big", 5)
	must(t, small.Add("s", 1))
	must(t, big.Add("u", 1))
	must(t, big.Add("v", 2))
	before := small.Snapshot()
	if !errors.Is(small.MergeFrom(big), ErrTooManyElements) {
		t.Fatalf("超限整份合并应被拒绝")
	}
	if fmt.Sprint(small.Snapshot()) != fmt.Sprint(before) {
		t.Fatalf("超限整份合并拒绝后记录被改变: %v", small.Snapshot())
	}
	if p := small.MergePosition("big"); p != 0 {
		t.Fatalf("超限整份合并拒绝后位置被推进: %d", p)
	}
	if !errors.Is(small.MergeIncremental(big), ErrTooManyElements) {
		t.Fatalf("超限增量合并应被拒绝")
	}
	if fmt.Sprint(small.Snapshot()) != fmt.Sprint(before) {
		t.Fatalf("超限增量合并拒绝后记录被改变: %v", small.Snapshot())
	}

	must(t, r.Check())
	must(t, small.Check())
	must(t, big.Check())
}

// TestConcurrentBidirectionalMerge 互逆方向合并并发进行不得死锁，最终各副本收敛。
func TestConcurrentBidirectionalMerge(t *testing.T) {
	a, _ := NewReplica("a")
	b, _ := NewReplica("b")
	var wg sync.WaitGroup
	stop := make(chan struct{})

	spawn := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
	}

	spawn(func() { _ = a.MergeFrom(b) })
	spawn(func() { _ = b.MergeFrom(a) })
	spawn(func() { _ = a.MergeIncremental(b) })
	spawn(func() { _ = b.MergeIncremental(a) })

	elems := []string{"e1", "e2", "e3", "e4"}
	for i := 0; i < 4; i++ {
		i := i
		spawn(func() {
			ts := int64(i + 1)
			r := a
			if i%2 == 0 {
				r = b
			}
			if err := r.Add(elems[i], ts); err != nil {
				t.Errorf("并发添加失败: %v", err)
			}
			if err := r.Remove(elems[i], ts+1); err != nil {
				t.Errorf("并发删除失败: %v", err)
			}
			if _, err := r.Contains(elems[i]); err != nil {
				t.Errorf("并发查询失败: %v", err)
			}
			if err := r.Check(); err != nil {
				t.Errorf("并发自检失败: %v", err)
			}
		})
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(stop)
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("互逆方向并发合并疑似死锁")
	}

	// 双向整份合并到达不动点，两副本记录必须完全一致。
	must(t, a.MergeFrom(b))
	must(t, b.MergeFrom(a))
	if fmt.Sprint(a.Snapshot()) != fmt.Sprint(b.Snapshot()) {
		t.Fatalf("收敛后记录不一致:\na=%v\nb=%v", a.Snapshot(), b.Snapshot())
	}
	if stringsJoin(a.Elements()) != stringsJoin(b.Elements()) {
		t.Fatalf("收敛后存在集合不一致: %v vs %v", a.Elements(), b.Elements())
	}
	must(t, a.Check())
	must(t, b.Check())
	t.Log("并发收敛后记录：")
	dumpRecords(t, a)
}

func stringsJoin(ss []string) string {
	return fmt.Sprint(ss)
}

// naiveRef 是不做任何并发/增量处理的朴素参照实现：每个元素两条时间取 max。
type naiveRef struct {
	m map[string]Record
}

func newNaiveRef() *naiveRef { return &naiveRef{m: map[string]Record{}} }

func (n *naiveRef) add(elem string, ts int64) {
	r := n.m[elem]
	if ts > r.AddTime {
		r.AddTime = ts
	}
	n.m[elem] = r
}

func (n *naiveRef) remove(elem string, ts int64) {
	r := n.m[elem]
	if ts > r.DelTime {
		r.DelTime = ts
	}
	n.m[elem] = r
}

func (n *naiveRef) merge(o *naiveRef) {
	for elem, rec := range o.m {
		cur := n.m[elem]
		n.m[elem] = mergeRecord(cur, rec)
	}
}

func (n *naiveRef) live() []string {
	out := make([]string, 0, len(n.m))
	for elem, rec := range n.m {
		if rec.Live() {
			out = append(out, elem)
		}
	}
	sort.Strings(out)
	return out
}

// TestAgainstNaiveReference 随机操作（含整份/增量合并）后与朴素参照逐元素一致。
func TestAgainstNaiveReference(t *testing.T) {
	const seed = 42
	rng := rand.New(rand.NewSource(seed))

	mk := func(id string) (*Replica, *naiveRef) {
		r, err := NewReplica(id)
		must(t, err)
		return r, newNaiveRef()
	}
	a, na := mk("a")
	b, nb := mk("b")
	c, nc := mk("c")
	reps := []*Replica{a, b, c}
	refs := []*naiveRef{na, nb, nc}

	for step := 0; step < 400; step++ {
		elem := fmt.Sprintf("e%d", rng.Intn(12))
		ts := int64(1 + rng.Intn(20))
		idx := rng.Intn(3)
		isAdd := rng.Intn(2) == 0
		if isAdd {
			must(t, reps[idx].Add(elem, ts))
			refs[idx].add(elem, ts)
			t.Logf("步骤%d 输入: 副本%d Add(%q,%d)", step, idx, elem, ts)
		} else {
			must(t, reps[idx].Remove(elem, ts))
			refs[idx].remove(elem, ts)
			t.Logf("步骤%d 输入: 副本%d Remove(%q,%d)", step, idx, elem, ts)
		}
		if rng.Intn(3) == 0 {
			from := rng.Intn(3)
			to := (from + 1 + rng.Intn(2)) % 3
			if rng.Intn(2) == 0 {
				must(t, reps[to].MergeFrom(reps[from]))
				t.Logf("步骤%d 输入: 副本%d MergeFrom(副本%d) 整份", step, to, from)
			} else {
				must(t, reps[to].MergeIncremental(reps[from]))
				t.Logf("步骤%d 输入: 副本%d MergeIncremental(副本%d) 增量", step, to, from)
			}
			refs[to].merge(refs[from])
		}
	}

	// 全部双向合并到不动点；朴素参照同样做全连接合并。
	for round := 0; round < 3; round++ {
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if i == j {
					continue
				}
				must(t, reps[i].MergeFrom(reps[j]))
				refs[i].merge(refs[j])
			}
		}
	}

	expected := na.live()
	for idx, r := range reps {
		must(t, r.Check())
		got := r.Elements()
		if stringsJoin(got) != stringsJoin(expected) {
			dumpRecords(t, r)
			t.Fatalf("副本%d 存在集合与朴素参照不一致: got=%v want=%v", idx, got, expected)
		}
		for elem, wantRec := range na.m {
			rec, exists, err := r.Get(elem)
			must(t, err)
			if !exists || rec != wantRec {
				t.Fatalf("副本%d 元素 %q 记录 %+v 与参照 %+v 不一致", idx, elem, rec, wantRec)
			}
		}
	}
	t.Logf("400 步随机操作 + 全连接合并后，3 个副本与朴素参照一致，存活元素: %v", expected)
	dumpRecords(t, a)
}
