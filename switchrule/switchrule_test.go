package switchrule

import (
	"testing"

	"ontology/plan"
)

func rec(d int, v Verdict) Record { return Record{D: d, Verdict: v} }

func TestNormalSwitchExamples(t *testing.T) {
	// 例 1：d = 0,2,1,0,0,3 → 第 6 批后最近 5 批含 2 拒收 → Tightened
	e := New(2)
	st := e.NewStream()
	ds := []struct {
		d    int
		v    Verdict
		want plan.Severity
	}{
		{0, Accept, plan.Normal},
		{2, Reject, plan.Normal},
		{1, Accept, plan.Normal},
		{0, Accept, plan.Normal},
		{0, Accept, plan.Normal},
		{3, Reject, plan.Tightened},
	}
	for i, c := range ds {
		if got := e.Observe(st, rec(c.d, c.v)); got != c.want {
			t.Fatalf("lot %d: want %v got %v", i+1, c.want, got)
		}
	}
}

func TestNormalWindowSliding(t *testing.T) {
	// 第 6 批 d=0 不收紧；第 7 批 d=2：窗口第 3..7 批只 1 拒，不转
	e := New(2)
	st := e.NewStream()
	seq := []Verdict{Accept, Reject, Accept, Accept, Accept, Accept, Reject}
	dseq := []int{0, 2, 1, 0, 0, 0, 2}
	for i := range seq {
		got := e.Observe(st, rec(dseq[i], seq[i]))
		if got != plan.Normal {
			t.Fatalf("lot %d unexpectedly switched to %v", i+1, got)
		}
	}
	// 再来一批拒收：最近 5 批（第 5..9，含第 7、9 两拒）→ Tightened
	if got := e.Observe(st, rec(2, Reject)); got != plan.Tightened {
		t.Fatalf("want Tightened, got %v", got)
	}
}

func TestTightenedRunsAndCounters(t *testing.T) {
	e := New(2)
	st := e.NewStream()
	e.Enter(st, plan.Tightened)
	// 收收收收拒收收收收收 → 连续 5 收转 Normal，累计拒 1
	seq := []Verdict{Accept, Accept, Accept, Accept, Reject, Accept, Accept, Accept, Accept, Accept}
	for i, v := range seq {
		got := e.Observe(st, rec(0, v))
		if i < len(seq)-1 && got != plan.Tightened {
			t.Fatalf("lot %d unexpected %v", i+1, got)
		}
		if i == len(seq)-1 && got != plan.Normal {
			t.Fatalf("lot 10 want Normal, got %v", got)
		}
	}
	// 进入 Normal 后首批即拒收：窗口只有 1 批，不转
	if got := e.Observe(st, rec(2, Reject)); got != plan.Normal {
		t.Fatalf("window must reset on switch, got %v", got)
	}
}

func TestTightenedFiveRejectsSuspend(t *testing.T) {
	// 累计（非连续）5 拒收 → Suspended，与连续接收滑动的区别
	e := New(2)
	st := e.NewStream()
	e.Enter(st, plan.Tightened)
	seq := []Verdict{Reject, Accept, Reject, Accept, Reject, Accept, Reject, Accept, Reject}
	for i, v := range seq {
		got := e.Observe(st, rec(0, v))
		if i < len(seq)-1 && got == plan.Suspended {
			t.Fatalf("suspended too early at %d", i+1)
		}
		if i == len(seq)-1 && got != plan.Suspended {
			t.Fatalf("want Suspended, got %v", got)
		}
	}
	e.Resume(st)
	if got := e.State(st); got != plan.Tightened {
		t.Fatalf("resume want Tightened, got %v", got)
	}
	// 恢复后计数清零：一拒收不暂停
	if got := e.Observe(st, rec(2, Reject)); got == plan.Suspended {
		t.Fatal("counters not cleared after resume")
	}
}

func TestReducedSwitch(t *testing.T) {
	e := New(2)
	st := e.NewStream()
	e.Enter(st, plan.Reduced)
	if got := e.Observe(st, rec(1, BorderlineAccept)); got != plan.Normal {
		t.Fatalf("borderline want Normal, got %v", got)
	}
	e.Enter(st, plan.Reduced)
	if got := e.Observe(st, rec(2, Reject)); got != plan.Normal {
		t.Fatalf("reject want Normal, got %v", got)
	}
	e.Enter(st, plan.Reduced)
	if got := e.Observe(st, rec(0, Accept)); got != plan.Reduced {
		t.Fatalf("accept must stay Reduced, got %v", got)
	}
}

func TestNormalRelaxLimit(t *testing.T) {
	// 连续 10 收，d = 1,0,0,0,1,0,0,0,0,1（和 3 > Lr=2）不转
	e := New(2)
	st := e.NewStream()
	ds := []int{1, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	for _, d := range ds {
		if got := e.Observe(st, rec(d, Accept)); got != plan.Normal {
			t.Fatalf("unexpected switch %v", got)
		}
	}
	// 第 11 批 d=0：最近 10 批和恰为 2 → Reduced
	if got := e.Observe(st, rec(0, Accept)); got != plan.Reduced {
		t.Fatalf("sum==Lr must switch, got %v", got)
	}
	// 切换清空窗口：回 Normal 后首批拒收不转
	e.Enter(st, plan.Normal)
	if got := e.Observe(st, rec(2, Reject)); got != plan.Normal {
		t.Fatalf("window must be cleared, got %v", got)
	}
}

func TestLookedBounded(t *testing.T) {
	for _, total := range []int{100, 10000} {
		e := New(2)
		st := e.NewStream()
		var last int
		for i := 0; i < total; i++ {
			// 每 6 批 1 拒收：最近 5 批最多 1 拒、无法连续 10 收，始终留在 Normal
			v := Accept
			if i%6 == 5 {
				v = Reject
			}
			if got := e.Observe(st, rec(0, v)); got != plan.Normal {
				t.Fatalf("history=%d lot %d unexpectedly left Normal: %v", total, i+1, got)
			}
			last = e.Looked(st)
		}
		if last != 10 {
			t.Fatalf("history=%d want looked=10, got %d", total, last)
		}
	}
}
