package blame

import (
	"errors"
	"strings"
	"testing"

	"ontology/dag"
)

func mustAdd(t *testing.T, a *Attributor, name string, off, dur int64, parents ...string) {
	t.Helper()
	if err := a.AddDataset(name, off, dur, parents); err != nil {
		t.Fatalf("AddDataset(%s): %v", name, err)
	}
}

func mustLand(t *testing.T, a *Attributor, name string, k, now int64) {
	t.Helper()
	if err := a.Land(name, k, now); err != nil {
		t.Fatalf("Land(%s,%d,%d): %v", name, k, now, err)
	}
}

func itoa64(k int64) string {
	if k == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for k > 0 {
		i--
		b[i] = byte('0' + k%10)
		k /= 10
	}
	return string(b[i:])
}

func joinAffected(al []Alert) string {
	var sb strings.Builder
	for _, x := range al {
		sb.WriteString(x.Root.Dataset)
		sb.WriteString(x.Root.Kind.String())
		sb.WriteString(itoa64(x.K))
		sb.WriteString("=")
		sb.WriteString(strings.Join(x.Affected, ","))
		sb.WriteString(";")
	}
	return sb.String()
}

func setupABCD(t *testing.T) *Attributor {
	t.Helper()
	a, err := New(100)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, a, "a", 20, 0)
	mustAdd(t, a, "b", 60, 30, "a")
	mustAdd(t, a, "c", 65, 10, "a", "b")
	mustAdd(t, a, "d", 90, 5, "c")
	return a
}

// 题目给出的三个基准情形。
func TestSpecExamples(t *testing.T) {
	t.Run("case1 chain to a", func(t *testing.T) {
		a := setupABCD(t)
		mustLand(t, a, "a", 0, 50)
		mustLand(t, a, "b", 0, 80)
		mustLand(t, a, "c", 0, 90)
		mustLand(t, a, "d", 0, 95)
		al, err := a.Evaluate(100)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := joinAffected(al), "aSelf0=a,b,c,d;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("case1 incremental with frozen blame", func(t *testing.T) {
		a := setupABCD(t)
		mustLand(t, a, "a", 0, 50)
		mustLand(t, a, "b", 0, 80)
		al1, err := a.Evaluate(85)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := joinAffected(al1), "aSelf0=a,b,c;"; got != want {
			t.Fatalf("first eval got %q want %q", got, want)
		}
		mustLand(t, a, "c", 0, 90)
		mustLand(t, a, "d", 0, 95)
		al2, err := a.Evaluate(100)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := joinAffected(al2), "aSelf0=d;"; got != want {
			t.Fatalf("second eval got %q want %q", got, want)
		}
		for _, d := range []string{"a", "b", "c", "d"} {
			r, err := a.Blame(d, 0)
			if err != nil || r.Dataset != "a" || r.Kind != Self {
				t.Fatalf("Blame(%s)=%+v,%v want a/Self", d, r, err)
			}
		}
	})

	t.Run("case2 unreachable", func(t *testing.T) {
		a := setupABCD(t)
		mustLand(t, a, "a", 0, 20)
		mustLand(t, a, "b", 0, 60)
		mustLand(t, a, "c", 0, 70)
		mustLand(t, a, "d", 0, 75)
		al, err := a.Evaluate(100)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := joinAffected(al), "cUnreachable0=c;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("case3 self with late upstream", func(t *testing.T) {
		a := setupABCD(t)
		mustLand(t, a, "a", 0, 20)
		mustLand(t, a, "b", 0, 50)
		mustLand(t, a, "c", 0, 70)
		mustLand(t, a, "d", 0, 75)
		al, err := a.Evaluate(100)
		if err != nil {
			t.Fatal(err)
		}
		// b 的 deadline=60，50 落地准时；c: ready=50,50+10=60<=65 上游给足时间 => c/Self；
		// d 的 deadline=90，75 落地准时。
		if got, want := joinAffected(al), "cSelf0=c;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

func TestBoundaryConditions(t *testing.T) {
	t.Run("land exactly at deadline is on time", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "a", 20, 0)
		mustLand(t, a, "a", 0, 20)
		al, err := a.Evaluate(20)
		if err != nil || len(al) != 0 {
			t.Fatalf("al=%v err=%v", al, err)
		}
	})

	t.Run("unlanded exactly at deadline is not violation", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "a", 20, 0)
		al, err := a.Evaluate(20)
		if err != nil || len(al) != 0 {
			t.Fatalf("al=%v err=%v", al, err)
		}
		al, _ = a.Evaluate(21)
		if len(al) != 1 || al[0].Root.Dataset != "a" {
			t.Fatalf("al=%v", al)
		}
	})

	t.Run("ready+dur exactly deadline => Self", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "a", 20, 0)
		mustAdd(t, a, "b", 60, 40, "a")
		mustLand(t, a, "a", 0, 20)
		mustLand(t, a, "b", 0, 61)
		al, _ := a.Evaluate(100)
		if got, want := joinAffected(al), "bSelf0=b;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("critical parent tie picks smaller name", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "p1", 20, 0)
		mustAdd(t, a, "p2", 20, 0)
		// 两个父都 50 落地（同样晚），c 的 deadline 80，50+30=80<=80 => c 为 Self；
		// 关键父选择的并列规则通过“两父同时未落地”的下一个用例验证。
		mustAdd(t, a, "c", 80, 30, "p1", "p2")
		mustLand(t, a, "p1", 0, 50)
		mustLand(t, a, "p2", 0, 50)
		mustLand(t, a, "c", 0, 90)
		al, _ := a.Evaluate(100)
		// 输出按 (k, root 名字节序) 排序：c < p1 < p2。
		if got, want := joinAffected(al), "cSelf0=c;p1Self0=p1;p2Self0=p2;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("unlanded parents tie picks smallest name", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "p1", 10, 0)
		mustAdd(t, a, "p2", 10, 0)
		mustAdd(t, a, "c", 40, 50, "p1", "p2") // 两父均未落地，关键父取 p1
		al, _ := a.Evaluate(50)
		// p1、p2、c 均违约；c 的关键父为未落地中名字最小者 p1。
		var cRoot Root
		for _, x := range al {
			for _, d := range x.Affected {
				if d == "c" {
					cRoot = x.Root
				}
			}
		}
		if cRoot.Dataset != "p1" || cRoot.Kind != Self {
			t.Fatalf("c root=%+v want p1/Self; alerts=%s", cRoot, joinAffected(al))
		}
	})

	t.Run("all upstream on time but insufficient => Unreachable", func(t *testing.T) {
		a, _ := New(100)
		mustAdd(t, a, "p", 10, 0)
		mustAdd(t, a, "c", 50, 50, "p")
		mustLand(t, a, "p", 0, 10)
		mustLand(t, a, "c", 0, 60)
		al, _ := a.Evaluate(100)
		if got, want := joinAffected(al), "cUnreachable0=c;"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

func TestFreezeNoRecompute(t *testing.T) {
	a, _ := New(100)
	mustAdd(t, a, "p", 10, 0)
	mustAdd(t, a, "c", 50, 20, "p")
	// 60 时 p、c 均未落地且都违约：c 的关键父 p（未落地）违约 -> 归 p/Self。
	al, err := a.Evaluate(60)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinAffected(al), "pSelf0=c,p;"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// 冻结后补落地不重算，也不产生新告警。
	mustLand(t, a, "p", 0, 60)
	mustLand(t, a, "c", 0, 70)
	al2, _ := a.Evaluate(80)
	if len(al2) != 0 {
		t.Fatalf("no new alerts expected, got %s", joinAffected(al2))
	}
	r, err := a.Blame("c", 0)
	if err != nil || r.Dataset != "p" || r.Kind != Self {
		t.Fatalf("frozen root changed: %+v %v", r, err)
	}
	if _, err := a.Blame("c", 9); !errors.Is(err, dag.ErrNotAlerted) {
		t.Fatalf("want ErrNotAlerted, got %v", err)
	}
}

func TestIncrementalOnlyNewAffected(t *testing.T) {
	a, _ := New(100)
	mustAdd(t, a, "p", 10, 0)
	mustAdd(t, a, "c1", 80, 5, "p")
	mustAdd(t, a, "c2", 85, 5, "p")
	// 82 时 p、c1 违约，c2 未违约（82<=85）。
	al1, _ := a.Evaluate(82)
	if got, want := joinAffected(al1), "pSelf0=c1,p;"; got != want {
		t.Fatalf("first got %q want %q", got, want)
	}
	// 90 时 c2 新违约，同键 (p,Self,0) 再发一条，只含 c2。
	al2, _ := a.Evaluate(90)
	if got, want := joinAffected(al2), "pSelf0=c2;"; got != want {
		t.Fatalf("second got %q want %q", got, want)
	}
}

func TestBacklogAcrossPeriods(t *testing.T) {
	a, _ := New(10)
	mustAdd(t, a, "x", 1, 0)
	mustLand(t, a, "x", 0, 5)
	mustLand(t, a, "x", 1, 15)
	mustLand(t, a, "x", 2, 25)
	al, err := a.Evaluate(30)
	if err != nil {
		t.Fatal(err)
	}
	if len(al) != 3 {
		t.Fatalf("want 3 alerts, got %v", al)
	}
	for i := int64(0); i < 3; i++ {
		if al[i].K != i || len(al[i].Affected) != 1 || al[i].Affected[0] != "x" {
			t.Fatalf("alert[%d]=%+v", i, al[i])
		}
	}
	if got := a.Examined(); got > 3+1 {
		t.Fatalf("examined=%d exceeds bound newViolations+datasets", got)
	}
	// 再次评估不重扫历史：examined 不增加（无新违约、缺口未变）。
	before := a.Examined()
	if _, err := a.Evaluate(31); err != nil {
		t.Fatal(err)
	}
	if got := a.Examined(); got != before {
		t.Fatalf("examined grew on re-evaluation: %d -> %d", before, got)
	}
}

func TestExaminedBoundedVsHistory(t *testing.T) {
	// 两档历史（10 期 / 1000 期，均准时）下，“再做一次无新违约的评估”
	// 所新增的考察数都必须为 0，且总额始终只与 新增违约数+数据集数 有关：
	// 首次结案时每个已落地期考察一次（那一期它们正是新出现的、要么准时结案要么
	// 成为新违约），之后任何评估都不得从第 0 期重扫。
	run := func(t *testing.T, periods int) (total, added int64) {
		a, _ := New(100)
		mustAdd(t, a, "x", 10, 0)
		for k := 0; k < periods; k++ {
			mustLand(t, a, "x", int64(k), int64(k*100+10))
		}
		if al, err := a.Evaluate(int64(periods * 100)); err != nil || len(al) != 0 {
			t.Fatalf("al=%v err=%v", al, err)
		}
		total = a.Examined()
		// 在新一期未到截止前再次评估：连缺口都不该被考察（now 恰等于/早于截止）。
		if _, err := a.Evaluate(int64(periods * 100)); err != nil {
			t.Fatal(err)
		}
		added = a.Examined() - total
		return total, added
	}
	_, add10 := run(t, 10)
	_, add1000 := run(t, 1000)
	if add10 != 0 || add1000 != 0 {
		t.Fatalf("re-evaluation re-scanned history: added 10-periods=%d 1000-periods=%d", add10, add1000)
	}
}
