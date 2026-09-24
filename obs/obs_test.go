package obs

import (
	"errors"
	"testing"

	"ontology/seq"
)

// TestFeedSteps 钉第三节六步序列每步后的 MaxSeen/三态/OOO/MaxLateness，
// 特别是第 3 步迟到量=5、第 5 步（迟到量仅 1）后 MaxLateness 仍为历史最大 5。
func TestFeedSteps(t *testing.T) {
	o, err := New(10)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		v                  int64
		maxSeen, ooo, late int64
		kind               seq.Kind
	}
	steps := []row{
		{10, 10, 0, 0, seq.InOrder},
		{10, 10, 0, 0, seq.Equal},
		{5, 10, 1, 5, seq.Late},
		{12, 12, 1, 5, seq.InOrder},
		{11, 12, 2, 5, seq.Late},
		{13, 13, 2, 5, seq.InOrder},
	}
	var prev int64
	var seen bool
	for i, st := range steps {
		r, cerr := seq.Classify(st.v, prev, seen)
		if cerr != nil {
			t.Fatalf("step %d classify: %v", i+1, cerr)
		}
		if err := o.Feed(st.v); err != nil {
			t.Fatalf("step %d feed: %v", i+1, err)
		}
		prev, seen = o.MaxSeen(), true
		if r.Kind != st.kind || o.MaxSeen() != st.maxSeen ||
			o.OutOfOrder() != st.ooo || o.MaxLateness() != st.late {
			t.Errorf("step %d Seq=%d: got kind=%v max=%d ooo=%d late=%d; want kind=%v max=%d ooo=%d late=%d",
				i+1, st.v, r.Kind, o.MaxSeen(), o.OutOfOrder(), o.MaxLateness(),
				st.kind, st.maxSeen, st.ooo, st.late)
		}
	}
}

func TestClassifyAndFeedRejectInvalid(t *testing.T) {
	bad := []int64{0, -1, -100}
	for _, v := range bad { // 表驱动：非法序号在 seq 层与 obs.Feed 都被同一哨兵拒绝
		if _, err := seq.Classify(v, 10, true); !errors.Is(err, seq.ErrInvalidSeq) {
			t.Errorf("Classify(%d) err=%v", v, err)
		}
		o, _ := New(10)
		if err := o.Feed(v); !errors.Is(err, ErrInvalidSeq) {
			t.Errorf("Feed(%d) err=%v", v, err)
		}
		if o.MaxSeen() != 0 || o.OutOfOrder() != 0 {
			t.Errorf("Feed(%d) left a trace", v)
		}
	}
}

func TestExceedsSlackFlag(t *testing.T) {
	cases := []struct {
		slack int64
		evs   []int64
		want  bool
	}{
		{10, []int64{10, 10, 5, 12, 11, 13}, false}, // 最大迟到 5，不超 10
		{5, []int64{10, 5}, false},                  // 迟到 5 不大于 slack 5
		{4, []int64{10, 5}, true},                   // 迟到 5 > 4
		{0, []int64{5, 3}, true},                    // 迟到 2 > 0
	}
	for i, c := range cases {
		o, _ := New(c.slack)
		for _, v := range c.evs {
			if err := o.Feed(v); err != nil {
				t.Fatal(err)
			}
		}
		if o.ExceedsSlack() != c.want {
			t.Errorf("case %d: ExceedsSlack=%v want %v", i, o.ExceedsSlack(), c.want)
		}
	}
}

// TestCheckCountConstant 证明每个事件只与标量 maxSeen 比较一次：先喂 m 个
// 递增事件，再喂一个新事件，lastChecked 不随 m 线性增长（恒 <=1）。
func TestCheckCountConstant(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} { // 100..10000 多档
		o, _ := New(10)
		if err := o.Feed(1); err != nil || o.lastChecked != 0 { // 首个事件无历史可查
			t.Fatalf("m=%d first event checks=%d", m, o.lastChecked)
		}
		for v := int64(2); v <= m; v++ {
			if err := o.Feed(v); err != nil {
				t.Fatal(err)
			}
		}
		if o.lastChecked != 1 { // 喂完 m 个后仍是常数 1
			t.Fatalf("m=%d checks after m events=%d", m, o.lastChecked)
		}
		if err := o.Feed(m - 1); err != nil || o.lastChecked != 1 { // 再多一个也不回扫
			t.Fatalf("m=%d extra event checks=%d err=%v", m, o.lastChecked, err)
		}
	}
}

func TestFreezeRejectsAndSticks(t *testing.T) {
	o, _ := New(10)
	if err := o.Feed(1); err != nil {
		t.Fatal(err)
	}
	if err := o.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := o.Feed(2); !errors.Is(err, ErrFrozen) {
		t.Errorf("frozen Feed err=%v", err)
	}
	if err := o.Freeze(); !errors.Is(err, ErrFrozen) {
		t.Errorf("double Freeze err=%v", err)
	}
	if o.MaxSeen() != 1 { // 冻结后写不留痕
		t.Errorf("frozen feed changed maxSeen=%d", o.MaxSeen())
	}
}

func TestNewNegativeSlack(t *testing.T) {
	o, err := New(-1)
	if o != nil || !errors.Is(err, ErrNegativeSlack) {
		t.Fatalf("New(-1) = %v, %v", o, err)
	}
}

func TestSelfCheck(t *testing.T) {
	o, _ := New(10)
	if err := o.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
