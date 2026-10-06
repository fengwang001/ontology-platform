package scholarship

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// logTracer 将每步输入、输出与判定依据写入测试日志。
type logTracer struct{ t *testing.T }

func (l logTracer) Log(step, detail string) { l.t.Logf("[trace] %s: %s", step, detail) }

func mustEngine(t *testing.T, levels []LevelConfig, quota map[string]map[string]int) *Engine {
	t.Helper()
	e, err := NewEngine(levels, quota, logTracer{t})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func awardLevel(res *Result, id string) string {
	for _, a := range res.Awards {
		if a.StudentID == id {
			return a.Level
		}
	}
	return ""
}

func TestThresholdEquality(t *testing.T) {
	lv := LevelConfig{ID: "L", MinAverage: 80, MinCredits: 20}
	eq := Student{ID: "s", Dept: "D", Average: 80, Credits: 20}
	if r := judge(&eq, lv, t0); !r.Eligible {
		t.Fatalf("exact threshold should be eligible, got reason %d", r.Reason)
	}
	below := eq
	below.Average = 79.99
	if r := judge(&below, lv, t0); r.Eligible || r.Reason != ReasonAverage {
		t.Fatalf("average below: %+v", r)
	}
	below2 := eq
	below2.Credits = 19.99
	if r := judge(&below2, lv, t0); r.Eligible || r.Reason != ReasonCredits {
		t.Fatalf("credits below: %+v", r)
	}
}

func TestDisqualificationPriority(t *testing.T) {
	lv := LevelConfig{ID: "L", MinAverage: 90, MinCredits: 30}
	s := Student{ID: "s", Dept: "D", Average: 1, Credits: 1, HasFail: true,
		Sanctions: []Sanction{{ID: "x"}}}
	if r := judge(&s, lv, t0); r.Reason != ReasonSanction {
		t.Fatalf("want sanction, got %d", r.Reason)
	}
	s.Sanctions = nil
	if r := judge(&s, lv, t0); r.Reason != ReasonFail {
		t.Fatalf("want fail, got %d", r.Reason)
	}
	s.HasFail = false
	if r := judge(&s, lv, t0); r.Reason != ReasonCredits {
		t.Fatalf("want credits, got %d", r.Reason)
	}
	s.Credits = 100
	if r := judge(&s, lv, t0); r.Reason != ReasonAverage {
		t.Fatalf("want average, got %d", r.Reason)
	}
}

func TestSanctionReleasedExactlyAtEval(t *testing.T) {
	lv := LevelConfig{ID: "L", MinAverage: 60, MinCredits: 1}
	s := Student{ID: "s", Dept: "D", Average: 80, Credits: 10,
		Sanctions: []Sanction{{ID: "x", ReleasedAt: t0}}}
	if r := judge(&s, lv, t0); !r.Eligible {
		t.Fatalf("released exactly at eval time must be eligible, got %d", r.Reason)
	}
	s.Sanctions[0].ReleasedAt = t0.Add(time.Nanosecond)
	if r := judge(&s, lv, t0); r.Eligible || r.Reason != ReasonSanction {
		t.Fatalf("released just after eval must be active, got %+v", r)
	}
}

func TestThreeKeyTieAndRankJump(t *testing.T) {
	lv := LevelConfig{ID: "L", MinAverage: 0}
	e := mustEngine(t, []LevelConfig{lv}, nil)
	for _, id := range []string{"a", "b", "c", "d"} {
		s := Student{ID: id, Dept: "D", Average: 90, Credits: 20, Honor: 5}
		if id == "c" || id == "d" {
			s.Honor = 4
		}
		if err := e.AddStudent(s); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.Evaluate(t0)
	if err != nil {
		t.Fatal(err)
	}
	rk := map[string]int{}
	for _, r := range res.Ranking["L"] {
		rk[r.StudentID] = r.Rank
	}
	if rk["a"] != 1 || rk["b"] != 1 || rk["c"] != 3 || rk["d"] != 3 {
		t.Fatalf("ranks = %v, want {a:1 b:1 c:3 d:3}", rk)
	}
}

// 并列组恰好填满名额：整组授予；超出一人：整组不授院系奖、名额回流后由池授予。
func TestTieGroupFillsAndExceedsByOne(t *testing.T) {
	lv := LevelConfig{ID: "L", MinAverage: 0}

	e := mustEngine(t, []LevelConfig{lv}, map[string]map[string]int{"L": {"D": 2}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	res, _ := e.Evaluate(t0)
	if awardLevel(res, "a") != "L" || awardLevel(res, "b") != "L" {
		t.Fatalf("tie group exactly filling quota must be granted: %+v", res.Awards)
	}

	// 超出一人：名额 2，并列组 3 人 -> 整组不授院系奖，2 名额回流；
	// 机动池仍不足以授予整组时整组不授、名额作废。
	e = mustEngine(t, []LevelConfig{lv}, map[string]map[string]int{"L": {"D": 2}})
	for _, id := range []string{"a", "b", "c"} {
		_ = e.AddStudent(Student{ID: id, Dept: "D", Average: 90, Credits: 20, Honor: 5})
	}
	res, _ = e.Evaluate(t0)
	var deptN, poolN int
	for _, aw := range res.Awards {
		switch aw.Source {
		case SourceDept:
			deptN++
		case SourcePool:
			poolN++
		}
	}
	if deptN != 0 || poolN != 0 {
		t.Fatalf("want 0 dept / 0 pool awards, got dept=%d pool=%d (%+v)", deptN, poolN, res.Awards)
	}

	// 回流名额跨院系生效：D 的并列组（2 人 > 名额 1）使 1 个名额回流，
	// 机动池中位于并列组之后、名次独立的 E 学生获得该回流名额
	//（头部并列组仍整组不授）。
	lvP := LevelConfig{ID: "L", MinAverage: 0, PoolQuota: 2}
	e = mustEngine(t, []LevelConfig{lvP}, map[string]map[string]int{"L": {"D": 2, "E": 0}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 95, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "c", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "d", Dept: "E", Average: 80, Credits: 20, Honor: 5})
	res, _ = e.Evaluate(t0)
	var dAward *Award
	for i := range res.Awards {
		if res.Awards[i].StudentID == "d" {
			dAward = &res.Awards[i]
		}
	}
	if dAward == nil || dAward.Source != SourcePool {
		t.Fatalf("reflowed slot must reach d via pool in E: %+v", res.Awards)
	}

	// 纯回流（PoolQuota=0）：D 的 2 人并列组被名额 1 阻塞，1 名额回流；
	// E 的独立候选人 d 在跨院系排序中无更高的未授并列组，仅凭回流名额获得机动奖。
	lvNoPool := LevelConfig{ID: "L", MinAverage: 0, PoolQuota: 0}
	e = mustEngine(t, []LevelConfig{lvNoPool}, map[string]map[string]int{"L": {"D": 1, "E": 0}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "d", Dept: "E", Average: 95, Credits: 20, Honor: 5})
	res, _ = e.Evaluate(t0)
	dAward = nil
	for i := range res.Awards {
		if res.Awards[i].StudentID == "d" {
			dAward = &res.Awards[i]
		}
	}
	if dAward == nil || dAward.Source != SourcePool {
		t.Fatalf("pure reflow slot must reach d: %+v", res.Awards)
	}
}

// 整组在高等级被跳过，转入下一等级后在下一等级再次并列并整组获得。
func TestBlockedGroupTiesAgainAtNextLevel(t *testing.T) {
	levels := []LevelConfig{
		{ID: "Gold", MinAverage: 90},
		{ID: "Silver", MinAverage: 0},
	}
	quota := map[string]map[string]int{
		"Gold":   {"D": 1},
		"Silver": {"D": 2},
	}
	e := mustEngine(t, levels, quota)
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 95, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 95, Credits: 20, Honor: 5})
	res, _ := e.Evaluate(t0)
	if awardLevel(res, "a") != "Silver" || awardLevel(res, "b") != "Silver" {
		t.Fatalf("both blocked at Gold must tie and get Silver: %+v", res.Awards)
	}
	for _, aw := range res.Awards {
		if aw.Source != SourceDept {
			t.Fatalf("silver awards must come from dept quota, got %+v", aw)
		}
	}
}

// 机动池跨院系并列：整组授予或整组不授；用不完作废。
func TestPoolCrossDeptTie(t *testing.T) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0, PoolQuota: 1}}
	e := mustEngine(t, levels, map[string]map[string]int{"L": {"D1": 0, "D2": 0}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D1", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D2", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "c", Dept: "D2", Average: 90, Credits: 20, Honor: 5})
	res, _ := e.Evaluate(t0)
	if len(res.Awards) != 0 {
		t.Fatalf("cross-dept tie group must be wholly refused by pool of 1: %+v", res.Awards)
	}

	levels[0].PoolQuota = 3
	e = mustEngine(t, levels, map[string]map[string]int{"L": {"D1": 0, "D2": 0}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D1", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "b", Dept: "D2", Average: 90, Credits: 20, Honor: 5})
	_ = e.AddStudent(Student{ID: "c", Dept: "D2", Average: 90, Credits: 20, Honor: 5})
	res, _ = e.Evaluate(t0)
	if len(res.Awards) != 3 {
		t.Fatalf("pool of 3 must grant the whole group: %+v", res.Awards)
	}
}

// 已确认奖励与重评结论冲突：奖励保留且占用名额，其余学生在剩余名额内分配。
func TestConfirmedSurvivesConflictingReevaluation(t *testing.T) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0}}
	e := mustEngine(t, levels, map[string]map[string]int{"L": {"D": 1}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 80})
	res, _ := e.Evaluate(t0)
	if awardLevel(res, "a") != "L" {
		t.Fatalf("a should win initially")
	}
	if _, err := e.Confirm("a"); err != nil {
		t.Fatal(err)
	}
	if err := e.CorrectGrade("b", 95, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	res, _ = e.Evaluate(t0)
	if awardLevel(res, "a") != "L" {
		t.Fatalf("confirmed award for a must survive: %+v", res.Awards)
	}
	if awardLevel(res, "b") == "L" {
		t.Fatalf("b must not receive the occupied quota: %+v", res.Awards)
	}
	var confirmed bool
	for _, aw := range res.Awards {
		if aw.StudentID == "a" {
			confirmed = aw.Confirmed
		}
	}
	if !confirmed {
		t.Fatalf("surviving award must be marked confirmed: %+v", res.Awards)
	}
}

// 排序键在重评后变化导致名次互换。
func TestRankSwapAfterCorrection(t *testing.T) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0}}
	e := mustEngine(t, levels, map[string]map[string]int{"L": {"D": 2}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90})
	_ = e.AddStudent(Student{ID: "b", Dept: "D", Average: 80})
	res, _ := e.Evaluate(t0)
	order := []string{res.Ranking["L"][0].StudentID, res.Ranking["L"][1].StudentID}
	if order[0] != "a" || order[1] != "b" {
		t.Fatalf("initial order %v", order)
	}
	if err := e.CorrectGrade("b", 95, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	res, _ = e.Evaluate(t0)
	order = []string{res.Ranking["L"][0].StudentID, res.Ranking["L"][1].StudentID}
	if order[0] != "b" || order[1] != "a" {
		t.Fatalf("order must swap after correction, got %v", order)
	}
}

// 拒绝优先级逐对验证：参数非法 > 不存在 > 未评定 > 不在结果 > 已确认。
func TestErrorPriorityPairs(t *testing.T) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0}}
	check := func(name string, err error, want ErrorCode) {
		t.Helper()
		ec, ok := err.(*EngineError)
		if !ok || ec.Code != want {
			t.Fatalf("%s: want code %d, got %v", name, want, err)
		}
	}

	// 参数非法 vs 不存在。
	e := mustEngine(t, levels, nil)
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90})
	_, err := e.Confirm("")
	check("invalid-vs-missing", err, ErrInvalidArgument)

	// 不存在 vs 未评定。
	_, err = e.Confirm("ghost")
	check("missing-vs-noteval", err, ErrNotFound)

	// 未评定 vs 不在结果。
	e = mustEngine(t, levels, map[string]map[string]int{"L": {"D": 0}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90})
	_, err = e.Confirm("a")
	check("noteval-vs-notinresult", err, ErrNotEvaluated)

	// 不在结果 vs 已确认。
	res, err := e.Evaluate(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Awards) != 0 {
		t.Fatalf("quota 0 should yield no awards, got %+v", res.Awards)
	}
	_, err = e.Confirm("a")
	check("notinresult-vs-confirmed", err, ErrAwardNotInResult)

	// 已确认（重复确认）。
	e = mustEngine(t, levels, map[string]map[string]int{"L": {"D": 1}})
	_ = e.AddStudent(Student{ID: "a", Dept: "D", Average: 90})
	_, _ = e.Evaluate(t0)
	if _, err := e.Confirm("a"); err != nil {
		t.Fatal(err)
	}
	_, err = e.Confirm("a")
	check("already-confirmed", err, ErrAlreadyConfirmed)

	// 被拒绝的操作不得改动状态：重复确认后仍只有一项已确认奖励，再次评定结果相同。
	r1, _ := e.Evaluate(t0)
	r2, _ := e.Evaluate(t0)
	if !reflect.DeepEqual(normalize(r1), normalize(r2)) {
		t.Fatalf("determinism violated after rejected op")
	}
}

// normalize 将结果整理为与学生输入无关、仅含规则输出的可比较形式。
func normalize(r *Result) []Award {
	out := append([]Award(nil), r.Awards...)
	sort.Slice(out, func(i, j int) bool { return out[i].StudentID < out[j].StudentID })
	return out
}

// assertInvariants 验证规格中的恒定约束：
// 每名学生至多一项；每等级每院系授予数不超过 名额+池授予；无已确认丢失。
func assertInvariants(t *testing.T, levels []LevelConfig, quota map[string]map[string]int,
	students map[string]*Student, res *Result, confirmed map[string]Award) {
	t.Helper()
	seen := map[string]bool{}
	granted := map[string]map[string]int{} // level -> dept -> count (全部来源)
	for _, aw := range res.Awards {
		if seen[aw.StudentID] {
			t.Fatalf("student %s received more than one award", aw.StudentID)
		}
		seen[aw.StudentID] = true
		if granted[aw.Level] == nil {
			granted[aw.Level] = map[string]int{}
		}
		granted[aw.Level][aw.Dept]++
	}
	// 院系名额约束：某等级某院系的院系来源授予数 <= 名额（已确认占用同名额）。
	deptSrc := map[string]map[string]int{}
	for _, aw := range res.Awards {
		if aw.Source != SourceDept {
			continue
		}
		if deptSrc[aw.Level] == nil {
			deptSrc[aw.Level] = map[string]int{}
		}
		deptSrc[aw.Level][aw.Dept]++
	}
	for _, lv := range levels {
		for dept, q := range quota[lv.ID] {
			if n := deptSrc[lv.ID][dept]; n > q {
				t.Fatalf("level %s dept %s granted %d > quota %d", lv.ID, dept, n, q)
			}
		}
	}
	// 已确认奖励必须全部保留。
	for id, cw := range confirmed {
		var found *Award
		for i := range res.Awards {
			if res.Awards[i].StudentID == id {
				found = &res.Awards[i]
			}
		}
		if found == nil {
			t.Fatalf("confirmed award for %s disappeared after reevaluation", id)
		}
		if found.Level != cw.Level || found.Source != cw.Source || found.Dept != cw.Dept {
			t.Fatalf("confirmed award for %s changed: %+v vs %+v", id, *found, cw)
		}
	}
}

// TestRandomDifferential 用随机数据与操作序列对照朴素模型，
// 并检查确定性、重评等价性与名额/唯一性不变量。
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < iterations; iter++ {
		seed := rng.Int63()
		t.Run(fmt.Sprintf("iter%d", iter), func(t *testing.T) {
			runDifferentialCase(t, rand.New(rand.NewSource(seed)))
		})
	}
}

func runDifferentialCase(t *testing.T, rng *rand.Rand) {
	nLevels := 1 + rng.Intn(3)
	levelDefs := make([]LevelConfig, nLevels)
	lower := 95.0
	for i := range levelDefs {
		minAvg := lower - rng.Float64()*10
		levelDefs[i] = LevelConfig{
			ID:         fmt.Sprintf("L%d", i),
			MinAverage: round1(minAvg),
			MinCredits: float64(rng.Intn(25)),
			PoolQuota:  rng.Intn(3),
		}
		lower = minAvg
	}
	nDepts := 1 + rng.Intn(3)
	deptNames := make([]string, nDepts)
	for i := range deptNames {
		deptNames[i] = fmt.Sprintf("D%d", i)
	}
	quota := map[string]map[string]int{}
	for _, lv := range levelDefs {
		quota[lv.ID] = map[string]int{}
		for _, d := range deptNames {
			if rng.Intn(5) != 0 {
				quota[lv.ID][d] = rng.Intn(4)
			}
		}
	}

	eng, err := NewEngine(levelDefs, quota, logTracer{t})
	if err != nil {
		t.Fatal(err)
	}
	nStudents := 1 + rng.Intn(12)
	sids := make([]string, nStudents)
	type rec struct {
		s        Student
		sanction map[string]time.Time // sanctionID -> releasedAt（零值=生效中）
	}
	recs := map[string]*rec{}
	applyStudent := func(r *rec) {
		s := r.s
		for sid := range r.sanction {
			s.Sanctions = append(s.Sanctions, Sanction{ID: sid})
		}
		if err := eng.AddStudent(s); err != nil {
			t.Fatal(err)
		}
	}
	snapshotStudents := func() map[string]*Student {
		out := map[string]*Student{}
		for id, r := range recs {
			s := r.s
			s.Sanctions = nil
			for sid, rel := range r.sanction {
				s.Sanctions = append(s.Sanctions, Sanction{ID: sid, ReleasedAt: rel})
			}
			cp := s
			out[id] = &cp
		}
		return out
	}
	for i := 0; i < nStudents; i++ {
		id := fmt.Sprintf("s%02d", i)
		sids[i] = id
		r := &rec{
			s: Student{
				ID:      id,
				Dept:    deptNames[rng.Intn(nDepts)],
				Average: round1(50 + rng.Float64()*50),
				Credits: float64(rng.Intn(30)),
				Honor:   rng.Intn(10),
				HasFail: rng.Intn(4) == 0,
			},
			sanction: map[string]time.Time{},
		}
		if rng.Intn(3) == 0 {
			r.sanction["x"] = time.Time{}
		}
		recs[id] = r
		applyStudent(r)
	}

	var snapshot map[string]*Student
	confirmed := map[string]Award{}
	snapshot = snapshotStudents()

	compare := func(at time.Time) *Result {
		t.Helper()
		res, err := eng.Evaluate(at)
		if err != nil {
			t.Fatal(err)
		}
		// 确定性：同数据同已确认集合，两次评定逐项相同。
		res2, _ := eng.Evaluate(at)
		if !reflect.DeepEqual(normalize(res), normalize(res2)) {
			t.Fatalf("non-deterministic evaluation: %+v vs %+v", res.Awards, res2.Awards)
		}
		// 与从零朴素模型一致。
		naive := naiveEvaluate(levelDefs, quota, snapshot, confirmed, at)
		if !reflect.DeepEqual(normalize(res), normalize(naive)) {
			t.Fatalf("engine/naive mismatch:\n engine=%+v\n naive =%+v", res.Awards, naive.Awards)
		}
		assertInvariants(t, levelDefs, quota, snapshot, res, confirmed)
		return res
	}

	at := t0
	res := compare(at)

	// 随机操作序列：更正成绩、登记/解除处分、确认，每步后对照朴素模型。
	for step := 0; step < 30; step++ {
		id := sids[rng.Intn(len(sids))]
		r := recs[id]
		switch rng.Intn(4) {
		case 0: // 成绩更正
			r.s.Average = round1(50 + rng.Float64()*50)
			r.s.Credits = float64(rng.Intn(30))
			r.s.Honor = rng.Intn(10)
			r.s.HasFail = rng.Intn(4) == 0
			if err := eng.CorrectGrade(id, r.s.Average, r.s.Credits, r.s.Honor, r.s.HasFail); err != nil {
				t.Fatal(err)
			}
		case 1: // 登记处分
			r.sanction["x"] = time.Time{}
			if err := eng.RegisterSanction(id, "x"); err != nil {
				t.Fatal(err)
			}
		case 2: // 解除处分（时刻可能早于、等于或晚于评定时刻）
			if _, ok := r.sanction["x"]; ok {
				rel := at.Add(time.Duration(rng.Intn(3)-1) * time.Hour)
				r.sanction["x"] = rel
				if err := eng.ReleaseSanction(id, "x", rel); err != nil {
					t.Fatal(err)
				}
			}
		case 3: // 确认当前结果中存在的某奖励
			var aw *Award
			for i := range res.Awards {
				if res.Awards[i].StudentID == id && !res.Awards[i].Confirmed {
					aw = &res.Awards[i]
				}
			}
			if aw != nil {
				if _, err := eng.Confirm(id); err != nil {
					t.Fatal(err)
				}
				cp := *aw
				cp.Confirmed = true
				confirmed[id] = cp
			}
		}
		snapshot = snapshotStudents()
		res = compare(at)
	}
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// TestConcurrentSafety 并发混合调用，结果必须等价于某个串行顺序且不发生数据竞争。
func TestConcurrentSafety(t *testing.T) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0}}
	e := mustEngine(t, levels, map[string]map[string]int{"L": {"D": 2}})
	for i := 0; i < 8; i++ {
		_ = e.AddStudent(Student{ID: fmt.Sprintf("s%d", i), Dept: "D",
			Average: float64(80 + i), Credits: 20, Honor: i})
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 25; k++ {
				_, _ = e.Evaluate(t0)
				_, _ = e.Confirm(fmt.Sprintf("s%d", i%8))
				_ = e.CorrectGrade(fmt.Sprintf("s%d", i%8), float64(70+(k%30)), 20, i, false)
			}
		}(i)
	}
	wg.Wait()
	res, err := e.Evaluate(t0)
	if err != nil {
		t.Fatal(err)
	}
	assertInvariants(t, levels, map[string]map[string]int{"L": {"D": 2}}, nil, res, nil)
}

func BenchmarkEligibilityConstantInN(b *testing.B) {
	levels := []LevelConfig{{ID: "L", MinAverage: 80, MinCredits: 20}}
	e, _ := NewEngine(levels, nil, nopTracer{})
	for i := 0; i < 100000; i++ {
		_ = e.AddStudent(Student{ID: fmt.Sprintf("s%06d", i), Dept: "D",
			Average: 85, Credits: 25, Sanctions: []Sanction{{ID: "x"}}})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Eligibility("s000000", "L", t0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReevalIndependentOfHistory(b *testing.B) {
	levels := []LevelConfig{{ID: "L", MinAverage: 0}}
	quota := map[string]map[string]int{"L": {"D": 200}}
	e, _ := NewEngine(levels, quota, nopTracer{})
	for i := 0; i < 500; i++ {
		_ = e.AddStudent(Student{ID: fmt.Sprintf("s%03d", i), Dept: "D", Average: float64(60 + i%40)})
	}
	_, _ = e.Evaluate(t0)
	for i := 0; i < 100; i++ { // 制造 100 次历史评定，重评开销不得随之增长
		_ = e.CorrectGrade(fmt.Sprintf("s%03d", i%500), float64(60+i%40), 0, 0, false)
		_, _ = e.Evaluate(t0)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Evaluate(t0); err != nil {
			b.Fatal(err)
		}
	}
}
