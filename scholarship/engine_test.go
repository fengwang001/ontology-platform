package scholarship

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var evalTime = time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		PoolSize: 0,
		Levels: []LevelConfig{
			{ID: "L1", MinAvg: 90, MinCredits: 20, Quotas: map[string]int{"CS": 1, "EE": 1}},
			{ID: "L2", MinAvg: 80, MinCredits: 15, Quotas: map[string]int{"CS": 2, "EE": 1}},
			{ID: "L3", MinAvg: 70, MinCredits: 10, Quotas: map[string]int{"CS": 1, "EE": 1}},
		},
	}
}

func mustEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustAdd(t *testing.T, e *Engine, s Student) {
	t.Helper()
	if err := e.AddStudent(s); err != nil {
		t.Fatalf("AddStudent(%s): %v", s.ID, err)
	}
}

func awardOf(res Result, studentID string) (Award, bool) {
	for _, a := range res.Awards {
		if a.StudentID == studentID {
			return a, true
		}
	}
	return Award{}, false
}

func rankOf(res Result, deptID, studentID string) int {
	for _, dr := range res.Rankings {
		if dr.DeptID != deptID {
			continue
		}
		for _, en := range dr.Entries {
			if en.StudentID == studentID {
				return en.Rank
			}
		}
	}
	return -1
}

func reasonOf(res Result, studentID, levelID string) FailReason {
	for _, d := range res.Disqualified {
		if d.StudentID == studentID && d.LevelID == levelID {
			return d.Reason
		}
	}
	return ReasonNone
}

// 平均成绩与学分下限取等视为达到。
func TestEligibilityBoundaryEquality(t *testing.T) {
	e := mustEngine(t, Config{
		Levels: []LevelConfig{{ID: "L1", MinAvg: 90, MinCredits: 20, Quotas: map[string]int{"CS": 3}}},
	})
	mustAdd(t, e, Student{ID: "eqBoth", DeptID: "CS", AvgGrade: 90, Credits: 20})
	mustAdd(t, e, Student{ID: "belowAvg", DeptID: "CS", AvgGrade: 89.5, Credits: 20})
	mustAdd(t, e, Student{ID: "belowCredits", DeptID: "CS", AvgGrade: 90, Credits: 19})

	res := e.Evaluate(evalTime)
	if a, ok := awardOf(res, "eqBoth"); !ok || a.LevelID != "L1" {
		t.Fatalf("eqBoth should win L1, got %+v ok=%v", a, ok)
	}
	if r := reasonOf(res, "belowAvg", "L1"); r != ReasonAvgGrade {
		t.Fatalf("belowAvg reason = %v, want %v", r, ReasonAvgGrade)
	}
	if r := reasonOf(res, "belowCredits", "L1"); r != ReasonCredits {
		t.Fatalf("belowCredits reason = %v, want %v", r, ReasonCredits)
	}
}

// 处分解除时刻恰等于评定时刻视为已解除; 晚于评定时刻则生效中。
func TestDisciplineLiftedExactlyAtEvaluation(t *testing.T) {
	e := mustEngine(t, Config{
		Levels: []LevelConfig{{ID: "L1", MinAvg: 90, MinCredits: 20, Quotas: map[string]int{"CS": 2}}},
	})
	mustAdd(t, e, Student{ID: "liftedEq", DeptID: "CS", AvgGrade: 95, Credits: 20,
		Disciplines: []Discipline{{ID: "D1", LiftedAt: evalTime}}})
	mustAdd(t, e, Student{ID: "liftedLate", DeptID: "CS", AvgGrade: 96, Credits: 20,
		Disciplines: []Discipline{{ID: "D2", LiftedAt: evalTime.Add(time.Second)}}})
	mustAdd(t, e, Student{ID: "indefinite", DeptID: "CS", AvgGrade: 97, Credits: 20,
		Disciplines: []Discipline{{ID: "D3"}}})

	res := e.Evaluate(evalTime)
	if a, ok := awardOf(res, "liftedEq"); !ok || a.LevelID != "L1" {
		t.Fatalf("liftedEq should win L1 (lifted exactly at eval time), got %+v ok=%v", a, ok)
	}
	if r := reasonOf(res, "liftedLate", "L1"); r != ReasonDiscipline {
		t.Fatalf("liftedLate reason = %v, want %v", r, ReasonDiscipline)
	}
	if r := reasonOf(res, "indefinite", "L1"); r != ReasonDiscipline {
		t.Fatalf("indefinite reason = %v, want %v", r, ReasonDiscipline)
	}
}

// 多条规则同时不满足时只报告优先级最高的一条: 处分 > 不及格 > 学分 > 平均成绩。
func TestFailReasonPriority(t *testing.T) {
	e := mustEngine(t, Config{
		Levels: []LevelConfig{{ID: "L1", MinAvg: 90, MinCredits: 20, Quotas: map[string]int{"CS": 1}}},
	})
	mustAdd(t, e, Student{ID: "allBad", DeptID: "CS", AvgGrade: 50, Credits: 5,
		HasFailRecord: true, Disciplines: []Discipline{{ID: "D1"}}})
	mustAdd(t, e, Student{ID: "failAndCredits", DeptID: "CS", AvgGrade: 50, Credits: 5, HasFailRecord: true})
	mustAdd(t, e, Student{ID: "creditsAndAvg", DeptID: "CS", AvgGrade: 50, Credits: 5})

	res := e.Evaluate(evalTime)
	if r := reasonOf(res, "allBad", "L1"); r != ReasonDiscipline {
		t.Fatalf("allBad reason = %v, want %v", r, ReasonDiscipline)
	}
	if r := reasonOf(res, "failAndCredits", "L1"); r != ReasonFailRecord {
		t.Fatalf("failAndCredits reason = %v, want %v", r, ReasonFailRecord)
	}
	if r := reasonOf(res, "creditsAndAvg", "L1"); r != ReasonCredits {
		t.Fatalf("creditsAndAvg reason = %v, want %v", r, ReasonCredits)
	}
}

// 三级排序键依次相同形成并列: 同名次, 其后名次按人数跳号。
func TestTieRankingThreeKeys(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustAdd(t, e, Student{ID: "t1", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "t2", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "t3", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "lower", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 4})
	mustAdd(t, e, Student{ID: "lowest", DeptID: "CS", AvgGrade: 90, Credits: 20, HonorPoints: 9})

	res := e.Evaluate(evalTime)
	for _, id := range []string{"t1", "t2", "t3"} {
		if r := rankOf(res, "CS", id); r != 1 {
			t.Fatalf("rank(%s) = %d, want 1", id, r)
		}
	}
	if r := rankOf(res, "CS", "lower"); r != 4 {
		t.Fatalf("rank(lower) = %d, want 4 (tie of 3 skips 2,3)", r)
	}
	if r := rankOf(res, "CS", "lowest"); r != 5 {
		t.Fatalf("rank(lowest) = %d, want 5", r)
	}
}

// 并列组恰好填满名额: 整组授予。
func TestTieGroupExactlyFillsQuota(t *testing.T) {
	cfg := testConfig()
	cfg.Levels[0].Quotas["CS"] = 2
	e := mustEngine(t, cfg)
	mustAdd(t, e, Student{ID: "a", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "b", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})

	res := e.Evaluate(evalTime)
	for _, id := range []string{"a", "b"} {
		if a, ok := awardOf(res, id); !ok || a.LevelID != "L1" || a.FromPool {
			t.Fatalf("%s should win L1 from dept quota, got %+v ok=%v", id, a, ok)
		}
	}
}

// 并列组超出名额一人: 整组不授予该等级, 剩余名额回流机动池;
// 回流的 1 个名额随后被其他院系的高分学生从机动池获得。
func TestTieGroupExceedsQuotaByOne(t *testing.T) {
	cfg := testConfig()
	cfg.Levels[0].Quotas["CS"] = 1
	cfg.Levels[0].Quotas["EE"] = 0
	cfg.Levels[1].Quotas["EE"] = 0
	cfg.Levels[2].Quotas["EE"] = 0
	e := mustEngine(t, cfg)
	mustAdd(t, e, Student{ID: "a", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "b", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "ee", DeptID: "EE", AvgGrade: 92, Credits: 20, HonorPoints: 0})

	res := e.Evaluate(evalTime)
	for _, id := range []string{"a", "b"} {
		if a, ok := awardOf(res, id); !ok || a.LevelID == "L1" {
			t.Fatalf("%s must not win L1 (tie group exceeds quota), got %+v ok=%v", id, a, ok)
		}
	}
	a, ok := awardOf(res, "ee")
	if !ok || a.LevelID != "L1" || !a.FromPool {
		t.Fatalf("ee should win L1 from returned pool quota, got %+v ok=%v", a, ok)
	}
	if res.PoolLeft != 0 {
		t.Fatalf("PoolLeft = %d, want 0 (1 returned, 1 consumed)", res.PoolLeft)
	}
}

// 整组转入下一等级后在下一等级再次并列并恰好填满。
func TestTieGroupFallsToNextLevelAndTiesAgain(t *testing.T) {
	cfg := testConfig()
	cfg.Levels[0].Quotas["CS"] = 1
	cfg.Levels[0].Quotas["EE"] = 0
	cfg.Levels[1].Quotas["CS"] = 2
	e := mustEngine(t, cfg)
	mustAdd(t, e, Student{ID: "a", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})
	mustAdd(t, e, Student{ID: "b", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5})

	res := e.Evaluate(evalTime)
	for _, id := range []string{"a", "b"} {
		a, ok := awardOf(res, id)
		if !ok || a.LevelID != "L2" {
			t.Fatalf("%s should fall to L2 and win as tie group, got %+v ok=%v", id, a, ok)
		}
	}
	if res.PoolLeft != 1 {
		t.Fatalf("PoolLeft = %d, want 1 (returned L1 quota unused)", res.PoolLeft)
	}
}

// 机动池跨院系并列: 整组授予; 名额不足时整组不授予。
func TestPoolCrossDeptTie(t *testing.T) {
	mkCfg := func(pool int) Config {
		return Config{
			PoolSize: pool,
			Levels: []LevelConfig{
				{ID: "L1", MinAvg: 90, MinCredits: 20, Quotas: map[string]int{"CS": 0, "EE": 0}},
			},
		}
	}
	mkStudents := func() []Student {
		return []Student{
			{ID: "cs", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 5},
			{ID: "ee", DeptID: "EE", AvgGrade: 95, Credits: 20, HonorPoints: 5},
		}
	}

	e := mustEngine(t, mkCfg(2))
	for _, s := range mkStudents() {
		mustAdd(t, e, s)
	}
	res := e.Evaluate(evalTime)
	for _, id := range []string{"cs", "ee"} {
		if a, ok := awardOf(res, id); !ok || a.LevelID != "L1" || !a.FromPool {
			t.Fatalf("%s should win L1 from pool, got %+v ok=%v", id, a, ok)
		}
	}

	e2 := mustEngine(t, mkCfg(1))
	for _, s := range mkStudents() {
		mustAdd(t, e2, s)
	}
	res2 := e2.Evaluate(evalTime)
	if len(res2.Awards) != 0 {
		t.Fatalf("pool=1 cannot fit tie group of 2, want no awards, got %+v", res2.Awards)
	}
	if res2.PoolLeft != 1 {
		t.Fatalf("PoolLeft = %d, want 1 (unused pool quota is voided)", res2.PoolLeft)
	}
}

// 已确认奖励与重评结论冲突: 保留已确认奖励并占用对应名额。
func TestConfirmedAwardConflictsWithReevaluation(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustAdd(t, e, Student{ID: "A", DeptID: "CS", AvgGrade: 95, Credits: 20})
	mustAdd(t, e, Student{ID: "B", DeptID: "CS", AvgGrade: 91, Credits: 20})

	res1 := e.Evaluate(evalTime)
	if a, ok := awardOf(res1, "A"); !ok || a.LevelID != "L1" {
		t.Fatalf("A should win L1 initially, got %+v ok=%v", a, ok)
	}
	if err := e.Confirm("A"); err != nil {
		t.Fatalf("Confirm(A): %v", err)
	}

	// 成绩更正使 A 不再具备 L1 资格(出现不及格记录)。
	if err := e.CorrectGrade("A", 95, true); err != nil {
		t.Fatalf("CorrectGrade(A): %v", err)
	}
	res2 := e.Evaluate(evalTime)
	a, ok := awardOf(res2, "A")
	if !ok || a.LevelID != "L1" {
		t.Fatalf("A must keep confirmed L1 despite reevaluation, got %+v ok=%v", a, ok)
	}
	if r := reasonOf(res2, "A", "L1"); r != ReasonFailRecord {
		t.Fatalf("A should be reported ineligible for L1, reason = %v", r)
	}
	if b, ok := awardOf(res2, "B"); ok && b.LevelID == "L1" {
		t.Fatalf("B must not win L1: quota occupied by confirmed A, got %+v", b)
	}
	if b, ok := awardOf(res2, "B"); !ok || b.LevelID != "L2" {
		t.Fatalf("B should win L2 instead, got %+v ok=%v", b, ok)
	}
}

// 排序键在重评后变化导致名次互换。
func TestRankingSwapAfterReevaluation(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustAdd(t, e, Student{ID: "A", DeptID: "CS", AvgGrade: 90, Credits: 20})
	mustAdd(t, e, Student{ID: "B", DeptID: "CS", AvgGrade: 91, Credits: 20})

	res1 := e.Evaluate(evalTime)
	if rankOf(res1, "CS", "B") != 1 || rankOf(res1, "CS", "A") != 2 {
		t.Fatalf("initial ranks wrong: %+v", res1.Rankings)
	}
	if a, ok := awardOf(res1, "B"); !ok || a.LevelID != "L1" {
		t.Fatalf("B should win L1 initially, got %+v ok=%v", a, ok)
	}

	if err := e.CorrectGrade("A", 95, false); err != nil {
		t.Fatalf("CorrectGrade(A): %v", err)
	}
	res2 := e.Evaluate(evalTime)
	if rankOf(res2, "CS", "A") != 1 || rankOf(res2, "CS", "B") != 2 {
		t.Fatalf("ranks should swap after correction: %+v", res2.Rankings)
	}
	if a, ok := awardOf(res2, "A"); !ok || a.LevelID != "L1" {
		t.Fatalf("A should win L1 after correction, got %+v ok=%v", a, ok)
	}
	if b, ok := awardOf(res2, "B"); !ok || b.LevelID != "L2" {
		t.Fatalf("B should drop to L2, got %+v ok=%v", b, ok)
	}
}

// 拒绝优先级逐对验证。
func TestErrorPriorityPairs(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustAdd(t, e, Student{ID: "S", DeptID: "CS", AvgGrade: 60, Credits: 20})

	// 参数非法 > 不存在: "" 既非法也不存在。
	if err := e.Confirm(""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Confirm(\"\") = %v, want ErrInvalidParam", err)
	}
	if err := e.CorrectGrade("", -1, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("CorrectGrade(\"\") = %v, want ErrInvalidParam", err)
	}
	// 不存在 > 未评定: ghost 不存在且尚未评定。
	if err := e.Confirm("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Confirm(ghost) = %v, want ErrNotFound", err)
	}
	// 未评定 > 奖励不存在: S 存在但尚未评定。
	if err := e.Confirm("S"); !errors.Is(err, ErrNotEvaluated) {
		t.Fatalf("Confirm(S) before evaluate = %v, want ErrNotEvaluated", err)
	}
	// 奖励不存在(评定后 S 无奖励)。
	e.Evaluate(evalTime)
	if err := e.Confirm("S"); !errors.Is(err, ErrAwardNotInResult) {
		t.Fatalf("Confirm(S) after evaluate = %v, want ErrAwardNotInResult", err)
	}
	// 已确认(重复确认): 先让 S 获奖再确认两次。
	if err := e.CorrectGrade("S", 99, false); err != nil {
		t.Fatalf("CorrectGrade(S): %v", err)
	}
	e.Evaluate(evalTime)
	if err := e.Confirm("S"); err != nil {
		t.Fatalf("first Confirm(S) = %v", err)
	}
	if err := e.Confirm("S"); !errors.Is(err, ErrAlreadyConfirmed) {
		t.Fatalf("second Confirm(S) = %v, want ErrAlreadyConfirmed", err)
	}
	// 注: 奖励不存在 与 已确认 无法同时成立(已确认者必被钉在当前结果中), 该对无反例。

	// 变更操作同样遵循 参数非法 > 不存在。
	if err := e.CorrectGrade("ghost", 200, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("CorrectGrade(ghost,200) = %v, want ErrInvalidParam", err)
	}
	if err := e.CorrectGrade("ghost", 90, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CorrectGrade(ghost,90) = %v, want ErrNotFound", err)
	}
	if err := e.LiftDiscipline("S", "noSuch", evalTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LiftDiscipline(noSuch) = %v, want ErrNotFound", err)
	}
}

// 被拒绝的操作不得改动任何状态。
func TestRejectedOpsNoStateChange(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustAdd(t, e, Student{ID: "A", DeptID: "CS", AvgGrade: 95, Credits: 20})
	mustAdd(t, e, Student{ID: "B", DeptID: "EE", AvgGrade: 92, Credits: 20})
	before := e.Evaluate(evalTime)

	_ = e.Confirm("")
	_ = e.Confirm("ghost")
	_ = e.Confirm("B") // B 在 EE 获奖? 若获奖则成功; 下面统一用非法调用
	_ = e.CorrectGrade("", -1, false)
	_ = e.CorrectGrade("ghost", 90, false)
	_ = e.RegisterDiscipline("", "D1", evalTime)
	_ = e.LiftDiscipline("A", "ghost", evalTime)
	if err := e.AddStudent(Student{ID: "A", DeptID: "CS"}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("duplicate AddStudent = %v, want ErrInvalidParam", err)
	}

	after := e.Evaluate(evalTime)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected ops changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// 相同数据与相同已确认集合下, 任意两次评定结果逐项相同。
func TestDeterministicEvaluation(t *testing.T) {
	build := func() *Engine {
		e := mustEngine(t, testConfig())
		mustAdd(t, e, Student{ID: "A", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 1})
		mustAdd(t, e, Student{ID: "B", DeptID: "CS", AvgGrade: 95, Credits: 20, HonorPoints: 1})
		mustAdd(t, e, Student{ID: "C", DeptID: "EE", AvgGrade: 92, Credits: 22})
		mustAdd(t, e, Student{ID: "D", DeptID: "EE", AvgGrade: 88, Credits: 18, HasFailRecord: true})
		return e
	}
	e1, e2 := build(), build()
	e1.Evaluate(evalTime)
	e2.Evaluate(evalTime)
	if err := e1.Confirm("C"); err != nil {
		t.Fatalf("Confirm(C): %v", err)
	}
	if err := e2.Confirm("C"); err != nil {
		t.Fatalf("Confirm(C): %v", err)
	}
	for i := 0; i < 5; i++ {
		a, b := e1.Evaluate(evalTime), e2.Evaluate(evalTime)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("round %d diverged:\n%+v\n%+v", i, a, b)
		}
	}
}

// 并发调用等价于某个串行顺序: 无数据竞争, 结束后不变量成立。
func TestConcurrentOps(t *testing.T) {
	e := mustEngine(t, testConfig())
	for i := 0; i < 20; i++ {
		mustAdd(t, e, Student{ID: fmt.Sprintf("S%02d", i), DeptID: []string{"CS", "EE"}[i%2],
			AvgGrade: float64(70 + i), Credits: 20})
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				switch j % 3 {
				case 0:
					e.Evaluate(evalTime)
				case 1:
					_ = e.CorrectGrade(fmt.Sprintf("S%02d", (i+j)%20), float64(70+(i*j)%31), false)
				case 2:
					_ = e.Confirm(fmt.Sprintf("S%02d", (i+j)%20))
				}
			}
		}(i)
	}
	wg.Wait()
	checkInvariants(t, testConfig(), e.Evaluate(evalTime))
}

// 不变量: 每名学生至多一项奖励; 每等级每院系非机动授予不超过其名额。
func checkInvariants(t *testing.T, cfg Config, res Result) {
	t.Helper()
	perStudent := map[string]int{}
	deptUsed := map[string]map[string]int{}
	for _, a := range res.Awards {
		perStudent[a.StudentID]++
		if perStudent[a.StudentID] > 1 {
			t.Errorf("student %s got %d awards", a.StudentID, perStudent[a.StudentID])
		}
		if a.FromPool {
			continue
		}
		if deptUsed[a.LevelID] == nil {
			deptUsed[a.LevelID] = map[string]int{}
		}
		deptUsed[a.LevelID][a.DeptID]++
	}
	for _, lv := range cfg.Levels {
		for d, q := range lv.Quotas {
			if used := deptUsed[lv.ID][d]; used > q {
				t.Errorf("level %s dept %s: %d dept-quota awards > quota %d", lv.ID, d, used, q)
			}
		}
	}
}

// 资格判定开销不随学生总数增长: 无论多少学生, 单次判定只读 1 条学生记录。
func TestEligibilityCostIndependentOfStudentCount(t *testing.T) {
	reads := map[int]int64{}
	for _, n := range []int{1000, 100000} {
		e := mustEngine(t, testConfig())
		for i := 0; i < n; i++ {
			mustAdd(t, e, Student{ID: fmt.Sprintf("S%d", i), DeptID: "CS", AvgGrade: 80, Credits: 20})
		}
		if _, err := e.CheckEligibility("S0", "L1", evalTime); err != nil {
			t.Fatalf("CheckEligibility: %v", err)
		}
		reads[n] = e.LastStats().StudentReads
	}
	if reads[1000] != 1 || reads[100000] != 1 {
		t.Fatalf("StudentReads = %v, want 1 regardless of student count", reads)
	}
}

// 重评开销不随历史评定次数增长: 引擎不保存历史, 每次评定的学生记录读取量相同。
func TestReevaluationCostIndependentOfHistory(t *testing.T) {
	build := func() *Engine {
		e := mustEngine(t, testConfig())
		for i := 0; i < 50; i++ {
			mustAdd(t, e, Student{ID: fmt.Sprintf("S%d", i), DeptID: []string{"CS", "EE"}[i%2],
				AvgGrade: float64(70 + i%30), Credits: 20})
		}
		return e
	}
	fresh := build()
	fresh.Evaluate(evalTime)
	want := fresh.LastStats().StudentReads

	seasoned := build()
	for i := 0; i < 100; i++ {
		seasoned.Evaluate(evalTime)
		_ = seasoned.CorrectGrade("S1", float64(70+i%30), false)
	}
	seasoned.Evaluate(evalTime)
	if got := seasoned.LastStats().StudentReads; got != want {
		t.Fatalf("StudentReads after 101 evaluations = %d, want %d (same as fresh)", got, want)
	}
}
