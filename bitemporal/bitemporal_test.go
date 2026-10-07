package bitemporal

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func iv(s, e int64, end BoundMode) *Interval {
	return &Interval{Start: s, End: e, StartBound: Closed, EndBound: end}
}

// TestPathEquivalence 覆盖连续多次迁移与直接迁移的结果一致性。
func TestPathEquivalence(t *testing.T) {
	reg := DefaultRegistry()

	// 1) 先降级后升级，路径终点为双时态半开，且无“更弱”的中间版本：
	//    v1(双时态,闭) -> v2(双时态,半开) -> v1(双时态,闭)，
	//    必须与 v1->v1 直迁一致：不产生额外损失，也不恢复信息。
	r := bothRecord("r", 10, 20, Closed)
	direct := Migrate(reg, r, "v1", "v1")
	path := MigratePath(reg, r, []string{"v1", "v2", "v1"})
	if path.Verdict.Status != direct.Verdict.Status {
		t.Fatalf("path status %s != direct %s", path.Verdict.Status, direct.Verdict.Status)
	}
	if !reflect.DeepEqual(path.Record, direct.Record) {
		t.Fatalf("path record %+v != direct %+v", path.Record, direct.Record)
	}
	if len(path.Verdict.Lost) != 0 {
		t.Fatalf("round trip through equally-capable versions must not report loss: %+v", path.Verdict.Lost)
	}

	// 2) 先升级后降级：v3(仅事务,闭) -> v1(双时态,闭) -> v3(仅事务,闭)。
	//    有效时间轴在升级跳被固定填充、在降级跳被丢弃；最终记录必须与
	//    v3->v3 直迁一致，且路径报告该轴曾发生“填充后丢弃”的信息损失记账，
	//    不得恢复出任何本不应保留的有效时间信息。
	r2 := txOnly("r2", 5, 8, Closed)
	direct2 := Migrate(reg, r2, "v3", "v3")
	path2 := MigratePath(reg, r2, []string{"v3", "v1", "v3"})
	if !reflect.DeepEqual(path2.Record, direct2.Record) {
		t.Fatalf("upgrade-then-downgrade record differs: path=%+v direct=%+v", path2.Record, direct2.Record)
	}
	if path2.Record.Valid != nil {
		t.Fatalf("valid time must not be resurrected after downgrade")
	}

	// 3) 关键不变式：任何路径迁移都不得让原始记录发生变化。
	snap := *r2
	_ = MigratePath(reg, r2, []string{"v3", "v1", "v2", "v3", "v4"})
	if !reflect.DeepEqual(*r2, snap) {
		t.Fatalf("input record mutated by path migration")
	}

	// 4) 经过丢弃轴的中间版本再回到双时态：只能得到固定填充值。
	r3 := bothRecord("r3", 100, 200, Closed)
	p3 := MigratePath(reg, r3, []string{"v1", "v3", "v1"})
	if p3.Record == nil {
		t.Fatalf("expected migrated record, got nil (%+v)", p3.Verdict)
	}
	wantFill := DefaultInterval(FormatVersion{Records: map[Axis]AxisSpec{
		ValidTime: {Recorded: true, StartBound: Closed, EndBound: Closed},
	}}.AxisSpecOf(ValidTime))
	if !reflect.DeepEqual(*p3.Record.Valid, wantFill) {
		t.Fatalf("re-added valid axis must use the fixed eternity fill, got %+v want %+v", *p3.Record.Valid, wantFill)
	}
	if !reflect.DeepEqual(*p3.Record.Transaction, *iv(100, 200, Closed)) {
		t.Fatalf("transaction axis must survive, got %+v", *p3.Record.Transaction)
	}

	// 5) 路径上出现硬错误（边界不兼容）时整体失败。
	r4 := bothRecord("r4", 1, MaxFinite, Closed)
	p4 := MigratePath(reg, r4, []string{"v1", "v2", "v3"})
	if p4.Record != nil || p4.Verdict.Status != StatusIncompatible {
		t.Fatalf("path with incompatible hop must fail, got %+v", p4.Verdict)
	}
}

// TestBatchLinearComplexity 以加倍法复核单批判定开销随记录总数大致成比例。
func TestBatchLinearComplexity(t *testing.T) {
	reg := DefaultRegistry()
	mkBatch := func(n int) []*Record {
		rs := make([]*Record, n)
		for i := range rs {
			rs[i] = bothRecord(fmt.Sprintf("r%d", i), int64(i%100), int64(i%100)+10, Closed)
		}
		return rs
	}
	measure := func(n int) time.Duration {
		rs := mkBatch(n)
		// 预热
		JudgeBatch(reg, rs[:100], "v1", "v2")
		start := time.Now()
		vs := JudgeBatch(reg, rs, "v1", "v2")
		d := time.Since(start)
		if len(vs) != n {
			t.Fatalf("batch size mismatch")
		}
		return d
	}
	const base = 20000
	d1 := measure(base)
	d2 := measure(base * 4)
	ratio := float64(d2) / float64(d1)
	t.Logf("n=%d d=%v ; n=%d d=%v ; ratio=%.2f (linear expectation ~4)", base, d1, base*4, d2, ratio)
	// 宽松上界，避免在高负载 CI 上抖动误报；比值显著小于二次曲线的 ~16。
	if ratio > 10 {
		t.Fatalf("work grows faster than linear: 4x records took %.2fx time", ratio)
	}
}

func pickName(names []string, rng *rand.Rand) string {
	if rng.Intn(20) == 0 {
		return "vX-unknown"
	}
	return names[rng.Intn(len(names))]
}

// randomRecordMatching 生成与所声明源版本一致的随机记录（含部分坏记录）。
func randomRecordMatching(rng *rand.Rand, reg *Registry, src string) *Record {
	r := &Record{ID: fmt.Sprintf("rec-%d", rng.Int63())}
	ver, ok := reg.Get(src)
	mk := func(end BoundMode) *Interval {
		lo := int64(rng.Intn(40)) - 10
		hi := lo + int64(rng.Intn(40))
		if rng.Intn(15) == 0 { // 注入坏区间：起点晚于终点
			return iv(hi, lo, end)
		}
		if rng.Intn(20) == 0 { // 注入域末闭区间（触发边界不兼容）
			return iv(lo, MaxFinite, Closed)
		}
		return iv(lo, hi, end)
	}
	if !ok {
		// 未知版本：随机给数据
		if rng.Intn(2) == 0 {
			end := Closed
			if rng.Intn(2) == 0 {
				end = HalfOpen
			}
			r.Valid = mk(end)
		}
		if rng.Intn(2) == 0 {
			end := Closed
			if rng.Intn(2) == 0 {
				end = HalfOpen
			}
			r.Transaction = mk(end)
		}
		return r
	}
	for _, a := range []Axis{ValidTime, TransactionTime} {
		if ver.AxisSpecOf(a).Recorded {
			end := ver.AxisSpecOf(a).EndBound
			switch a {
			case ValidTime:
				r.Valid = mk(end)
			case TransactionTime:
				r.Transaction = mk(end)
			}
		}
	}
	return r
}

type caseLog struct {
	N            int
	Src, Dst     string
	Record       string
	Status       Status
	Lost, Filled []Axis
	Reasons      []string
	RefMatch     bool
}

// TestRandomDifferential 在大量随机场景上与朴素参照模型对照，
// 并把每次判定的输入、输出与依据写入日志（BITEMPORAL_LOG 指定文件）。
func TestRandomDifferential(t *testing.T) {
	reg := DefaultRegistry()
	rng := rand.New(rand.NewSource(20261007))
	names := reg.Names()
	const n = 4000
	logs := make([]caseLog, 0, n)

	for k := 0; k < n; k++ {
		src := pickName(names, rng)
		dst := pickName(names, rng)
		r := randomRecordMatching(rng, reg, src)
		got := Judge(reg, r, src, dst)
		refStatus, refLost, refFilled, _ := naiveJudge(reg, r, src, dst)
		match := got.Status == refStatus &&
			reflect.DeepEqual(lostAxes(got), refLost) &&
			reflect.DeepEqual(filledAxes(got), refFilled)
		logs = append(logs, caseLog{
			N: k, Src: src, Dst: dst,
			Record: fmt.Sprintf("valid=%+v tx=%+v", r.Valid, r.Transaction),
			Status: got.Status, Lost: lostAxes(got), Filled: filledAxes(got),
			Reasons: got.Reasons, RefMatch: match,
		})
		if !match {
			t.Fatalf("case %d mismatch: impl=%s/%v/%v ref=%s/%v/%v (%s->%s %s)",
				k, got.Status, lostAxes(got), filledAxes(got),
				refStatus, refLost, refFilled, src, dst, logs[k].Record)
		}
	}

	// 统计四类结论确实都在随机场景中出现，避免测试被单一类别“空转”通过。
	counts := map[Status]int{}
	for _, l := range logs {
		counts[l.Status]++
	}
	for _, s := range []Status{StatusCompatible, StatusInfoLoss, StatusIncompatible, StatusRecordInvalid, StatusUnknownVersion} {
		if counts[s] == 0 {
			t.Fatalf("status %s never observed in %d random cases", s, n)
		}
	}
	t.Logf("status distribution over %d cases: %+v", n, counts)

	if path := os.Getenv("BITEMPORAL_LOG"); path != "" {
		var sb strings.Builder
		for _, l := range logs {
			fmt.Fprintf(&sb, "#%d %s->%s | %s | status=%s lost=%v filled=%v match=%v | %s\n",
				l.N, l.Src, l.Dst, l.Record, l.Status, l.Lost, l.Filled, l.RefMatch, strings.Join(l.Reasons, "; "))
		}
		if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
			t.Fatalf("write log: %v", err)
		}
	}
}

// TestDeterminismAndConcurrency 对同一记录反复/并发判定，结果必须完全一致，
// 且被判定记录不得被修改。
func TestDeterminismAndConcurrency(t *testing.T) {
	reg := DefaultRegistry()
	original := bothRecord("r", 10, 20, Closed)
	snapshot := *original

	first := Judge(reg, original, "v1", "v4")
	for i := 0; i < 50; i++ {
		if got := Judge(reg, original, "v1", "v4"); !reflect.DeepEqual(got, first) {
			t.Fatalf("iteration %d verdict drifted: %+v vs %+v", i, got, first)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got := Judge(reg, original, "v1", "v4")
				if !reflect.DeepEqual(got, first) {
					errs <- fmt.Errorf("concurrent verdict mismatch: %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(*original, snapshot) {
		t.Fatalf("record was mutated: %+v vs %+v", *original, snapshot)
	}
}

// TestBoundaryMembership 覆盖边界闭合方式不一致时归属结果改变/不改变两种情形。
func TestBoundaryMembership(t *testing.T) {
	reg := DefaultRegistry()

	t.Run("convention differs but membership unchanged", func(t *testing.T) {
		r := bothRecord("r", 10, 20, Closed)
		v := Judge(reg, r, "v1", "v2")
		if v.Status != StatusCompatible {
			t.Fatalf("want compatible, got %s (%v)", v.Status, v.Reasons)
		}
		m := Migrate(reg, r, "v1", "v2")
		if !reflect.DeepEqual(*m.Record.Valid, *iv(10, 21, HalfOpen)) {
			t.Fatalf("converted interval = %+v", *m.Record.Valid)
		}
		for _, tp := range []int64{9, 10, 20, 21} {
			if iv(10, 20, Closed).Contains(tp) != iv(10, 21, HalfOpen).Contains(tp) {
				t.Fatalf("membership changed at %d", tp)
			}
		}
	})

	t.Run("membership changes at domain max => incompatible", func(t *testing.T) {
		r := bothRecord("r", 10, MaxFinite, Closed)
		v := Judge(reg, r, "v1", "v2")
		if v.Status != StatusIncompatible {
			t.Fatalf("want incompatible, got %s (%v)", v.Status, v.Reasons)
		}
		var found bool
		for _, bv := range v.Boundary {
			if bv.Differs && bv.HasWitness && bv.Witness == MaxFinite {
				found = true
			}
		}
		if !found {
			t.Fatalf("want witness at MaxFinite, boundary=%+v", v.Boundary)
		}
	})

	t.Run("empty half-open cannot become closed => incompatible", func(t *testing.T) {
		r := bothRecord("r", 10, 10, HalfOpen)
		v := Judge(reg, r, "v2", "v1")
		if v.Status != StatusIncompatible {
			t.Fatalf("want incompatible, got %s (%v)", v.Status, v.Reasons)
		}
	})

	t.Run("half-open to closed normal interval unchanged", func(t *testing.T) {
		r := bothRecord("r", 10, 21, HalfOpen)
		v := Judge(reg, r, "v2", "v1")
		if v.Status != StatusCompatible {
			t.Fatalf("want compatible, got %s (%v)", v.Status, v.Reasons)
		}
		m := Migrate(reg, r, "v2", "v1")
		if !reflect.DeepEqual(*m.Record.Valid, *iv(10, 20, Closed)) {
			t.Fatalf("converted = %+v", *m.Record.Valid)
		}
	})
}

// TestRecordInvalid 覆盖两条轴各自的不自洽区间及优先级。
func TestRecordInvalid(t *testing.T) {
	reg := DefaultRegistry()
	bad := &Record{ID: "bad", Valid: iv(20, 10, Closed), Transaction: iv(0, 5, Closed)}
	v := Judge(reg, bad, "v1", "v3")
	if v.Status != StatusRecordInvalid || !strings.Contains(strings.Join(v.Reasons, " "), string(ValidTime)) {
		t.Fatalf("valid axis inconsistency: %+v", v)
	}

	bad2 := &Record{ID: "bad2", Valid: iv(0, 5, Closed), Transaction: iv(9, 1, HalfOpen)}
	if v2 := Judge(reg, bad2, "v2", "v4"); v2.Status != StatusRecordInvalid {
		t.Fatalf("transaction axis inconsistency: %s", v2.Status)
	}

	// 自洽性优先于版本不可识别。
	if v3 := Judge(reg, bad, "v999", "v1000"); v3.Status != StatusRecordInvalid {
		t.Fatalf("record invalid must outrank unknown version, got %s", v3.Status)
	}
	// 自洽性优先于边界不兼容。
	bad3 := &Record{ID: "bad3", Valid: iv(MaxFinite, 1, Closed), Transaction: iv(0, 5, Closed)}
	if v4 := Judge(reg, bad3, "v1", "v2"); v4.Status != StatusRecordInvalid {
		t.Fatalf("record invalid must outrank boundary incompatibility, got %s", v4.Status)
	}
}

// TestPrecedenceOrder 验证边界不兼容优先于信息损失，以及未知版本判定。
func TestPrecedenceOrder(t *testing.T) {
	reg := DefaultRegistry()
	// v1(双时态,闭) -> v6(仅有效,半开)：事务轴会丢，且有效轴右端到域末边界不兼容。
	r := &Record{ID: "r", Valid: iv(10, MaxFinite, Closed), Transaction: iv(10, 20, Closed)}
	if v := Judge(reg, r, "v1", "v6"); v.Status != StatusIncompatible {
		t.Fatalf("boundary incompatibility must outrank info loss, got %s", v.Status)
	}
	ok := bothRecord("r", 10, 20, Closed)
	if v := Judge(reg, ok, "v1", "v42"); v.Status != StatusUnknownVersion {
		t.Fatalf("want unknown target, got %s", v.Status)
	}
	if v := Judge(reg, ok, "v42", "v1"); v.Status != StatusUnknownVersion {
		t.Fatalf("want unknown source, got %s", v.Status)
	}
}

func bothRecord(id string, s, e int64, end BoundMode) *Record {
	return &Record{ID: id, Valid: iv(s, e, end), Transaction: iv(s, e, end)}
}

func txOnly(id string, s, e int64, end BoundMode) *Record {
	return &Record{ID: id, Transaction: iv(s, e, end)}
}

func validOnly(id string, s, e int64, end BoundMode) *Record {
	return &Record{ID: id, Valid: iv(s, e, end)}
}

func noneRecord(id string) *Record { return &Record{ID: id} }

func sortedAxes(in []Axis) []Axis {
	out := append([]Axis(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func lostAxes(v Verdict) []Axis {
	out := make([]Axis, 0, len(v.Lost))
	for _, l := range v.Lost {
		out = append(out, l.Axis)
	}
	return sortedAxes(out)
}

func filledAxes(v Verdict) []Axis {
	out := make([]Axis, 0, len(v.Filled))
	for _, f := range v.Filled {
		out = append(out, f.Axis)
	}
	return sortedAxes(out)
}

// TestAxisPresenceCombinations 覆盖两条时间轴各自记录与否的全部 4 种组合。
func TestAxisPresenceCombinations(t *testing.T) {
	reg := DefaultRegistry()
	cases := []struct {
		src, dst string
		rec      func() *Record
		want     Status
		wantLost []Axis
	}{
		{"v1", "v3", func() *Record { return bothRecord("r", 10, 20, Closed) }, StatusInfoLoss, []Axis{ValidTime}},
		{"v1", "v5", func() *Record { return bothRecord("r", 10, 20, Closed) }, StatusInfoLoss, []Axis{TransactionTime}},
		{"v2", "v4", func() *Record { return bothRecord("r", 10, 20, HalfOpen) }, StatusInfoLoss, []Axis{ValidTime}},
		{"v3", "v1", func() *Record { return txOnly("r", 10, 20, Closed) }, StatusCompatible, nil},
		{"v4", "v2", func() *Record { return txOnly("r", 10, 20, HalfOpen) }, StatusCompatible, nil},
		{"v5", "v1", func() *Record { return validOnly("r", 10, 20, Closed) }, StatusCompatible, nil},
		{"v1", "v7", func() *Record { return bothRecord("r", 10, 20, Closed) }, StatusInfoLoss, []Axis{TransactionTime, ValidTime}},
		{"v3", "v7", func() *Record { return txOnly("r", 10, 20, Closed) }, StatusInfoLoss, []Axis{TransactionTime}},
		{"v7", "v1", func() *Record { return noneRecord("r") }, StatusCompatible, nil},
		{"v2", "v2", func() *Record { return bothRecord("r", 10, 20, HalfOpen) }, StatusCompatible, nil},
	}
	for _, c := range cases {
		t.Run(c.src+"->"+c.dst, func(t *testing.T) {
			r := c.rec()
			v := Judge(reg, r, c.src, c.dst)
			if v.Status != c.want {
				t.Fatalf("status = %s, want %s; reasons=%v", v.Status, c.want, v.Reasons)
			}
			if got := lostAxes(v); !reflect.DeepEqual(got, sortedAxes(c.wantLost)) {
				t.Fatalf("lost = %v, want %v", got, c.wantLost)
			}
			if c.src == "v2" && c.dst == "v4" {
				if len(v.Lost) != 1 || v.Lost[0].Axis != ValidTime {
					t.Fatalf("must report loss of valid_time specifically, got %+v", v.Lost)
				}
				if !strings.Contains(strings.Join(v.Reasons, " "), string(ValidTime)) {
					t.Fatalf("reasons must name valid_time: %v", v.Reasons)
				}
			}
			if len(v.Filled) > 0 {
				for _, f := range v.Filled {
					if !strings.Contains(f.Reason, "eternity interval") {
						t.Fatalf("fill rule not stated: %s", f.Reason)
					}
				}
			}
		})
	}
}
