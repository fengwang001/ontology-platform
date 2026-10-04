package lockstep

import (
	"bytes"
	"errors"
	"testing"

	"ontology/turn"
)

type op struct {
	now int64
	p   int
	k   int
	cmd []byte
}

func mustSubmit(t *testing.T, e *Engine, o op, wantErr error) {
	t.Helper()
	err := e.Submit(o.now, o.p, o.k, o.cmd)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit(now=%d p=%d k=%d cmd=%q) err=%v, want %v", o.now, o.p, o.k, string(o.cmd), err, wantErr)
	}
	t.Logf("输入 Submit(now=%d p=%d k=%d cmd=%q) => err=%v；判定依据：%s",
		o.now, o.p, o.k, string(o.cmd), err, verdict(err))
}

func mustAdvance(t *testing.T, e *Engine, now int64, wantTurns []int) {
	t.Helper()
	recs, err := e.Advance(now)
	if err != nil {
		t.Fatalf("Advance(%d) err=%v", now, err)
	}
	got := make([]int, len(recs))
	for i, r := range recs {
		got[i] = r.Turn
	}
	if len(got) != len(wantTurns) {
		t.Fatalf("Advance(%d) settled turns=%v, want %v", now, got, wantTurns)
	}
	for i := range got {
		if got[i] != wantTurns[i] {
			t.Fatalf("Advance(%d) settled turns=%v, want %v", now, got, wantTurns)
		}
	}
	for _, r := range recs {
		t.Logf("输出 回合%d ts=%d %s", r.Turn, r.TS, slotsString(r.Slots))
	}
}

func verdict(err error) string {
	switch {
	case errors.Is(err, ErrInvalidParam):
		return "参数非法"
	case errors.Is(err, ErrClockRewind):
		return "时钟回退"
	case errors.Is(err, ErrLate):
		return "迟到（入口处理之后判定）"
	case errors.Is(err, ErrAhead):
		return "超前窗口"
	case errors.Is(err, ErrDuplicate):
		return "重复提交"
	default:
		return "接受并存入输入"
	}
}

func slotsString(ss []turn.Slot) string {
	var b bytes.Buffer
	b.WriteString("[")
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(' ')
		}
		switch s.Source {
		case turn.Live:
			b.WriteString("实交:")
		case turn.Repeat:
			b.WriteString("重复:")
		case turn.Blank:
			b.WriteString("空填:")
		}
		b.Write(s.Cmd)
	}
	b.WriteString("]")
	return b.String()
}

func assertSlot(t *testing.T, rec turn.Record, p int, src turn.Source, cmd string) {
	t.Helper()
	s := rec.Slots[p]
	if s.Source != src || string(s.Cmd) != cmd {
		t.Fatalf("回合%d 玩家%d = (%q,%d), want (%q,%d)", rec.Turn, p, string(s.Cmd), s.Source, cmd, src)
	}
}

func TestDocumentedExample(t *testing.T) {
	e := New(2, 100, 2, 1, 3, 0)
	mustSubmit(t, e, op{now: 10, p: 0, k: 1, cmd: []byte("a")}, nil)
	mustSubmit(t, e, op{now: 20, p: 1, k: 1, cmd: []byte("b")}, nil)
	if e.Cur() != 2 || e.Deadline() != 120 {
		t.Fatalf("cur=%d dl=%d, want 2/120", e.Cur(), e.Deadline())
	}
	mustSubmit(t, e, op{now: 30, p: 0, k: 2, cmd: []byte("c")}, nil)
	mustSubmit(t, e, op{now: 40, p: 0, k: 3, cmd: []byte("d")}, nil)

	mustSubmit(t, e, op{now: 125, p: 1, k: 2, cmd: []byte("x")}, ErrLate)
	if e.Cur() != 3 || e.Deadline() != 220 || e.Miss(1) != 1 {
		t.Fatalf("after timeout: cur=%d dl=%d m1=%d", e.Cur(), e.Deadline(), e.Miss(1))
	}
	r2 := e.Log(2)[0]
	assertSlot(t, r2, 0, turn.Live, "c")
	assertSlot(t, r2, 1, turn.Repeat, "b")

	mustAdvance(t, e, 330, []int{3, 4})
	r3, r4 := e.Log(3)[0], e.Log(4)[0]
	if r3.TS != 220 || r4.TS != 320 {
		t.Fatalf("ts r3=%d r4=%d", r3.TS, r4.TS)
	}
	assertSlot(t, r3, 0, turn.Live, "d")
	assertSlot(t, r3, 1, turn.Blank, "")
	assertSlot(t, r4, 0, turn.Repeat, "d")
	assertSlot(t, r4, 1, turn.Blank, "")
	if e.Active(1) {
		t.Fatal("p1 should be inactive after turn 4")
	}

	mustSubmit(t, e, op{now: 340, p: 0, k: 5, cmd: []byte("e")}, nil)
	r5 := e.Log(5)[0]
	assertSlot(t, r5, 0, turn.Live, "e")
	assertSlot(t, r5, 1, turn.Blank, "")

	mustSubmit(t, e, op{now: 350, p: 1, k: 7, cmd: []byte("y")}, nil)
	if !e.Active(1) || e.Miss(1) != 4 {
		t.Fatalf("p1 active=%v m=%d", e.Active(1), e.Miss(1))
	}
	mustSubmit(t, e, op{now: 360, p: 0, k: 6, cmd: []byte("f")}, nil)
	if e.Cur() != 6 {
		t.Fatalf("cur=%d want 6", e.Cur())
	}
	mustAdvance(t, e, 440, []int{6})
	r6 := e.Log(6)[0]
	assertSlot(t, r6, 0, turn.Live, "f")
	assertSlot(t, r6, 1, turn.Blank, "")
	if e.Active(1) {
		t.Fatal("p1 should be inactive again")
	}

	mustSubmit(t, e, op{now: 450, p: 0, k: 7, cmd: []byte("g")}, nil)
	r7 := e.Log(7)[0]
	if r7.TS != 450 {
		t.Fatalf("r7 ts=%d", r7.TS)
	}
	assertSlot(t, r7, 0, turn.Live, "g")
	assertSlot(t, r7, 1, turn.Live, "y")
	if e.Active(1) || e.Miss(1) != 0 {
		t.Fatalf("p1 active=%v m=%d, want inactive/m=0", e.Active(1), e.Miss(1))
	}

	mustSubmit(t, e, op{now: 460, p: 0, k: 8, cmd: []byte("h")}, nil)
	r8 := e.Log(8)[0]
	assertSlot(t, r8, 0, turn.Live, "h")
	assertSlot(t, r8, 1, turn.Repeat, "y")
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name     string
		n        int
		tt       int64
		a, r, kd int
		now0     int64
		ok       bool
	}{
		{"最小合法", 1, 1, 0, 0, 1, 0, true},
		{"最大合法", 4096, 1_000_000, 64, 100, 100, 1_000_000_000_000, true},
		{"n=0", 0, 100, 0, 0, 1, 0, false},
		{"n=4097", 4097, 100, 0, 0, 1, 0, false},
		{"t=0", 2, 0, 0, 0, 1, 0, false},
		{"t超界", 2, 1_000_001, 0, 0, 1, 0, false},
		{"a=-1", 2, 100, -1, 0, 1, 0, false},
		{"a=65", 2, 100, 65, 0, 1, 0, false},
		{"r=-1", 2, 100, 0, -1, 1, 0, false},
		{"r=101", 2, 100, 0, 101, 1, 0, false},
		{"kd=0", 2, 100, 0, 0, 0, 0, false},
		{"kd=101", 2, 100, 0, 0, 101, 0, false},
		{"now0=-1", 2, 100, 0, 0, 1, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			en := New(c.n, c.tt, c.a, c.r, c.kd, c.now0)
			if c.ok && en == nil {
				t.Fatal("want ok, got nil")
			}
			if !c.ok && en != nil {
				t.Fatal("want nil, got engine")
			}
			if c.ok && (en.Cur() != 1 || en.Deadline() != c.now0+c.tt) {
				t.Fatalf("init cur=%d dl=%d", en.Cur(), en.Deadline())
			}
		})
	}
}
