package blame

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, T int64) *Monitor {
	t.Helper()
	m, err := New(T)
	if err != nil {
		t.Fatalf("New(%d): %v", T, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Monitor, name string, off, dur int64, parents ...string) {
	t.Helper()
	if err := m.AddDataset(name, off, dur, parents); err != nil {
		t.Fatalf("AddDataset(%s): %v", name, err)
	}
}

func mustLand(t *testing.T, m *Monitor, d string, k, now int64) {
	t.Helper()
	if err := m.Land(d, k, now); err != nil {
		t.Fatalf("Land(%s,%d,%d): %v", d, k, now, err)
	}
}

func mustEvaluate(t *testing.T, m *Monitor, now int64) []Alert {
	t.Helper()
	alerts, err := m.Evaluate(now)
	if err != nil {
		t.Fatalf("Evaluate(%d): %v", now, err)
	}
	return alerts
}

func mustBlame(t *testing.T, m *Monitor, d string, k int64) Decision {
	t.Helper()
	dec, err := m.Blame(d, k)
	if err != nil {
		t.Fatalf("Blame(%s,%d): %v", d, k, err)
	}
	return dec
}

// chain 构造题目示例中的 a<-b<-c<-d 图（T=100）。
func chain(t *testing.T) *Monitor {
	t.Helper()
	m := mustNew(t, 100)
	mustAdd(t, m, "a", 20, 0)
	mustAdd(t, m, "b", 60, 30, "a")
	mustAdd(t, m, "c", 65, 10, "a", "b")
	mustAdd(t, m, "d", 90, 5, "c")
	return m
}

func checkAlerts(t *testing.T, got, want []Alert) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alerts mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestExampleScenario1(t *testing.T) {
	m := chain(t)
	mustLand(t, m, "a", 0, 50)
	mustLand(t, m, "b", 0, 80)
	mustLand(t, m, "c", 0, 90)
	mustLand(t, m, "d", 0, 95)
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "a", Kind: Self, K: 0, Affected: []string{"a", "b", "c", "d"}},
	})
	for _, d := range []string{"a", "b", "c", "d"} {
		if dec := mustBlame(t, m, d, 0); dec != (Decision{Root: "a", Kind: Self}) {
			t.Fatalf("Blame(%s,0)=%+v", d, dec)
		}
	}
}

func TestExampleScenario1Incremental(t *testing.T) {
	m := chain(t)
	mustLand(t, m, "a", 0, 50)
	mustLand(t, m, "b", 0, 80)
	checkAlerts(t, mustEvaluate(t, m, 85), []Alert{
		{Root: "a", Kind: Self, K: 0, Affected: []string{"a", "b", "c"}},
	})
	mustLand(t, m, "c", 0, 90)
	mustLand(t, m, "d", 0, 95)
	// 同一键再发一条只含新增者的告警。
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "a", Kind: Self, K: 0, Affected: []string{"d"}},
	})
	if dec := mustBlame(t, m, "d", 0); dec != (Decision{Root: "a", Kind: Self}) {
		t.Fatalf("Blame(d,0)=%+v", dec)
	}
}

func TestExampleScenario2(t *testing.T) {
	m := chain(t)
	mustLand(t, m, "a", 0, 20)
	mustLand(t, m, "b", 0, 60)
	mustLand(t, m, "c", 0, 70)
	mustLand(t, m, "d", 0, 75)
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "c", Kind: Unreachable, K: 0, Affected: []string{"c"}},
	})
	if dec := mustBlame(t, m, "c", 0); dec != (Decision{Root: "c", Kind: Unreachable}) {
		t.Fatalf("Blame(c,0)=%+v", dec)
	}
	if _, err := m.Blame("d", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Blame(d,0) want ErrNotFound, got %v", err)
	}
}

func TestExampleScenario3(t *testing.T) {
	m := chain(t)
	mustLand(t, m, "a", 0, 20)
	mustLand(t, m, "b", 0, 50)
	mustLand(t, m, "c", 0, 70)
	mustLand(t, m, "d", 0, 75) // d 准时，不产生告警
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "c", Kind: Self, K: 0, Affected: []string{"c"}},
	})
}

func TestExactBoundaries(t *testing.T) {
	t.Run("landed exactly at deadline is on time", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 20, 0)
		mustLand(t, m, "a", 0, 20)
		if got := mustEvaluate(t, m, 100); len(got) != 0 {
			t.Fatalf("alerts=%+v", got)
		}
		if _, err := m.Blame("a", 0); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Blame(a,0) want ErrNotFound, got %v", err)
		}
	})
	t.Run("unlanded at t==deadline is not violation", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 20, 0)
		if got := mustEvaluate(t, m, 20); len(got) != 0 {
			t.Fatalf("alerts=%+v", got)
		}
		checkAlerts(t, mustEvaluate(t, m, 21), []Alert{
			{Root: "a", Kind: Self, K: 0, Affected: []string{"a"}},
		})
	})
	t.Run("ready+dur == deadline blames Self", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 20, 0)
		mustAdd(t, m, "b", 60, 30, "a")
		mustLand(t, m, "a", 0, 30) // a 迟到
		mustLand(t, m, "b", 0, 70) // b 迟到，但 ready+dur=30+30=60<=60
		checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
			{Root: "a", Kind: Self, K: 0, Affected: []string{"a"}},
			{Root: "b", Kind: Self, K: 0, Affected: []string{"b"}},
		})
	})
}

func TestCriticalParentTieBreak(t *testing.T) {
	m := mustNew(t, 100)
	mustAdd(t, m, "a", 10, 0)
	mustAdd(t, m, "b", 10, 0)
	mustAdd(t, m, "c", 50, 40, "b", "a") // 父登记顺序 b,a，不影响字节序平局
	mustLand(t, m, "a", 0, 20)
	mustLand(t, m, "b", 0, 20) // 与 a 落地时刻相同
	mustLand(t, m, "c", 0, 95)
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "a", Kind: Self, K: 0, Affected: []string{"a", "c"}},
		{Root: "b", Kind: Self, K: 0, Affected: []string{"b"}},
	})
	if dec := mustBlame(t, m, "c", 0); dec != (Decision{Root: "a", Kind: Self}) {
		t.Fatalf("Blame(c,0)=%+v, want tie-break to a", dec)
	}
}

func TestUnlandedParentPriority(t *testing.T) {
	build := func(t *testing.T) *Monitor {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 10, 0)
		mustAdd(t, m, "z", 90, 0)
		mustAdd(t, m, "c", 50, 5, "a", "z")
		mustLand(t, m, "a", 0, 50) // a 已落地（迟到）
		return m
	}
	t.Run("unlanded parent beats landed parent", func(t *testing.T) {
		m := build(t)
		checkAlerts(t, mustEvaluate(t, m, 95), []Alert{
			{Root: "a", Kind: Self, K: 0, Affected: []string{"a"}},
			{Root: "z", Kind: Self, K: 0, Affected: []string{"c", "z"}},
		})
		if dec := mustBlame(t, m, "c", 0); dec != (Decision{Root: "z", Kind: Self}) {
			t.Fatalf("Blame(c,0)=%+v", dec)
		}
	})
	t.Run("unlanded parent not yet due gives Unreachable", func(t *testing.T) {
		m := build(t)
		checkAlerts(t, mustEvaluate(t, m, 85), []Alert{
			{Root: "a", Kind: Self, K: 0, Affected: []string{"a"}},
			{Root: "c", Kind: Unreachable, K: 0, Affected: []string{"c"}},
		})
	})
}

func TestBlameFrozenNotRecomputed(t *testing.T) {
	m := mustNew(t, 100)
	mustAdd(t, m, "p", 90, 0)
	mustAdd(t, m, "q", 50, 5, "p")
	// t=60：q 违约，p 未落地但未到期 -> (q,Unreachable) 并冻结。
	checkAlerts(t, mustEvaluate(t, m, 60), []Alert{
		{Root: "q", Kind: Unreachable, K: 0, Affected: []string{"q"}},
	})
	mustLand(t, m, "p", 0, 95) // p 之后迟到落地
	checkAlerts(t, mustEvaluate(t, m, 100), []Alert{
		{Root: "p", Kind: Self, K: 0, Affected: []string{"p"}},
	})
	// 若重算，q 会沿迟到的 p 归到 (p,Self)；冻结保证仍是 (q,Unreachable)。
	if dec := mustBlame(t, m, "q", 0); dec != (Decision{Root: "q", Kind: Unreachable}) {
		t.Fatalf("Blame(q,0)=%+v, want frozen (q,Unreachable)", dec)
	}
}

func TestBacklogAcrossPeriods(t *testing.T) {
	m := mustNew(t, 10)
	mustAdd(t, m, "a", 2, 0)
	before := m.examined
	checkAlerts(t, mustEvaluate(t, m, 35), []Alert{
		{Root: "a", Kind: Self, K: 0, Affected: []string{"a"}},
		{Root: "a", Kind: Self, K: 1, Affected: []string{"a"}},
		{Root: "a", Kind: Self, K: 2, Affected: []string{"a"}},
		{Root: "a", Kind: Self, K: 3, Affected: []string{"a"}},
	})
	if got := m.examined - before; got != 5 { // 4 个违约 + 1 个数据集
		t.Fatalf("examined=%d, want 5", got)
	}
	// 补落地后，下一期未到期，不再告警。
	for k := int64(0); k < 4; k++ {
		mustLand(t, m, "a", k, 36+k)
	}
	before = m.examined
	if got := mustEvaluate(t, m, 40); len(got) != 0 {
		t.Fatalf("alerts=%+v", got)
	}
	if got := m.examined - before; got != 1 {
		t.Fatalf("examined=%d, want 1", got)
	}
}

func TestLandRejectOrder(t *testing.T) {
	setupClock50 := func(t *testing.T) *Monitor {
		m := mustNew(t, 100)
		mustAdd(t, m, "pa", 10, 0)
		mustLand(t, m, "pa", 0, 50)
		return m
	}
	setupLanded := func(t *testing.T) *Monitor {
		m := mustNew(t, 100)
		mustAdd(t, m, "pa", 10, 0)
		mustLand(t, m, "pa", 0, 10)
		return m
	}
	setupQ := func(t *testing.T) *Monitor {
		m := mustNew(t, 100)
		mustAdd(t, m, "pa", 10, 0)
		mustAdd(t, m, "q", 50, 0, "pa")
		mustLand(t, m, "pa", 0, 10)
		mustLand(t, m, "q", 0, 20)
		return m
	}
	setupQ3 := func(t *testing.T) *Monitor {
		m := mustNew(t, 100)
		mustAdd(t, m, "pa", 10, 0)
		mustAdd(t, m, "pb", 10, 0)
		mustAdd(t, m, "pc", 10, 0)
		mustAdd(t, m, "q", 50, 0, "pc", "pa", "pb")
		return m
	}
	cases := []struct {
		name     string
		setup    func(t *testing.T) *Monitor
		d        string
		k        int64
		now      int64
		want     error
		msgHas   string
		msgLacks string
	}{
		{"invalid beats clock and unknown", setupClock50, "ghost", -1, 49, ErrInvalid, "", ""},
		{"invalid now beats clock", setupClock50, "ghost", 0, -1, ErrInvalid, "", ""},
		{"clock beats unknown dataset", setupClock50, "ghost", 0, 49, ErrClock, "", ""},
		{"unknown dataset", setupClock50, "ghost", 0, 60, ErrNoSuchDataset, "", ""},
		{"already", setupLanded, "pa", 0, 60, ErrAlready, "", ""},
		{"out of order beats too early", setupLanded, "pa", 5, 10, ErrOutOfOrder, "", ""},
		{"too early beats upstream missing", setupQ, "q", 1, 50, ErrTooEarly, "", ""},
		{"upstream missing smallest name", setupQ3, "q", 0, 10, ErrUpstreamMissing, "pa", "pb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(t)
			err := m.Land(tc.d, tc.k, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if tc.msgHas != "" && !strings.Contains(err.Error(), tc.msgHas) {
				t.Fatalf("error %q should mention %q", err, tc.msgHas)
			}
			if tc.msgLacks != "" && strings.Contains(err.Error(), tc.msgLacks) {
				t.Fatalf("error %q should not mention %q", err, tc.msgLacks)
			}
		})
	}
}

func TestAddDatasetRejectOrder(t *testing.T) {
	t.Run("invalid beats frozen", func(t *testing.T) {
		m := mustNew(t, 100)
		mustEvaluate(t, m, 0) // 冻结
		if err := m.AddDataset("", 0, 0, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("want ErrInvalid, got %v", err)
		}
	})
	t.Run("frozen beats exists", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 50, 0)
		mustEvaluate(t, m, 0)
		if err := m.AddDataset("a", 50, 0, nil); !errors.Is(err, ErrFrozen) {
			t.Fatalf("want ErrFrozen, got %v", err)
		}
	})
	t.Run("land freezes too", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 10, 0)
		mustLand(t, m, "a", 0, 10)
		if err := m.AddDataset("b", 10, 0, nil); !errors.Is(err, ErrFrozen) {
			t.Fatalf("want ErrFrozen, got %v", err)
		}
	})
	t.Run("exists beats missing parent", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 50, 0)
		if err := m.AddDataset("a", 50, 0, []string{"ghost"}); !errors.Is(err, ErrExists) {
			t.Fatalf("want ErrExists, got %v", err)
		}
	})
	t.Run("param boundaries accepted", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "lo", 1, 0)
		mustAdd(t, m, "hi", 100, 100)
		parents := make([]string, 0, 8)
		for i := 0; i < 8; i++ {
			name := fmt.Sprintf("p%d", i)
			mustAdd(t, m, name, 1, 0)
			parents = append(parents, name)
		}
		mustAdd(t, m, "wide", 50, 0, parents...)
	})
	t.Run("blame does not freeze", func(t *testing.T) {
		m := mustNew(t, 100)
		mustAdd(t, m, "a", 50, 0)
		if _, err := m.Blame("a", 0); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		mustAdd(t, m, "b", 50, 0)
	})
}

func TestClock(t *testing.T) {
	m := mustNew(t, 100)
	mustAdd(t, m, "a", 50, 0)
	mustLand(t, m, "a", 0, 50)
	if err := m.Land("a", 1, 49); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if _, err := m.Evaluate(49); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	// 被拒绝的操作不推进时钟：50 仍可用。
	mustEvaluate(t, m, 50)
	mustLand(t, m, "a", 1, 150)
	mustEvaluate(t, m, 150) // 等于时钟不算回退
	if _, err := m.Evaluate(149); !errors.Is(err, ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if _, err := m.Evaluate(MaxNow + 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if err := m.Land("a", 2, MaxNow+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestRejectedOpsKeepState(t *testing.T) {
	m := mustNew(t, 100)
	mustAdd(t, m, "pa", 10, 0)
	if err := m.Land("ghost", 0, 0); !errors.Is(err, ErrNoSuchDataset) {
		t.Fatalf("want ErrNoSuchDataset, got %v", err)
	}
	if err := m.Land("pa", 1, 0); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("want ErrOutOfOrder, got %v", err)
	}
	if _, err := m.Evaluate(-1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	// 没有被接受的操作，注册表未冻结、时钟未启动。
	mustAdd(t, m, "pb", 20, 0)
	mustLand(t, m, "pa", 0, 10)
	if err := m.Land("pa", 0, 11); !errors.Is(err, ErrAlready) {
		t.Fatalf("want ErrAlready, got %v", err)
	}
	// 被拒绝的 Land 不推进时钟：10 仍可用。
	mustLand(t, m, "pa", 1, 100)
}

func TestExaminedIndependentOfClosedHistory(t *testing.T) {
	for _, periods := range []int64{10, 1000} {
		t.Run(fmt.Sprintf("periods=%d", periods), func(t *testing.T) {
			m := mustNew(t, 10)
			for i := 0; i < 5; i++ {
				mustAdd(t, m, fmt.Sprintf("d%d", i), 5, 1)
			}
			for k := int64(0); k < periods; k++ {
				for i := 0; i < 5; i++ {
					mustLand(t, m, fmt.Sprintf("d%d", i), k, k*10+5) // 恰准时
				}
			}
			before := m.examined
			if got := mustEvaluate(t, m, periods*10+5); len(got) != 0 {
				t.Fatalf("alerts=%+v", got)
			}
			// 每个数据集只考察其最小未结案期：与已结案历史期数无关。
			if got := m.examined - before; got != 5 {
				t.Fatalf("examined=%d, want 5", got)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	m := mustNew(t, 1)
	mustAdd(t, m, "a", 1, 0)
	const periods = 100
	const now = int64(150) // 落地时刻与评估时刻：0..99 期均迟到，100..148 期未落地违约
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		all    []Alert
		failed bool
	)
	// 8 个 worker 并发落地各自的期号，now 固定（不回退）。
	for w := int64(0); w < 8; w++ {
		wg.Add(1)
		go func(w int64) {
			defer wg.Done()
			for k := w; k < periods; k += 8 {
				for {
					err := m.Land("a", k, now)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrOutOfOrder) {
						mu.Lock()
						failed = true
						mu.Unlock()
						t.Errorf("Land(a,%d): %v", k, err)
						break
					}
				}
			}
		}(w)
	}
	// 并发评估与查询。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				alerts, err := m.Evaluate(now)
				if err != nil {
					t.Errorf("Evaluate: %v", err)
					return
				}
				mu.Lock()
				all = append(all, alerts...)
				mu.Unlock()
				_, _ = m.Blame("a", int64(j))
			}
		}()
	}
	wg.Wait()
	if failed {
		t.Fatal("unexpected land errors under concurrency")
	}
	alerts, err := m.Evaluate(now)
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, alerts...)
	// 每个违约 (a,k) 恰出现在一条告警的 Affected 中（含未落地的积压期）。
	seen := make(map[int64]int)
	for _, a := range all {
		if a.Root != "a" || a.Kind != Self {
			t.Fatalf("unexpected alert %+v", a)
		}
		for _, d := range a.Affected {
			if d != "a" {
				t.Fatalf("unexpected affected %q", d)
			}
			seen[a.K]++
		}
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("period %d alerted %d times, want 1", k, n)
		}
	}
	for k := int64(0); k < periods; k++ {
		if seen[k] != 1 {
			t.Fatalf("period %d never alerted", k)
		}
		if dec := mustBlame(t, m, "a", k); dec != (Decision{Root: "a", Kind: Self}) {
			t.Fatalf("Blame(a,%d)=%+v", k, dec)
		}
	}
}
