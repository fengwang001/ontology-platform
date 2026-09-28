package intervaljoin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	// 默认静默内部日志，需要断言日志内容的用例自行重定向。
	SetLogger(io.Discard)
	os.Exit(m.Run())
}

func wantErrCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error code %s, got nil", want)
	}
	var je *JoinError
	if !errors.As(err, &je) {
		t.Fatalf("want *JoinError, got %T: %v", err, err)
	}
	if je.Code != want {
		t.Fatalf("want error code %s, got %s (%v)", want, je.Code, err)
	}
}

func TestNewInvalidConfig(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		c, err := New(Config{MaxRetainedPerSide: n})
		wantErrCode(t, err, CodeInvalidParameter)
		if !strings.Contains(err.Error(), string(CodeInvalidParameter)) {
			t.Fatalf("error text %q missing code", err.Error())
		}
		if c != nil {
			t.Fatalf("New with limit %d returned non-nil connector", n)
		}
	}
}

func TestProcessInvalidInputs(t *testing.T) {
	c, err := New(Config{MaxRetainedPerSide: 8})
	if err != nil {
		t.Fatal(err)
	}
	good := &Event{Key: "k", Lo: 1, Hi: 2}

	if _, err := c.Process(Side(9), good); err == nil {
		t.Fatal("invalid side accepted")
	} else {
		wantErrCode(t, err, CodeInvalidParameter)
	}
	if _, err := c.Process(SideLeft, nil); err == nil {
		t.Fatal("nil event accepted")
	} else {
		wantErrCode(t, err, CodeInvalidParameter)
	}
	if _, err := c.Process(SideLeft, &Event{Key: "k", Lo: 5, Hi: 4}); err == nil {
		t.Fatal("Lo>Hi accepted")
	} else {
		wantErrCode(t, err, CodeInvalidParameter)
	}
	if _, err := c.Process(SideLeft, &Event{Key: "", Lo: 1, Hi: 2}); err == nil {
		t.Fatal("empty key accepted")
	} else {
		wantErrCode(t, err, CodeEmptyKey)
	}

	// 全部拒绝后状态必须仍为空。
	if ps := c.Pairs(); len(ps) != 0 {
		t.Fatalf("rejected ops produced pairs: %v", ps)
	}
	if rs := c.Retained(SideLeft); len(rs) != 0 {
		t.Fatalf("rejected ops retained events: %v", rs)
	}
	if _, ok := c.Watermark(SideLeft); ok {
		t.Fatal("rejected ops advanced watermark")
	}

	// 非法侧的只读访问器返回零值，不 panic。
	if rs := c.Retained(Side(9)); len(rs) != 0 {
		t.Fatalf("Retained on invalid side: %v", rs)
	}
	if wm, ok := c.Watermark(Side(9)); wm != 0 || ok {
		t.Fatalf("Watermark on invalid side: %d,%v", wm, ok)
	}
}

func TestTimeRegression(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 8})
	if _, err := c.Process(SideLeft, &Event{Key: "k", Lo: 10, Hi: 20}); err != nil {
		t.Fatal(err)
	}
	// Lo 小于本侧水位线 → 拒绝；对侧独立水位线不受此限。
	_, err := c.Process(SideLeft, &Event{Key: "k", Lo: 9, Hi: 20})
	wantErrCode(t, err, CodeTimeRegression)

	if wm, ok := c.Watermark(SideLeft); !ok || wm != 10 {
		t.Fatalf("watermark changed after rejected op: %d,%v", wm, ok)
	}
	// 编号不得被拒绝操作消耗：下一条成功事件编号必须为 2。
	step, err := c.Process(SideLeft, &Event{Key: "k", Lo: 10, Hi: 20})
	if err != nil {
		t.Fatalf("equal Lo (non-decreasing) rejected: %v", err)
	}
	if rs := c.Retained(SideLeft); len(rs) != 2 || rs[1].ID != 2 {
		t.Fatalf("id consumed by rejected op, retained=%v", rs)
	}
	if len(step) != 0 {
		t.Fatalf("same-side events must not pair, got %v", step)
	}
}

func TestClosedIntervalBoundaries(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 8})
	// 左事件 [5,10] 先到；右流随后按 Lo 非递减到达。
	if _, err := c.Process(SideLeft, &Event{Key: "k", Lo: 5, Hi: 10}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		e      Event
		paired bool
		reason string
	}{
		{Event{Key: "k", Lo: 1, Hi: 5}, true, "right.Hi==left.Lo（左端点闭合）"},
		{Event{Key: "k", Lo: 1, Hi: 4}, false, "right.Hi==left.Lo-1，间隙 1"},
		{Event{Key: "k", Lo: 10, Hi: 20}, true, "right.Lo==left.Hi（右端点闭合）"},
		{Event{Key: "k", Lo: 11, Hi: 20}, false, "right.Lo==left.Hi+1，间隙 1；且左事件此刻已被清理"},
	}
	var got []Pair
	for _, tc := range cases {
		step, err := c.Process(SideRight, &tc.e)
		if err != nil {
			t.Fatalf("%s: %v", tc.reason, err)
		}
		if (len(step) > 0) != tc.paired {
			t.Fatalf("%s: want paired=%v, got step=%v", tc.reason, tc.paired, step)
		}
		got = append(got, step...)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 boundary pairs, got %d: %+v", len(got), got)
	}
	for _, p := range got {
		if p.LeftID != 1 || (p.RightID != 1 && p.RightID != 3) {
			t.Fatalf("unexpected pair %+v", p)
		}
		if p.Key != "k" {
			t.Fatalf("pair key mismatch: %+v", p)
		}
	}
}

func TestCleanupPrecisionPerStep(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 8})
	ids := func(rs []RetainedEvent) []int64 {
		out := make([]int64, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.ID)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	eq := func(got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("retained ids=%v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("retained ids=%v, want %v", got, want)
			}
		}
	}

	// 左：a=[1,100]，b=[2,2]。
	mustStep(t, c, SideLeft, &Event{Key: "a", Lo: 1, Hi: 100})
	mustStep(t, c, SideLeft, &Event{Key: "b", Lo: 2, Hi: 2})
	eq(ids(c.Retained(SideLeft)), 1, 2)

	// 右 wm 升到 50：b.Hi=2<50 立即删除；a.Hi=100 保留；右事件自身保留。
	mustStep(t, c, SideRight, &Event{Key: "a", Lo: 50, Hi: 60})
	eq(ids(c.Retained(SideLeft)), 1)
	eq(ids(c.Retained(SideRight)), 1)

	// 右 wm=100：a.Hi==100 == wm，闭合端点不得清理。
	mustStep(t, c, SideRight, &Event{Key: "a", Lo: 100, Hi: 100})
	eq(ids(c.Retained(SideLeft)), 1)
	eq(ids(c.Retained(SideRight)), 1, 2)

	// 右 wm=101：a.Hi=100<101 删除；右 [50,60] 在左 wm=2 下仍保留。
	mustStep(t, c, SideRight, &Event{Key: "a", Lo: 101, Hi: 101})
	eq(ids(c.Retained(SideLeft)))
	eq(ids(c.Retained(SideRight)), 1, 2, 3)

	// 左 wm 跳到 200：右三条 Hi 分别为 60/100/101 全部 <200，逐条删除；
	// 新左事件 [200,200] 在右 wm=101 下保留。注意 id=2 已随 b 删除但编号不回收，
	// 新事件为左 id=3。
	mustStep(t, c, SideLeft, &Event{Key: "a", Lo: 200, Hi: 200})
	eq(ids(c.Retained(SideRight)))
	eq(ids(c.Retained(SideLeft)), 3)
}

func mustStep(t *testing.T, c *Connector, s Side, e *Event) []Pair {
	t.Helper()
	step, err := c.Process(s, e)
	if err != nil {
		t.Fatalf("Process(%s,%+v): %v", s, e, err)
	}
	return step
}

func TestRetentionLimitRejected(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 1})
	mustStep(t, c, SideLeft, &Event{Key: "a", Lo: 1, Hi: 100})
	// 对侧水位线尚未建立，左第二条无法被清理 → 一步之后保留 2 > 1，拒绝。
	_, err := c.Process(SideLeft, &Event{Key: "a", Lo: 2, Hi: 100})
	wantErrCode(t, err, CodeRetentionLimitExceeded)

	// 拒绝后：水位线、编号、保留、已输出对全部不变。
	if wm, ok := c.Watermark(SideLeft); !ok || wm != 1 {
		t.Fatalf("watermark mutated: %d,%v", wm, ok)
	}
	if rs := c.Retained(SideLeft); len(rs) != 1 || rs[0].ID != 1 {
		t.Fatalf("retention mutated: %+v", rs)
	}
	step := mustStep(t, c, SideRight, &Event{Key: "a", Lo: 50, Hi: 60})
	// 被拒事件编号 2 不得被复用：右第一条仍是 id=1，并与左 id=1 配对。
	if len(step) != 1 || step[0].LeftID != 1 || step[0].RightID != 1 {
		t.Fatalf("unexpected step after rejected op: %+v", step)
	}
	if ps := c.Pairs(); len(ps) != 1 {
		t.Fatalf("pairs mutated: %+v", ps)
	}
}

// TestRetentionLimitOppositeSide 直接构造内部状态，覆盖“对侧清理后超限”的
// 防御性预检分支：正常事件顺序下对侧计数只减不增，该分支不可由公开 API 触达。
func TestRetentionLimitOppositeSide(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 1})
	// 右侧手工放入 2 条在新左水位线 10 下都不会被清理的事件。
	c.sides[SideRight].wm, c.sides[SideRight].wmSet = 5, true
	c.sides[SideRight].nextID = 2
	c.sides[SideRight].retained["a"] = []entry{
		{id: 1, e: Event{Key: "a", Lo: 1, Hi: 100}},
		{id: 2, e: Event{Key: "a", Lo: 5, Hi: 100}},
	}
	_, err := c.Process(SideLeft, &Event{Key: "a", Lo: 10, Hi: 100})
	wantErrCode(t, err, CodeRetentionLimitExceeded)
	var je *JoinError
	errors.As(err, &je)
	if je.Side != SideRight {
		t.Fatalf("want error attributed to right side, got %s", je.Side)
	}
	if _, ok := c.Watermark(SideLeft); ok {
		t.Fatal("left watermark must not advance after opposite-side limit rejection")
	}
}

func TestRejectedOpLeavesEverythingUnchanged(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 8})
	mustStep(t, c, SideLeft, &Event{Key: "a", Lo: 1, Hi: 100})
	step := mustStep(t, c, SideRight, &Event{Key: "a", Lo: 50, Hi: 60})
	if len(step) != 1 {
		t.Fatalf("baseline pair missing: %+v", step)
	}

	snapshot := struct {
		pairs    []Pair
		left     []RetainedEvent
		right    []RetainedEvent
		lwm, rwm int64
		lset     bool
	}{
		pairs: c.Pairs(), left: c.Retained(SideLeft), right: c.Retained(SideRight),
	}
	snapshot.lwm, snapshot.lset = c.Watermark(SideLeft)
	snapshot.rwm, _ = c.Watermark(SideRight)

	// 依次触发全部拒绝原因。
	bad := []struct {
		s Side
		e *Event
	}{
		{SideLeft, nil},
		{SideLeft, &Event{Key: "a", Lo: 0, Hi: 0}},   // 时间倒退
		{SideRight, &Event{Key: "", Lo: 50, Hi: 60}}, // 空键
		{SideRight, &Event{Key: "a", Lo: 5, Hi: 1}},  // 区间非法
		{Side(7), &Event{Key: "a", Lo: 50, Hi: 60}},  // 非法侧
	}
	for _, b := range bad {
		if _, err := c.Process(b.s, b.e); err == nil {
			t.Fatalf("bad op accepted: %+v", b)
		}
	}

	if ps := c.Pairs(); len(ps) != len(snapshot.pairs) || ps[0] != snapshot.pairs[0] {
		t.Fatalf("pairs changed: %+v vs %+v", ps, snapshot.pairs)
	}
	if wm, ok := c.Watermark(SideLeft); wm != snapshot.lwm || ok != snapshot.lset {
		t.Fatalf("left wm changed: %d,%v vs %d,%v", wm, ok, snapshot.lwm, snapshot.lset)
	}
	if wm, ok := c.Watermark(SideRight); wm != snapshot.rwm || !ok {
		t.Fatalf("right wm changed: %d,%v vs %d", wm, ok, snapshot.rwm)
	}
	if rs := c.Retained(SideLeft); len(rs) != len(snapshot.left) || rs[0] != snapshot.left[0] {
		t.Fatalf("left retained changed: %+v vs %+v", rs, snapshot.left)
	}
	if rs := c.Retained(SideRight); len(rs) != len(snapshot.right) || rs[0] != snapshot.right[0] {
		t.Fatalf("right retained changed: %+v vs %+v", rs, snapshot.right)
	}
}

func TestKeyIsolation(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 8})
	mustStep(t, c, SideLeft, &Event{Key: "a", Lo: 1, Hi: 10})
	step := mustStep(t, c, SideRight, &Event{Key: "b", Lo: 1, Hi: 10})
	if len(step) != 0 {
		t.Fatalf("different keys must not pair: %+v", step)
	}
	step = mustStep(t, c, SideRight, &Event{Key: "a", Lo: 1, Hi: 10})
	if len(step) != 1 || step[0].Key != "a" {
		t.Fatalf("same-key pair missing: %+v", step)
	}
}

func TestDeterministicReplay(t *testing.T) {
	type in struct {
		s Side
		e Event
	}
	seq := []in{
		{SideLeft, Event{Key: "a", Lo: 1, Hi: 10}},
		{SideRight, Event{Key: "a", Lo: 2, Hi: 3}},
		{SideRight, Event{Key: "b", Lo: 2, Hi: 8}},
		{SideLeft, Event{Key: "a", Lo: 4, Hi: 20}},
		{SideLeft, Event{Key: "b", Lo: 4, Hi: 5}},
		{SideRight, Event{Key: "a", Lo: 9, Hi: 12}},
		{SideRight, Event{Key: "b", Lo: 9, Hi: 10}},
		{SideLeft, Event{Key: "a", Lo: 21, Hi: 30}},
	}
	run := func() (steps [][]Pair, final []Pair) {
		c, _ := New(Config{MaxRetainedPerSide: 16})
		for _, x := range seq {
			steps = append(steps, mustStep(t, c, x.s, &x.e))
		}
		return steps, c.Pairs()
	}
	s1, f1 := run()
	s2, f2 := run()
	if len(s1) != len(s2) {
		t.Fatal("step count mismatch")
	}
	for i := range s1 {
		if len(s1[i]) != len(s2[i]) {
			t.Fatalf("step %d pair count %d vs %d", i, len(s1[i]), len(s2[i]))
		}
		for j := range s1[i] {
			if s1[i][j] != s2[i][j] {
				t.Fatalf("step %d pair %d differs: %+v vs %+v", i, j, s1[i][j], s2[i][j])
			}
		}
	}
	if len(f1) != len(f2) {
		t.Fatalf("final pair count %d vs %d", len(f1), len(f2))
	}
	for i := range f1 {
		if f1[i] != f2[i] {
			t.Fatalf("final pair %d differs: %+v vs %+v", i, f1[i], f2[i])
		}
	}
	if len(f1) == 0 {
		t.Fatal("sanity: expected some pairs in replay")
	}
}

func TestConcurrentReaders(t *testing.T) {
	c, _ := New(Config{MaxRetainedPerSide: 100000})
	const writers = 2
	const steps = 100
	var readerWg, writerWg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：并发热读，任何返回切片都必须是自洽、有序的快照。
	reader := func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			ps := c.Pairs()
			if !sort.SliceIsSorted(ps, func(i, j int) bool {
				if ps[i].LeftID != ps[j].LeftID {
					return ps[i].LeftID < ps[j].LeftID
				}
				return ps[i].RightID < ps[j].RightID
			}) {
				t.Errorf("Pairs snapshot not sorted: %+v", ps)
				return
			}
			for _, p := range ps {
				if p.LeftLo > p.LeftHi || p.RightLo > p.RightHi {
					t.Errorf("corrupt pair snapshot: %+v", p)
					return
				}
			}
			for _, s := range []Side{SideLeft, SideRight} {
				rs := c.Retained(s)
				for _, r := range rs {
					if r.Lo > r.Hi {
						t.Errorf("corrupt retained snapshot: %+v", r)
						return
					}
				}
				_, _ = c.Watermark(s)
			}
		}
	}
	for i := 0; i < 4; i++ {
		readerWg.Add(1)
		go reader()
	}

	// 写者：左右各自 Lo 单调递增、区间足够宽，保证不退步、不触发上限。
	for w := 0; w < writers; w++ {
		writerWg.Add(1)
		go func(off int) {
			defer writerWg.Done()
			for i := 0; i < steps; i++ {
				lo := int64(off + i*2)
				_, err := c.Process(Side(off%2), &Event{Key: "k", Lo: lo, Hi: lo + 1_000_000})
				if err != nil {
					t.Errorf("writer %d step %d: %v", off, i, err)
					return
				}
			}
		}(w)
	}
	// 等写者全部结束后再停读者，最后回收读者。
	writerWg.Wait()
	close(stop)
	readerWg.Wait()
}

func TestSetLoggerNilDiscards(t *testing.T) {
	var buf bytes.Buffer
	SetLogger(&buf)
	SetLogger(nil)
	c, _ := New(Config{MaxRetainedPerSide: 4})
	mustStep(t, c, SideLeft, &Event{Key: "k", Lo: 1, Hi: 2})
	if buf.Len() != 0 {
		t.Fatalf("nil logger did not discard output: %q", buf.String())
	}
	SetLogger(io.Discard)
}

func TestLoggingContents(t *testing.T) {
	var buf bytes.Buffer
	SetLogger(&buf)
	defer SetLogger(io.Discard)

	c, _ := New(Config{MaxRetainedPerSide: 8})
	mustStep(t, c, SideLeft, &Event{Key: "k", Lo: 1, Hi: 10})
	mustStep(t, c, SideRight, &Event{Key: "k", Lo: 5, Hi: 6})   // 相交
	mustStep(t, c, SideRight, &Event{Key: "k", Lo: 11, Hi: 20}) // 不相交，并清理左事件
	if _, err := c.Process(SideLeft, &Event{Key: "k", Lo: 0, Hi: 0}); err == nil {
		t.Fatal("time-regression op should be rejected")
	}
	log := buf.String()

	for _, want := range []string{
		"input  side=left",
		"input  side=right",
		"match?",
		"=> true",
		"=> false",
		"cleanup",
		"removed:",
		"kept:",
		"output side=",
		"pairs=1",
		"reject  side=left",
		"code=time_regression",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
