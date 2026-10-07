package ontology_test

import (
	"fmt"
	"testing"

	"ontology"
)

// 本测试以确定性计数器（扫描的住宿记录数、重新推导的病例数）
// 在两档规模上对照，证明：
// 状态查询开销与全院住宿记录总数无关；
// 发病改正/住宿追补后的重新推导只涉及受影响的病例与病房。

const scaleNow = 7000

// buildScaleEngine 构造固定逻辑场景与 filler 条无关住宿记录。
// 热点场景：病例 C-HOT（患者 P，病房 WH）；X 为密接；Z 经 Q 为次密接。
// 填充数据分布在 2000 个无关病房，与热点场景无任何接触关系。
func buildScaleEngine(tb testing.TB, filler int) *ontology.Engine {
	tb.Helper()
	e := ontology.NewEngine()
	for i := 0; i < filler; i++ {
		p := fmt.Sprintf("FP%d", i)
		w := fmt.Sprintf("WF%d", i%2000)
		if err := e.BackfillStay(p, w, 10, 500, scaleNow); err != nil {
			tb.Fatalf("filler %d: %v", i, err)
		}
	}
	must := func(err error) {
		if err != nil {
			tb.Fatal(err)
		}
	}
	must(e.BackfillStay("P", "WH", 1000, 5000, scaleNow))
	must(e.BackfillStay("X", "WH", 1100, 1300, scaleNow))
	must(e.BackfillStay("Q", "WH", 1200, 1400, scaleNow))
	must(e.BackfillStay("Q", "WH2", 3000, 3300, scaleNow))
	must(e.BackfillStay("Z", "WH2", 3100, 3300, scaleNow))
	must(e.RegisterCase("C-HOT", "P", 2000, scaleNow))
	return e
}

func TestScaleIndependence(t *testing.T) {
	type snapshot struct {
		status     ontology.StatusResult
		queryStats ontology.OpStats
		invalidIDs []string
		derived    int
	}
	run := func(filler int) snapshot {
		e := buildScaleEngine(t, filler)

		st, err := e.Status("X", scaleNow)
		if err != nil {
			t.Fatal(err)
		}
		qs := e.LastOpStats()

		// 无关病房的追补不得使任何病例失效
		if err := e.BackfillStay("FP0", "WF0", 600, 900, scaleNow); err != nil {
			t.Fatal(err)
		}
		if n := e.LastOpStats().CasesInvalidated; n != 0 {
			t.Fatalf("filler=%d 无关追补导致 %d 个病例失效", filler, n)
		}

		// 改正发病时刻只应使该病例失效；随后的查询只重新推导它
		if err := e.CorrectOnset("C-HOT", 2100, scaleNow); err != nil {
			t.Fatal(err)
		}
		inv := e.LastOpStats()
		if _, err := e.Status("X", scaleNow); err != nil {
			t.Fatal(err)
		}
		return snapshot{
			status:     st,
			queryStats: qs,
			invalidIDs: inv.InvalidatedIDs,
			derived:    e.LastOpStats().CasesDerived,
		}
	}

	small := run(2_000)
	large := run(200_000)

	if small.status.Status != ontology.StatusClose {
		t.Fatalf("热点场景状态错误: %v", small.status.Status)
	}
	// 查询开销（扫描记录数、推导病例数）在两档规模下完全一致
	if small.queryStats.StaysScanned != large.queryStats.StaysScanned ||
		small.queryStats.CasesDerived != large.queryStats.CasesDerived ||
		small.queryStats.CasesInvalidated != large.queryStats.CasesInvalidated {
		t.Fatalf("查询开销随总规模增长: 小=%+v 大=%+v", small.queryStats, large.queryStats)
	}
	if small.queryStats.CasesDerived != 1 {
		t.Fatalf("状态查询推导病例数 = %d，应为 1", small.queryStats.CasesDerived)
	}
	if small.queryStats.StaysScanned > 100 {
		t.Fatalf("状态查询扫描住宿记录数 = %d，与热点规模不符", small.queryStats.StaysScanned)
	}
	// 发病改正只使该病例失效，且随后的查询只重新推导它
	if len(small.invalidIDs) != 1 || small.invalidIDs[0] != "C-HOT" {
		t.Fatalf("改正发病失效集合 = %v", small.invalidIDs)
	}
	if len(large.invalidIDs) != 1 || large.invalidIDs[0] != "C-HOT" {
		t.Fatalf("改正发病失效集合（大） = %v", large.invalidIDs)
	}
	if small.derived != 1 || large.derived != 1 {
		t.Fatalf("改正后重新推导病例数 小=%d 大=%d，应均为 1", small.derived, large.derived)
	}
	t.Logf("两档规模对照（2千 vs 20万条住宿）：状态查询扫描记录=%d 推导病例=%d，完全一致",
		small.queryStats.StaysScanned, small.queryStats.CasesDerived)
}

// 次密接查询在两档规模下的对照。
func TestScaleSecondaryQuery(t *testing.T) {
	var scanned []int
	for _, filler := range []int{2_000, 200_000} {
		e := buildScaleEngine(t, filler)
		st, err := e.Status("Z", scaleNow)
		if err != nil {
			t.Fatal(err)
		}
		if st.Status != ontology.StatusSecondary {
			t.Fatalf("filler=%d Z 状态 = %v，应为次密接观察中", filler, st.Status)
		}
		scanned = append(scanned, e.LastOpStats().StaysScanned)
	}
	if scanned[0] != scanned[1] {
		t.Fatalf("次密接查询开销随总规模增长: %v", scanned)
	}
}

func BenchmarkStatusQueryScale2K(b *testing.B) {
	e := buildScaleEngine(b, 2_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Status("X", scaleNow); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatusQueryScale200K(b *testing.B) {
	e := buildScaleEngine(b, 200_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Status("X", scaleNow); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCaseContactsScale200K(b *testing.B) {
	e := buildScaleEngine(b, 200_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.CaseContacts("C-HOT", scaleNow); err != nil {
			b.Fatal(err)
		}
	}
}
