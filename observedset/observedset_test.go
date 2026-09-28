package observedset

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// testLogger 记录每步输入、集合内容与判定依据，供 t.Log 输出。
// 可被多个副本、多个 goroutine 并发使用。
type testLogger struct {
	mu sync.Mutex
	sb strings.Builder
}

func (l *testLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func newTestReplica(t *testing.T, id int, opts ...Option) *Replica {
	t.Helper()
	r, err := NewReplica(id, opts...)
	if err != nil {
		t.Fatalf("NewReplica(%d): %v", id, err)
	}
	return r
}

func logOpt() (Option, *testLogger) {
	l := &testLogger{}
	return WithLogger(l), l
}

func dumpLog(t *testing.T, l *testLogger) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Log("\n操作与判定日志:\n" + l.sb.String())
}

// naiveReplica 是朴素参照 OR-Set，用于结果交叉校验。
type naiveReplica struct {
	id         int
	counter    int64
	adds       map[Tag]string
	tombstones map[Tag]struct{}
}

func newNaive(id int) *naiveReplica {
	return &naiveReplica{id: id, adds: map[Tag]string{}, tombstones: map[Tag]struct{}{}}
}

func (n *naiveReplica) add(e string) Tag {
	n.counter++
	t := Tag{ReplicaID: n.id, Counter: n.counter}
	n.adds[t] = e
	return t
}

func (n *naiveReplica) remove(e string) {
	for t, v := range n.adds {
		if v == e {
			if _, dead := n.tombstones[t]; !dead {
				n.tombstones[t] = struct{}{}
			}
		}
	}
}

func (n *naiveReplica) elements() []string {
	set := map[string]struct{}{}
	for t, v := range n.adds {
		if _, dead := n.tombstones[t]; !dead {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func (n *naiveReplica) mergeFrom(o *naiveReplica) {
	for t, v := range o.adds {
		n.adds[t] = v
	}
	for t := range o.tombstones {
		n.tombstones[t] = struct{}{}
	}
}

func assertMatchesNaive(t *testing.T, r *Replica, n *naiveReplica) {
	t.Helper()
	got := r.Elements()
	want := n.elements()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("与朴素参照不一致: got=%v want=%v", got, want)
	}
	if err := r.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 1. 增删交错，逐步与朴素参照对齐。
func TestAddRemoveInterleaved(t *testing.T) {
	opt, l := logOpt()
	r := newTestReplica(t, 0, opt)
	n := newNaive(0)

	r.Add("a")
	n.add("a")
	r.Add("b")
	n.add("b")
	assertMatchesNaive(t, r, n)

	r.Remove("a")
	n.remove("a")
	assertMatchesNaive(t, r, n)

	r.Add("a") // 重新加入产生新标签，新标签不在墓碑中
	n.add("a")
	assertMatchesNaive(t, r, n)

	r.Remove("b")
	n.remove("b")
	r.Add("c")
	n.add("c")
	r.Remove("a")
	n.remove("a")
	assertMatchesNaive(t, r, n)

	r.Add("a")
	n.add("a")
	assertMatchesNaive(t, r, n)

	if tags := r.Explain("a"); len(tags) != 1 {
		t.Fatalf("Explain(a) 应有 1 个存活标签, got %v", tags)
	}
	dumpLog(t, l)
}

// 3. 合并律：幂等律、交换律、结合律，并与朴素参照交叉验证。
func TestMergeLaws(t *testing.T) {
	build := func(id int, elems ...string) (*Replica, *naiveReplica) {
		r := newTestReplica(t, id)
		n := newNaive(id)
		for _, e := range elems {
			r.Add(e)
			n.add(e)
		}
		return r, n
	}

	// 幂等：自己合并自己是合法空操作
	r, n := build(7, "a", "b")
	before := append([]string(nil), r.Elements()...)
	if err := MergeInto(r, r); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Elements(), before) {
		t.Fatal("自合并不应改变状态")
	}
	assertMatchesNaive(t, r, n)

	// 交换律：merge(A,B) 与 merge(B,A) 的合并结果等价
	a1, na1 := build(0, "x", "y")
	b1, nb1 := build(1, "y", "z")
	a2, na2 := build(0, "x", "y")
	b2, nb2 := build(1, "y", "z")
	if err := MergeInto(a1, b1); err != nil {
		t.Fatal(err)
	}
	na1.mergeFrom(nb1)
	if err := MergeInto(b2, a2); err != nil {
		t.Fatal(err)
	}
	nb2.mergeFrom(na2)
	if !reflect.DeepEqual(a1.Elements(), b2.Elements()) {
		t.Fatalf("交换律失败: %v vs %v", a1.Elements(), b2.Elements())
	}
	assertMatchesNaive(t, a1, na1)
	assertMatchesNaive(t, b2, nb2)

	// 结合律：(A∪B)∪C == A∪(B∪C)
	ca, nca := build(2, "p", "x")
	cb, ncb := build(3, "x", "q")
	cc, ncc := build(4, "q", "r")

	left, nleft := newTestReplica(t, 5), newNaive(5)
	if err := MergeInto(left, ca); err != nil {
		t.Fatal(err)
	}
	nleft.mergeFrom(nca)
	if err := MergeInto(left, cb); err != nil {
		t.Fatal(err)
	}
	nleft.mergeFrom(ncb)
	if err := MergeInto(left, cc); err != nil {
		t.Fatal(err)
	}
	nleft.mergeFrom(ncc)

	mid, nmid := newTestReplica(t, 6), newNaive(6)
	if err := MergeInto(mid, cb); err != nil {
		t.Fatal(err)
	}
	nmid.mergeFrom(ncb)
	if err := MergeInto(mid, cc); err != nil {
		t.Fatal(err)
	}
	nmid.mergeFrom(ncc)
	right, nright := newTestReplica(t, 7), newNaive(7)
	if err := MergeInto(right, ca); err != nil {
		t.Fatal(err)
	}
	nright.mergeFrom(nca)
	if err := MergeInto(right, mid); err != nil {
		t.Fatal(err)
	}
	nright.mergeFrom(nmid)

	if !reflect.DeepEqual(left.Elements(), right.Elements()) {
		t.Fatalf("结合律失败: %v vs %v", left.Elements(), right.Elements())
	}
	assertMatchesNaive(t, left, nleft)
	assertMatchesNaive(t, right, nright)
}

// 4. 非法输入：互不相同的错误类别，失败不留痕，标签计数不跳号。
func TestInvalidInputs(t *testing.T) {
	if _, err := NewReplica(-1); !errors.Is(err, ErrInvalidReplicaID) {
		t.Fatalf("负编号应拒绝, got %v", err)
	}

	r := newTestReplica(t, 0)

	// 任何成功添加之前的多次拒绝
	if _, err := r.Add(""); !errors.Is(err, ErrEmptyElement) {
		t.Fatalf("空串 Add 应返回 ErrEmptyElement, got %v", err)
	}
	if err := r.Remove(""); !errors.Is(err, ErrEmptyElement) {
		t.Fatalf("空串 Remove 应返回 ErrEmptyElement, got %v", err)
	}
	if err := r.Remove("ghost"); !errors.Is(err, ErrElementNotFound) {
		t.Fatalf("删除从未存在的元素应返回 ErrElementNotFound, got %v", err)
	}

	// 全部被拒之后第一次成功 Add：标签必须仍是 r0#1，计数不跳号
	first, err := r.Add("b")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tag{ReplicaID: 0, Counter: 1}); first != want {
		t.Fatalf("被拒后首个成功标签应为 %v, got %v（失败不留痕）", want, first)
	}

	r.Add("a")
	if err := r.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("a"); !errors.Is(err, ErrElementNotFound) {
		t.Fatalf("删除已删除元素应返回 ErrElementNotFound, got %v", err)
	}

	// 删除不存在元素被拒后，再成功 Add 的标签必须紧邻上一次成功分配（r0#2）
	tag, err := r.Add("c")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tag{ReplicaID: 0, Counter: 3}); tag != want {
		t.Fatalf("被拒不应消耗计数, got %v want %v", tag, want)
	}

	if err := MergeInto(nil, r); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil 目标应返回 ErrNilReplica, got %v", err)
	}
	if err := MergeInto(r, nil); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil 来源应返回 ErrNilReplica, got %v", err)
	}
	if _, err := (*Replica)(nil).Add("x"); !errors.Is(err, ErrNilReplica) {
		t.Fatalf("nil Add 应返回 ErrNilReplica, got %v", err)
	}

	// 超限：拒绝且状态（含标签计数）不变
	limited := newTestReplica(t, 5, WithAddLimit(2))
	limited.Add("one")
	limited.Add("two")
	snapshot := append([]string(nil), limited.Elements()...)
	if _, err := limited.Add("three"); !errors.Is(err, ErrTooManyAdds) {
		t.Fatalf("超限应返回 ErrTooManyAdds, got %v", err)
	}
	if !reflect.DeepEqual(limited.Elements(), snapshot) {
		t.Fatal("超限拒绝不得改变状态")
	}
	if _, err := limited.Add("four"); !errors.Is(err, ErrTooManyAdds) {
		t.Fatalf("再次超限仍应拒绝, got %v", err)
	}
	if err := limited.Check(); err != nil {
		t.Fatal(err)
	}

	// 五类错误必须两两可区分
	errs := []error{ErrNilReplica, ErrInvalidReplicaID, ErrEmptyElement, ErrElementNotFound, ErrTooManyAdds}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("错误类别必须互不相同: %v 与 %v 不可区分", errs[i], errs[j])
			}
		}
	}
}

// 5. 并发：增删、查询、自检与互逆方向合并同时进行，
// 不死锁；全部完成后各副本元素集合收敛一致。
func TestConcurrentBidirectionalMergeNoDeadlock(t *testing.T) {
	opt, l := logOpt()
	const iterations = 300
	r0 := newTestReplica(t, 0, opt)
	r1 := newTestReplica(t, 1, opt)

	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		for i := 0; i < iterations; i++ {
			e := fmt.Sprintf("a%d", i%5)
			if i%4 == 0 {
				if err := r0.Remove(e); err != nil {
					r0.Add(e)
				}
			} else {
				r0.Add(e)
			}
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; i < iterations; i++ {
			e := fmt.Sprintf("b%d", i%5)
			if i%3 == 0 {
				if err := r1.Remove(e); err != nil {
					r1.Add(e)
				}
			} else {
				r1.Add(e)
			}
		}
	}()

	stop := make(chan struct{})
	var mergers sync.WaitGroup
	mergers.Add(2)
	mergeLoop := func(dst, src *Replica, name string) {
		defer mergers.Done()
		for {
			if err := MergeInto(dst, src); err != nil {
				t.Errorf("%s: %v", name, err)
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}
	go mergeLoop(r0, r1, "merge 0<-1")
	go mergeLoop(r1, r0, "merge 1<-0")

	// 读者：并发判存在、查元素、自检
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r0.Contains("a1")
			r1.Elements()
			if err := r0.Check(); err != nil {
				t.Errorf("check r0: %v", err)
				return
			}
			if err := r1.Check(); err != nil {
				t.Errorf("check r1: %v", err)
				return
			}
		}
	}()

	writers.Wait()
	close(stop)
	mergers.Wait()
	readers.Wait()

	// 最终双向合并保证收敛
	if err := MergeInto(r0, r1); err != nil {
		t.Fatal(err)
	}
	if err := MergeInto(r1, r0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r0.Elements(), r1.Elements()) {
		t.Fatalf("合并后应收敛一致: %v vs %v", r0.Elements(), r1.Elements())
	}
	if err := r0.Check(); err != nil {
		t.Fatal(err)
	}
	if err := r1.Check(); err != nil {
		t.Fatal(err)
	}
	t.Logf("收敛集合: %v", r0.Elements())
	dumpLog(t, l)
}

// 6. 并发的“后加入者胜出”：并发增删 churn 结束并同步后，
// 再由 r1 做一次确定的“最后加入”。任何删除都不可能墓碑
// 这个更晚产生的标签，因此收敛结果确定包含 k 且可复现。
func TestConcurrentAddWinsReproducible(t *testing.T) {
	const rounds = 100
	run := func() []string {
		r0 := newTestReplica(t, 0)
		r1 := newTestReplica(t, 1)
		n0, n1 := newNaive(0), newNaive(1)
		r0.Add("k")
		n0.add("k")
		MergeInto(r1, r0)
		n1.mergeFrom(n0)

		// 阶段一：两个副本各自并发 churn，期间持续双向合并
		stop := make(chan struct{})
		var churn sync.WaitGroup
		churn.Add(2)
		go func() {
			defer churn.Done()
			for i := 0; i < rounds; i++ {
				if err := r0.Remove("k"); err != nil {
					r0.Add("k")
				}
			}
		}()
		go func() {
			defer churn.Done()
			for i := 0; i < rounds; i++ {
				r1.Add("k")
			}
		}()
		var syncers sync.WaitGroup
		syncers.Add(2)
		go func() {
			defer syncers.Done()
			for {
				MergeInto(r0, r1)
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
		go func() {
			defer syncers.Done()
			for {
				MergeInto(r1, r0)
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
		churn.Wait()
		close(stop)
		syncers.Wait()

		// 阶段二：确定的“最后加入者”——r1 在所有删除都已结束后 Add
		// （这是 add-wins 语义的关键场景：删除墓碑不到未来的标签）
		MergeInto(r0, r1)
		MergeInto(r1, r0)
		lastTag, _ := r1.Add("k")
		n1.add("k")
		MergeInto(r0, r1)
		MergeInto(r1, r0)

		e0, e1 := r0.Elements(), r1.Elements()
		if !reflect.DeepEqual(e0, e1) {
			t.Fatalf("两副本应一致: %v vs %v", e0, e1)
		}
		if !r0.Contains("k") {
			t.Fatalf("后加入者应胜出: 最后加入的标签存活, got %v", e0)
		}
		if live := r0.Explain("k"); len(live) == 0 || live[len(live)-1] != lastTag {
			t.Fatalf("最后加入的标签 %v 必须存活, live=%v", lastTag, live)
		}
		if err := r0.Check(); err != nil {
			t.Fatal(err)
		}
		// churn 阶段并发交错不可逐字重放；最终元素集合与朴素参照
		// “初始 Add + 最后 Add 均存活”的 add-wins 结论一致（[k]）。
		n0.mergeFrom(n1)
		if want := n0.elements(); !reflect.DeepEqual(e0, want) {
			t.Fatalf("与朴素 add-wins 结论不一致: got %v want %v", e0, want)
		}
		return e0
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同样的最终操作下结果应可复现: %v vs %v", first, second)
	}
}

// 2. 后加入者胜出：删除只墓碑当时存活的标签。
func TestAddWinsSemantics(t *testing.T) {
	opt, l := logOpt()
	r0 := newTestReplica(t, 0, opt)
	r1 := newTestReplica(t, 1, opt)
	n0, n1 := newNaive(0), newNaive(1)

	tag0, _ := r0.Add("x")
	n0.add("x")
	if err := MergeInto(r1, r0); err != nil {
		t.Fatal(err)
	}
	n1.mergeFrom(n0)

	// r1 删除 x（仅墓碑 tag0），r0 并发地再次 Add x（产生 tag1）
	if err := r1.Remove("x"); err != nil {
		t.Fatal(err)
	}
	n1.remove("x")
	tag1, _ := r0.Add("x")
	n0.add("x")

	if err := MergeInto(r0, r1); err != nil {
		t.Fatal(err)
	}
	n0.mergeFrom(n1)
	if err := MergeInto(r1, r0); err != nil {
		t.Fatal(err)
	}
	n1.mergeFrom(n0)

	if !r0.Contains("x") || !r1.Contains("x") {
		t.Fatal("后加入者应胜出: 两个副本都应包含 x")
	}
	assertMatchesNaive(t, r0, n0)
	assertMatchesNaive(t, r1, n1)
	if tags := r0.Explain("x"); !reflect.DeepEqual(tags, []Tag{tag1}) {
		t.Fatalf("x 的存活标签应为 %v, got %v (早先标签 %v 应已入墓碑)", tag1, tags, tag0)
	}
	dumpLog(t, l)
}
