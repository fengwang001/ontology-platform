package flexray

import (
	"fmt"
	"sync"
	"testing"
)

func errCodeOf(err error) ErrCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

func mustAssign(t *testing.T, a *Arbiter, id, base, rep int) {
	t.Helper()
	if err := a.Assign(id, base, rep); err != nil {
		t.Fatalf("Assign(%d,%d,%d): %v", id, base, rep, err)
	}
}

func mustPost(t *testing.T, a *Arbiter, id, length int, tag string) {
	t.Helper()
	if err := a.Post(id, length, tag); err != nil {
		t.Fatalf("Post(%d,%d,%q): %v", id, length, tag, err)
	}
}

func assertCycle(t *testing.T, label string, got, want CycleResult) {
	t.Helper()
	if got.C != want.C || got.Unused != want.Unused || got.EmptyFrame != want.EmptyFrame ||
		len(got.Sent) != len(want.Sent) {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
	for i := range want.Sent {
		if got.Sent[i] != want.Sent[i] {
			t.Fatalf("%s sent[%d] = %+v, want %+v (full %+v)",
				label, i, got.Sent[i], want.Sent[i], got.Sent)
		}
	}
}

// TestExampleFromSpec 复现题目给出的两轮周期示例。
func TestExampleFromSpec(t *testing.T) {
	a, err := New(3, 6, 4)
	if err != nil {
		t.Fatal(err)
	}
	mustAssign(t, a, 1, 0, 1)
	mustAssign(t, a, 2, 1, 2)
	mustAssign(t, a, 3, 0, 1)
	for id := 4; id <= 9; id++ {
		mustAssign(t, a, id, 0, 1)
	}
	mustPost(t, a, 1, 1, "a")
	mustPost(t, a, 2, 1, "b")
	mustPost(t, a, 4, 2, "m")
	mustPost(t, a, 5, 2, "n")
	mustPost(t, a, 7, 1, "p")
	mustPost(t, a, 9, 1, "q")

	r0 := a.Cycle()
	want0 := CycleResult{
		C:          0,
		Unused:     1,
		EmptyFrame: 1,
		Sent: []SendItem{
			{ID: 1, Tag: "a", Slot: 1},
			{ID: 4, Tag: "m", Start: 1},
			{ID: 5, Tag: "n", Start: 3},
		},
	}
	assertCycle(t, "cycle0", r0, want0)
	if tags, _ := a.Pending(7); len(tags) != 1 || tags[0] != "p" {
		t.Fatalf("frame7 pending = %v, want [p]", tags)
	}
	if tags, _ := a.Pending(9); len(tags) != 1 || tags[0] != "q" {
		t.Fatalf("frame9 pending = %v, want [q]", tags)
	}

	r1 := a.Cycle()
	want1 := CycleResult{
		C:          1,
		Unused:     2,
		EmptyFrame: 2,
		Sent: []SendItem{
			{ID: 2, Tag: "b", Slot: 2},
			{ID: 7, Tag: "p", Start: 4},
		},
	}
	assertCycle(t, "cycle1", r1, want1)
	if tags, _ := a.Pending(9); len(tags) != 1 || tags[0] != "q" {
		t.Fatalf("frame9 pending = %v, want [q]", tags)
	}

	st := a.Stats()
	if st.Sent != 5 || st.EmptyFrame != 3 || st.Overwrite != 0 {
		t.Fatalf("stats = %+v, want sent=5 empty=3 overwrite=0", st)
	}
}

// TestActivationRepBase 覆盖各 rep/base 的激活周期以及 63 后回绕。
func TestActivationRepBase(t *testing.T) {
	a, _ := New(4, 0, 0)
	mustAssign(t, a, 1, 0, 1)
	mustAssign(t, a, 2, 1, 2)
	mustAssign(t, a, 3, 2, 4)
	mustAssign(t, a, 4, 3, 8)
	active := map[int][]int{1: {}, 2: {}, 3: {}, 4: {}}
	for c := 0; c < 64; c++ {
		if a.C() != c {
			t.Fatalf("c = %d, want %d", a.C(), c)
		}
		mustPost(t, a, 1, 1, "x")
		mustPost(t, a, 2, 1, "x")
		mustPost(t, a, 3, 1, "x")
		mustPost(t, a, 4, 1, "x")
		r := a.Cycle()
		for _, s := range r.Sent {
			active[s.ID] = append(active[s.ID], c)
		}
	}
	check := func(id, rep, base int) {
		var want []int
		for c := 0; c < 64; c++ {
			if c%rep == base {
				want = append(want, c)
			}
		}
		if fmt.Sprint(active[id]) != fmt.Sprint(want) {
			t.Fatalf("frame %d active cycles %v, want %v", id, active[id], want)
		}
	}
	check(1, 1, 0)
	check(2, 2, 1)
	check(3, 4, 2)
	check(4, 8, 3)

	mustPost(t, a, 1, 1, "x")
	r := a.Cycle()
	if r.C != 0 || len(r.Sent) != 1 || r.Sent[0] != (SendItem{ID: 1, Tag: "x", Slot: 1}) {
		t.Fatalf("wrap cycle = %+v", r)
	}
}

// TestStaticOverwrite 静态缓冲覆盖与未激活消息保留到下个激活周期。
func TestStaticOverwrite(t *testing.T) {
	a, _ := New(2, 0, 0)
	mustAssign(t, a, 1, 1, 2)
	mustAssign(t, a, 2, 0, 1)

	mustPost(t, a, 1, 1, "old")
	mustPost(t, a, 1, 1, "new")
	mustPost(t, a, 1, 1, "new2")
	if st := a.Stats(); st.Overwrite != 2 {
		t.Fatalf("overwrite = %d, want 2", st.Overwrite)
	}

	r0 := a.Cycle()
	if len(r0.Sent) != 0 || r0.Unused != 1 || r0.EmptyFrame != 1 {
		t.Fatalf("cycle0 = %+v", r0)
	}
	r1 := a.Cycle()
	if r1.Unused != 1 || len(r1.Sent) != 1 ||
		r1.Sent[0] != (SendItem{ID: 1, Tag: "new2", Slot: 1}) {
		t.Fatalf("cycle1 = %+v", r1)
	}

	if err := a.Post(2, 9999, "z"); err != nil {
		t.Fatalf("static post ignores length, got %v", err)
	}
}

// TestUnusedSlotsMerge u 并入 N：长帧在 N=8 放得下，在 N=6 推迟。
func TestUnusedSlotsMerge(t *testing.T) {
	a, _ := New(3, 6, 6)
	for id := 1; id <= 9; id++ {
		mustAssign(t, a, id, 0, 1)
	}
	mustPost(t, a, 6, 5, "long")
	mustPost(t, a, 1, 1, "s")
	r := a.Cycle()
	if r.Unused != 2 || r.EmptyFrame != 2 {
		t.Fatalf("u = %d empty = %d, want 2/2", r.Unused, r.EmptyFrame)
	}
	if len(r.Sent) != 2 || r.Sent[1] != (SendItem{ID: 6, Tag: "long", Start: 3}) {
		t.Fatalf("sent = %+v, want frame6 start 3", r.Sent)
	}

	b, _ := New(3, 6, 6)
	for id := 1; id <= 9; id++ {
		mustAssign(t, b, id, 0, 1)
	}
	for id := 1; id <= 3; id++ {
		mustPost(t, b, id, 1, "s")
	}
	mustPost(t, b, 6, 5, "long")
	r = b.Cycle()
	if r.Unused != 0 || len(r.Sent) != 3 {
		t.Fatalf("sent = %+v u=%d, want 3 static only", r.Sent, r.Unused)
	}
	tags, _ := b.Pending(6)
	if len(tags) != 1 || tags[0] != "long" {
		t.Fatalf("frame6 pending = %v, want retained", tags)
	}
}

// TestBoundaryNAndLt i+L-1 恰等于 N 允许、大 1 推迟；i 恰等于 Lt 允许、大 1 挡下。
func TestBoundaryNAndLt(t *testing.T) {
	a, _ := New(1, 5, 5)
	mustAssign(t, a, 1, 0, 1)
	mustPost(t, a, 1, 1, "s")
	mustAssign(t, a, 3, 0, 1)
	mustPost(t, a, 3, 4, "eq")
	r := a.Cycle()
	if r.Sent[len(r.Sent)-1] != (SendItem{ID: 3, Tag: "eq", Start: 2}) {
		t.Fatalf("boundary N equal: %+v", r.Sent)
	}

	b, _ := New(1, 5, 5)
	mustAssign(t, b, 1, 0, 1)
	mustPost(t, b, 1, 1, "s")
	mustAssign(t, b, 4, 0, 1)
	mustPost(t, b, 4, 4, "big")
	r = b.Cycle()
	if len(r.Sent) != 1 {
		t.Fatalf("should defer beyond N, sent = %+v", r.Sent)
	}
	if tags, _ := b.Pending(4); len(tags) != 1 || tags[0] != "big" {
		t.Fatalf("big msg should remain, pending = %v", tags)
	}

	d, _ := New(1, 6, 3)
	mustAssign(t, d, 1, 0, 1)
	mustPost(t, d, 1, 1, "s")
	mustAssign(t, d, 4, 0, 1)
	mustPost(t, d, 4, 1, "at")
	r = d.Cycle()
	if r.Sent[len(r.Sent)-1] != (SendItem{ID: 4, Tag: "at", Start: 3}) {
		t.Fatalf("i==Lt should send: %+v", r.Sent)
	}

	e, _ := New(1, 6, 2)
	mustAssign(t, e, 1, 0, 1)
	mustPost(t, e, 1, 1, "s")
	mustAssign(t, e, 4, 0, 1)
	mustPost(t, e, 4, 1, "late")
	r = e.Cycle()
	if len(r.Sent) != 1 {
		t.Fatalf("Lt should block, sent = %+v", r.Sent)
	}
	if tags, _ := e.Pending(4); len(tags) != 1 || tags[0] != "late" {
		t.Fatalf("blocked msg should remain, pending = %v", tags)
	}
}

// TestKAdvancesWithI 空过时 k 同步前进；长帧发送后 k 只加 1，占用区间不重叠。
func TestKAdvancesWithI(t *testing.T) {
	a, _ := New(1, 8, 8)
	mustAssign(t, a, 1, 0, 1)
	mustPost(t, a, 1, 1, "s")
	mustAssign(t, a, 2, 0, 1)
	mustPost(t, a, 2, 3, "long")
	mustAssign(t, a, 3, 0, 1)
	mustAssign(t, a, 4, 0, 1)
	mustPost(t, a, 4, 1, "after")
	r := a.Cycle()
	want := []SendItem{
		{ID: 1, Tag: "s", Slot: 1},
		{ID: 2, Tag: "long", Start: 1},
		{ID: 4, Tag: "after", Start: 5},
	}
	if fmt.Sprint(r.Sent) != fmt.Sprint(want) {
		t.Fatalf("sent = %+v, want %+v", r.Sent, want)
	}

	b, _ := New(1, 6, 6)
	mustAssign(t, b, 1, 0, 1)
	mustPost(t, b, 1, 1, "s")
	mustAssign(t, b, 2, 0, 1)
	mustPost(t, b, 2, 1, "m1")
	mustPost(t, b, 2, 1, "m2")
	r = b.Cycle()
	if len(r.Sent) != 2 || r.Sent[1].Tag != "m1" {
		t.Fatalf("only head per cycle, got %+v", r.Sent)
	}
	tags, _ := b.Pending(2)
	if fmt.Sprint(tags) != "[m2]" {
		t.Fatalf("pending = %v, want [m2]", tags)
	}
}

// TestDegenerate Nm=0 与 Lt=0 的退化情形。
func TestDegenerate(t *testing.T) {
	a, _ := New(2, 0, 0)
	mustAssign(t, a, 1, 0, 1)
	r := a.Cycle()
	// 仅帧 1 登记且激活为空 -> u=1；帧 2 未登记，静默。
	if r.Unused != 1 || r.EmptyFrame != 1 || len(r.Sent) != 0 {
		t.Fatalf("Nm=0 cycle = %+v", r)
	}
	if err := a.Assign(3, 0, 1); errCodeOf(err) != ErrInvalidID {
		t.Fatalf("Assign id=3 with Ns=2: code=%v", errCodeOf(err))
	}

	b, _ := New(1, 3, 0)
	mustAssign(t, b, 1, 0, 1)
	mustPost(t, b, 1, 1, "s")
	mustAssign(t, b, 2, 0, 1)
	mustPost(t, b, 2, 1, "d")
	r = b.Cycle()
	if len(r.Sent) != 1 {
		t.Fatalf("Lt=0 must block dynamic, got %+v", r.Sent)
	}
	if tags, _ := b.Pending(2); len(tags) != 1 {
		t.Fatalf("Lt=0 pending = %v", tags)
	}

	c, _ := New(1, 3, 0)
	mustAssign(t, c, 1, 0, 1)
	mustAssign(t, c, 2, 0, 1)
	mustPost(t, c, 2, 1, "d")
	r = c.Cycle()
	if r.Unused != 1 || len(r.Sent) != 0 {
		t.Fatalf("Lt=0 with u=1: %+v", r)
	}
}

// TestRejectionOrder 拒绝原因按规定顺序只报第一个，且拒绝不改变状态。
func TestRejectionOrder(t *testing.T) {
	a, _ := New(2, 3, 2)
	mustAssign(t, a, 1, 0, 1)

	for _, args := range [][3]int{{0, 3, 2}, {65, 3, 2}, {1, -1, 0}, {1, 257, 0}, {1, 3, 4}, {1, 3, -1}} {
		if _, err := New(args[0], args[1], args[2]); errCodeOf(err) != ErrInvalidParam {
			t.Fatalf("New%v code=%v", args, errCodeOf(err))
		}
	}

	if err := a.Assign(99, 5, 2); errCodeOf(err) != ErrInvalidParam {
		t.Fatalf("bad base+bad id code=%v, want invalid param", errCodeOf(err))
	}
	if err := a.Assign(99, 0, 3); errCodeOf(err) != ErrInvalidParam {
		t.Fatalf("bad rep+bad id code=%v, want invalid param", errCodeOf(err))
	}
	if err := a.Assign(99, 0, 1); errCodeOf(err) != ErrInvalidID {
		t.Fatalf("bad id code=%v", errCodeOf(err))
	}
	if err := a.Assign(1, 0, 1); errCodeOf(err) != ErrDuplicate {
		t.Fatalf("duplicate code=%v", errCodeOf(err))
	}

	if err := a.Post(99, 0, "x"); errCodeOf(err) != ErrInvalidID {
		t.Fatalf("post bad id code=%v", errCodeOf(err))
	}
	if err := a.Post(2, 1, "x"); errCodeOf(err) != ErrNotAssigned {
		t.Fatalf("post unassigned code=%v", errCodeOf(err))
	}

	mustAssign(t, a, 3, 0, 1)
	if err := a.Post(3, 0, "x"); errCodeOf(err) != ErrInvalidParam {
		t.Fatalf("L=0 code=%v", errCodeOf(err))
	}
	if err := a.Post(3, 4, "x"); errCodeOf(err) != ErrInvalidParam {
		t.Fatalf("L>Nm code=%v", errCodeOf(err))
	}
	for i := 0; i < 8; i++ {
		mustPost(t, a, 3, 1, "q")
	}
	if err := a.Post(3, 1, "ninth"); errCodeOf(err) != ErrQueueFull {
		t.Fatalf("queue full code=%v", errCodeOf(err))
	}

	// 被拒绝的操作不改变状态：队列仍为 8 条，c 不变，统计不变。
	tags, _ := a.Pending(3)
	if len(tags) != 8 {
		t.Fatalf("queue len after rejection = %d", len(tags))
	}
	if a.C() != 0 || a.Stats().Overwrite != 0 {
		t.Fatalf("state changed by rejected ops: c=%d stats=%+v", a.C(), a.Stats())
	}

	// 静态缓冲被覆盖时计数 +1。
	mustPost(t, a, 1, 1, "v1")
	mustPost(t, a, 1, 1, "v2")
	if a.Stats().Overwrite != 1 {
		t.Fatalf("overwrite = %d, want 1", a.Stats().Overwrite)
	}

	if _, err := a.Pending(0); errCodeOf(err) != ErrInvalidID {
		t.Fatalf("Pending bad id code=%v", errCodeOf(err))
	}
}

// TestNoOverlap 多帧动态区间互不重叠且落在 1..N 之内。
func TestNoOverlap(t *testing.T) {
	a, _ := New(2, 10, 10)
	for id := 1; id <= 12; id++ {
		mustAssign(t, a, id, 0, 1)
	}
	mustPost(t, a, 3, 2, "a")
	mustPost(t, a, 5, 3, "b")
	mustPost(t, a, 6, 1, "c")
	mustPost(t, a, 12, 2, "d")
	r := a.Cycle()
	n := 12 // u=2（两个静态帧均空），N=10+2
	lengthOf := map[int]int{3: 2, 5: 3, 6: 1, 12: 2}
	wantStarts := map[int]int{3: 1, 5: 4, 6: 7}
	busy := make([]bool, n+1)
	for _, s := range r.Sent {
		if s.ID <= 2 {
			continue
		}
		l, ok := lengthOf[s.ID]
		if !ok {
			t.Fatalf("unexpected send: %+v", s)
		}
		if s.Start != wantStarts[s.ID] {
			t.Fatalf("frame %d start = %d, want %d", s.ID, s.Start, wantStarts[s.ID])
		}
		for j := s.Start; j <= s.Start+l-1; j++ {
			if j < 1 || j > n || busy[j] {
				t.Fatalf("frame %d overlaps or out of range at %d", s.ID, j)
			}
			busy[j] = true
		}
	}
	// k 每步只 +1：帧 12 在 i=12 时 k 才到 11，帧 12 永远轮不到，d 留存。
	if tags, _ := a.Pending(12); fmt.Sprint(tags) != "[d]" {
		t.Fatalf("frame12 pending = %v, want [d]", tags)
	}
}

// TestConcurrent 并发调用结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	a, _ := New(4, 8, 6)
	for id := 1; id <= 12; id++ {
		mustAssign(t, a, id, id%2, 2)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch i % 5 {
				case 0:
					a.Cycle()
				case 1:
					a.Post(1+(g+i)%4, 1, "s")
				case 2:
					a.Post(5+(g+i)%8, 1+((g+i)%8), "d")
				case 3:
					a.Pending(5 + (g+i)%8)
				case 4:
					a.Stats()
				}
			}
		}(g)
	}
	wg.Wait()

	for id := 5; id <= 12; id++ {
		if tags, _ := a.Pending(id); len(tags) > 8 {
			t.Fatalf("queue %d len %d > 8", id, len(tags))
		}
	}
}
