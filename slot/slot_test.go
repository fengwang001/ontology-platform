package slot

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, filler int64) *Timeline {
	t.Helper()
	tl, err := New(filler)
	if err != nil {
		t.Fatalf("New(%d): %v", filler, err)
	}
	return tl
}

func layoutSnapshot(tl *Timeline) string {
	s := ""
	tl.View(func(st *State) {
		for _, seg := range st.Layout {
			s += fmt.Sprintf("%s[%d,%d)@%d;", seg.ID, seg.Start, seg.End, seg.Offset)
		}
	})
	return s
}

func TestNew(t *testing.T) {
	for _, f := range []int64{-1, 0, MaxFiller + 1} {
		if _, err := New(f); !errors.Is(err, ErrInvalid) {
			t.Errorf("New(%d): got %v, want ErrInvalid", f, err)
		}
	}
	for _, f := range []int64{1, MaxFiller} {
		if _, err := New(f); err != nil {
			t.Errorf("New(%d): got %v, want nil", f, err)
		}
	}
}

type schedStep struct {
	now, start, dur int64
	id              string
	fixed           bool
	want            error
}

func TestSchedule(t *testing.T) {
	cases := []struct {
		name  string
		steps []schedStep
		want  string // 最终 Layout 快照，空串表示不检查
	}{
		{"基本排入", []schedStep{
			{0, 100, 100, "A", false, nil},
		}, "A[100,200)@0;"},
		{"相接不算重叠", []schedStep{
			{0, 0, 100, "A", false, nil},
			{0, 100, 100, "B", false, nil},
			{0, 200, 100, "C", true, nil},
		}, "A[0,100)@0;B[100,200)@0;C[200,300)@0;"},
		{"两侧相接中间相交", []schedStep{
			{0, 0, 100, "A", false, nil},
			{0, 200, 100, "B", false, nil},
			{0, 50, 150, "X", false, ErrOverlap},
			{0, 100, 100, "C", false, nil},
		}, "A[0,100)@0;C[100,200)@0;B[200,300)@0;"},
		{"start恰等now允许", []schedStep{
			{50, 50, 10, "A", false, nil},
			{50, 49, 10, "B", false, ErrPast},
		}, "A[50,60)@0;"},
		{"参数非法", []schedStep{
			{0, 0, 0, "A", false, ErrInvalid},          // dur 为 0
			{0, 0, MaxDur + 1, "A", false, ErrInvalid}, // dur 超界
			{0, 0, 10, "", false, ErrInvalid},          // 空 id
			{0, -1, 10, "A", false, ErrInvalid},        // start 为负
			{-1, 0, 10, "A", false, ErrInvalid},        // now 为负
			{MaxTime + 1, 0, 10, "A", false, ErrInvalid},
			{0, MaxTime, 1, "A", false, ErrInvalid}, // 终点超出时间线
		}, ""},
		{"时钟回退与被拒不走钟", []schedStep{
			{10, 100, 10, "A", false, nil},
			{9, 200, 10, "B", false, ErrClock},
			{20, 100, 10, "C", false, ErrOverlap}, // 被拒，时钟不进
			{15, 200, 10, "D", false, nil},        // 时钟仍为 10，15 合法
		}, "A[100,110)@0;D[200,210)@0;"},
		{"标识已存在", []schedStep{
			{0, 0, 10, "A", false, nil},
			{0, 100, 10, "A", false, ErrIDExists},
		}, "A[0,10)@0;"},
		{"拒绝次序", []schedStep{
			{0, 0, 100, "A", false, nil},
			{10, 200, 10, "D", false, nil},
			{9, 0, 0, "B", false, ErrInvalid},    // 非法+回退 -> 非法
			{9, 300, 10, "A", false, ErrClock},   // 回退+已存在 -> 回退
			{10, 5, 10, "A", false, ErrIDExists}, // 已存在+已过去 -> 已存在
			{10, 5, 10, "B", false, ErrPast},     // 已过去+重叠 -> 已过去
		}, "A[0,100)@0;D[200,210)@0;"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := mustNew(t, 7)
			for i, s := range c.steps {
				err := tl.Schedule(s.now, s.id, s.start, s.dur, s.fixed)
				if !errors.Is(err, s.want) {
					t.Errorf("step %d: got %v, want %v", i, err, s.want)
				}
			}
			if c.want != "" {
				if got := layoutSnapshot(tl); got != c.want {
					t.Errorf("layout = %s, want %s", got, c.want)
				}
			}
		})
	}
}

func TestCancel(t *testing.T) {
	type cancelStep struct {
		now  int64
		id   string
		want error
	}
	cases := []struct {
		name   string
		sched  []schedStep
		cancel []cancelStep
		want   string
	}{
		{"截断正在播出的段", []schedStep{{0, 100, 100, "A", true, nil}},
			[]cancelStep{{150, "A", nil}}, "A[100,150)@0;"},
		{"删除未开播的段", []schedStep{{0, 100, 100, "A", false, nil}},
			[]cancelStep{{50, "A", nil}}, ""},
		{"全部结束后报已结束", []schedStep{{0, 100, 100, "A", false, nil}},
			[]cancelStep{{150, "A", nil}, {150, "A", ErrEnded}, {200, "A", ErrEnded}}, "A[100,150)@0;"},
		{"标识不存在", nil,
			[]cancelStep{{0, "ZZ", ErrIDNotFound}}, ""},
		{"取消不回填", []schedStep{{0, 100, 100, "A", false, nil}, {0, 300, 100, "B", false, nil}},
			[]cancelStep{{150, "A", nil}}, "A[100,150)@0;B[300,400)@0;"},
		{"拒绝次序", []schedStep{{0, 100, 100, "A", false, nil}},
			[]cancelStep{
				{150, "A", nil},
				{140, "", ErrInvalid}, // 非法+回退 -> 非法
				{140, "A", ErrClock},  // 回退+已结束 -> 回退
				{160, "ZZ", ErrIDNotFound},
				{160, "A", ErrEnded},
			}, "A[100,150)@0;"},
		{"被拒不走钟", []schedStep{{0, 100, 100, "A", false, nil}, {0, 300, 100, "B", false, nil}},
			[]cancelStep{
				{150, "A", nil},
				{200, "A", ErrEnded}, // 被拒，时钟仍为 150
				{160, "B", nil},      // 160 >= 150 合法；B 未开播被整段删除
			}, "A[100,150)@0;"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := mustNew(t, 7)
			for i, s := range c.sched {
				if err := tl.Schedule(s.now, s.id, s.start, s.dur, s.fixed); err != nil {
					t.Fatalf("sched step %d: %v", i, err)
				}
			}
			for i, s := range c.cancel {
				if err := tl.Cancel(s.now, s.id); !errors.Is(err, s.want) {
					t.Errorf("cancel step %d: got %v, want %v", i, err, s.want)
				}
			}
			if got := layoutSnapshot(tl); got != c.want {
				t.Errorf("layout = %q, want %q", got, c.want)
			}
		})
	}
}
