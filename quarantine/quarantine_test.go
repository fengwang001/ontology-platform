package quarantine

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, W, F, P, R, D int) *Planner {
	t.Helper()
	p, err := New(W, F, P, R, D)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d): %v", W, F, P, R, D, err)
	}
	return p
}

func mustPlan(t *testing.T, p *Planner, build string, tests []string, n int) [][]string {
	t.Helper()
	shards, err := p.Plan(build, tests, n)
	if err != nil {
		t.Fatalf("Plan(%s): %v", build, err)
	}
	return shards
}

func mustReport(t *testing.T, p *Planner, build, test string, pass bool, ms int64) {
	t.Helper()
	if err := p.Report(build, test, pass, ms); err != nil {
		t.Fatalf("Report(%s,%s,%v,%d): %v", build, test, pass, ms, err)
	}
}

func mustFinish(t *testing.T, p *Planner, build string) Verdict {
	t.Helper()
	v, err := p.Finish(build)
	if err != nil {
		t.Fatalf("Finish(%s): %v", build, err)
	}
	return v
}

// passBuild 让 build 中每个用例都以 ms 首次通过并 Finish。
func passBuild(t *testing.T, p *Planner, build string, tests []string, ms int64) {
	t.Helper()
	mustPlan(t, p, build, tests, 1)
	for _, test := range tests {
		mustReport(t, p, build, test, true, ms)
	}
	mustFinish(t, p, build)
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		W, F, P, R, D int
		ok            bool
	}{
		{1, 1, 1, 0, 1, true},
		{50, 50, 20, 3, 10, true},
		{0, 1, 1, 0, 1, false},  // W 下界
		{51, 1, 1, 0, 1, false}, // W 上界
		{5, 0, 1, 0, 1, false},  // F 下界
		{5, 6, 1, 0, 1, false},  // F > W
		{5, 5, 0, 0, 1, false},  // P 下界
		{5, 5, 21, 0, 1, false}, // P 上界
		{5, 5, 1, -1, 1, false}, // R 下界
		{5, 5, 1, 4, 1, false},  // R 上界
		{5, 5, 1, 0, 0, false},  // D 下界
		{5, 5, 1, 0, 11, false}, // D 上界
	}
	for _, c := range cases {
		_, err := New(c.W, c.F, c.P, c.R, c.D)
		if c.ok && err != nil {
			t.Errorf("New(%+v) unexpected error %v", c, err)
		}
		if !c.ok && !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New(%+v) = %v, want ErrInvalidParam", c, err)
		}
	}
}

func TestPlanValidationAndOrder(t *testing.T) {
	p := mustNew(t, 5, 2, 2, 1, 3)
	mustPlan(t, p, "b1", []string{"a"}, 1)
	many := make([]string, 10001)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	cases := []struct {
		name  string
		build string
		tests []string
		n     int
		want  error
	}{
		{"empty build", "", []string{"a"}, 1, ErrInvalidParam},
		{"no tests", "b2", nil, 1, ErrInvalidParam},
		{"too many tests", "b2", many, 1, ErrInvalidParam},
		{"empty test name", "b2", []string{""}, 1, ErrInvalidParam},
		{"duplicate tests", "b2", []string{"a", "a"}, 1, ErrInvalidParam},
		{"n zero", "b2", []string{"a"}, 0, ErrInvalidParam},
		{"n too big", "b2", []string{"a"}, 257, ErrInvalidParam},
		{"build exists", "b1", []string{"a"}, 1, ErrBuildExists},
		{"invalid param beats exists", "b1", nil, 0, ErrInvalidParam},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := p.Plan(c.build, c.tests, c.n); !errors.Is(err, c.want) {
				t.Fatalf("Plan = %v, want %v", err, c.want)
			}
		})
	}
	// 被拒绝的 Plan 不改状态：b2 仍可使用。
	mustPlan(t, p, "b2", []string{"a"}, 1)
}
func TestPlanShardsWithEstAndMedian(t *testing.T) {
	// D=3：x 样本 10,11,14 → est=⌈35/3⌉=12；y 样本 5 → est=5。
	p := mustNew(t, 5, 2, 2, 0, 3)
	passBuild(t, p, "s1", []string{"x"}, 10)
	passBuild(t, p, "s2", []string{"x"}, 11)
	passBuild(t, p, "s3", []string{"x"}, 14)
	passBuild(t, p, "s4", []string{"y"}, 5)
	// z 无样本：有样本的非隔离用例 est 为 [12,5]，下中位数取升序第 1 个 = 5。
	got := mustPlan(t, p, "b", []string{"x", "y", "z"}, 2)
	want := [][]string{{"x"}, {"y", "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shards = %v, want %v", got, want)
	}
}

func TestPlanQuarantineShard(t *testing.T) {
	// F=1：q 一次 Flaky 即被隔离；隔离分片按名字升序附在最后。
	p := mustNew(t, 5, 1, 2, 1, 3)
	mustPlan(t, p, "s1", []string{"q"}, 1)
	mustReport(t, p, "s1", "q", false, 3)
	mustReport(t, p, "s1", "q", true, 3) // Flaky
	mustFinish(t, p, "s1")
	// a、b、c 均无样本且无有效中位数来源，est=1；q 进隔离分片。
	got := mustPlan(t, p, "b", []string{"b", "a", "q", "c"}, 2)
	want := [][]string{{"a", "c"}, {"b"}, {"q"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shards = %v, want %v", got, want)
	}
}

func TestReportStateMachine(t *testing.T) {
	// R=1：每用例最多 2 次尝试。
	p := mustNew(t, 5, 2, 2, 1, 3)
	mustPlan(t, p, "b", []string{"c", "f", "k"}, 1)
	// 首次即通过 = Clean，之后再报状态不符。
	mustReport(t, p, "b", "c", true, 7)
	if err := p.Report("b", "c", true, 7); !errors.Is(err, ErrState) {
		t.Fatalf("report after Clean = %v, want ErrState", err)
	}
	// 失败后通过 = Flaky。
	mustReport(t, p, "b", "f", false, 1)
	mustReport(t, p, "b", "f", true, 9)
	if err := p.Report("b", "f", false, 1); !errors.Is(err, ErrState) {
		t.Fatalf("report after Flaky = %v, want ErrState", err)
	}
	// R+1 次全失败 = Broken。
	mustReport(t, p, "b", "k", false, 1)
	mustReport(t, p, "b", "k", false, 1)
	if err := p.Report("b", "k", false, 1); !errors.Is(err, ErrState) {
		t.Fatalf("report after Broken = %v, want ErrState", err)
	}
	// k 在快照中未隔离且 Broken → Failed。
	if v := mustFinish(t, p, "b"); v != Failed {
		t.Fatalf("verdict = %v, want Failed", v)
	}
}

func TestReportMSValidation(t *testing.T) {
	p := mustNew(t, 5, 2, 2, 0, 3)
	mustPlan(t, p, "b", []string{"a"}, 1)
	for _, ms := range []int64{-1, 1_000_000_001} {
		if err := p.Report("b", "a", true, ms); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Report ms=%d = %v, want ErrInvalidParam", ms, err)
		}
	}
	mustReport(t, p, "b", "a", true, 0) // 边界 0 合法
	mustFinish(t, p, "b")
	mustPlan(t, p, "b2", []string{"a"}, 1)
	mustReport(t, p, "b2", "a", true, 1_000_000_000) // 边界 1e9 合法
	mustFinish(t, p, "b2")
}

func TestReportRejectionOrder(t *testing.T) {
	p := mustNew(t, 5, 2, 2, 0, 3)
	mustPlan(t, p, "b", []string{"a"}, 1)
	mustReport(t, p, "b", "a", true, 5)
	mustFinish(t, p, "b")
	mustPlan(t, p, "b2", []string{"a"}, 1)
	mustReport(t, p, "b2", "a", true, 5) // b2/a 已有最终结果
	cases := []struct {
		name  string
		build string
		test  string
		ms    int64
		want  error
	}{
		{"invalid ms beats unknown build", "nope", "a", -1, ErrInvalidParam},
		{"unknown build", "nope", "a", 0, ErrBuildNotFound},
		{"finished beats unknown test", "b", "zzz", 0, ErrBuildFinished},
		{"unknown test", "b2", "zzz", 0, ErrTestNotInPlan},
		{"state mismatch", "b2", "a", 0, ErrState},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := p.Report(c.build, c.test, true, c.ms); !errors.Is(err, c.want) {
				t.Fatalf("Report = %v, want %v", err, c.want)
			}
		})
	}
	// 被拒绝的 Report 不改状态：b2 可正常 Finish。
	if v := mustFinish(t, p, "b2"); v != Passed {
		t.Fatalf("verdict = %v, want Passed", v)
	}
}

func TestFinishErrors(t *testing.T) {
	p := mustNew(t, 5, 2, 2, 0, 3)
	if _, err := p.Finish("nope"); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("Finish unknown = %v, want ErrBuildNotFound", err)
	}
	mustPlan(t, p, "b", []string{"a", "c"}, 1)
	mustReport(t, p, "b", "a", true, 5)
	if _, err := p.Finish("b"); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Finish incomplete = %v, want ErrIncomplete", err)
	}
	// 被拒绝的 Finish 不改状态：补上 c 后仍可 Finish。
	mustReport(t, p, "b", "c", true, 5)
	if v := mustFinish(t, p, "b"); v != Passed {
		t.Fatalf("verdict = %v, want Passed", v)
	}
	if _, err := p.Finish("b"); !errors.Is(err, ErrBuildFinished) {
		t.Fatalf("Finish again = %v, want ErrBuildFinished", err)
	}
}

// flakyBuild 让 x 先失败后通过（Flaky）并 Finish。
func flakyBuild(t *testing.T, p *Planner, build string) {
	t.Helper()
	mustPlan(t, p, build, []string{"x"}, 1)
	mustReport(t, p, build, "x", false, 1)
	mustReport(t, p, build, "x", true, 10)
	if v := mustFinish(t, p, build); v != Passed {
		t.Fatalf("%s verdict = %v, want Passed", build, v)
	}
}

// cleanBuild 让 x 首次即通过（Clean）并 Finish。
func cleanBuild(t *testing.T, p *Planner, build string) {
	t.Helper()
	passBuild(t, p, build, []string{"x"}, 10)
}

func TestSpecScenarioQuarantineLifecycle(t *testing.T) {
	// W=5,F=2,P=2,R=1：规格中的完整隔离/解除生命周期。
	p := mustNew(t, 5, 2, 2, 1, 3)
	flakyBuild(t, p, "b1") // 窗口 [Flaky]
	cleanBuild(t, p, "b2") // 窗口 [Flaky, Clean]
	flakyBuild(t, p, "b3") // 窗口含 2 个 Flaky → x 被隔离
	// 构建 4 在隔离之后 Plan：x 进隔离分片，Broken 不影响判定。
	got := mustPlan(t, p, "b4", []string{"x"}, 1)
	if want := [][]string{nil, {"x"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("b4 shards = %v, want %v", got, want)
	}
	mustReport(t, p, "b4", "x", false, 1)
	mustReport(t, p, "b4", "x", false, 1) // Broken
	if v := mustFinish(t, p, "b4"); v != Passed {
		t.Fatalf("b4 verdict = %v, want Passed (x quarantined in snapshot)", v)
	}
	cleanBuild(t, p, "b5") // 连续干净 1
	cleanBuild(t, p, "b6") // 连续干净 2 = P → 解除隔离，窗口清空
	// 解除后 x 回到常规分片。
	got = mustPlan(t, p, "b7", []string{"x"}, 1)
	if want := [][]string{{"x"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("b7 shards = %v, want %v", got, want)
	}
	mustReport(t, p, "b7", "x", true, 10)
	mustFinish(t, p, "b7")
	// 窗口已清空需重新累计：一个 Flaky 不足以再次隔离。
	flakyBuild(t, p, "b8")
	got = mustPlan(t, p, "b9", []string{"x"}, 1)
	if want := [][]string{{"x"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("b9 shards = %v, want %v (window cleared on release)", got, want)
	}
	mustReport(t, p, "b9", "x", true, 10)
	mustFinish(t, p, "b9")
}

func TestSnapshotVsRealtimeReverseDirection(t *testing.T) {
	// 构建 4 在构建 3 Finish 之前 Plan：快照中 x 未隔离。
	p := mustNew(t, 5, 2, 2, 1, 3)
	flakyBuild(t, p, "b1") // 窗口 [Flaky]
	cleanBuild(t, p, "b2") // 窗口 [Flaky, Clean]
	mustPlan(t, p, "b3", []string{"x"}, 1)
	mustPlan(t, p, "b4", []string{"x"}, 1) // 快照：x 未隔离
	// b3 先 Finish：窗口含 2 个 Flaky → x 被隔离。
	mustReport(t, p, "b3", "x", false, 1)
	mustReport(t, p, "b3", "x", true, 10)
	mustFinish(t, p, "b3")
	// b4 中 x 两次全失败（Broken）：快照未隔离 → Failed。
	mustReport(t, p, "b4", "x", false, 1)
	mustReport(t, p, "b4", "x", false, 1)
	if v := mustFinish(t, p, "b4"); v != Failed {
		t.Fatalf("b4 verdict = %v, want Failed (snapshot not quarantined)", v)
	}
	// b4 Finish 时 x 已被隔离：标记不入窗口、连续干净数归 0。
	// 之后 b5、b6 各一次 Clean 达 P=2 解除，b7 回到常规分片。
	cleanBuild(t, p, "b5")
	cleanBuild(t, p, "b6")
	got := mustPlan(t, p, "b7", []string{"x"}, 1)
	if want := [][]string{{"x"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("b7 shards = %v, want %v", got, want)
	}
	mustReport(t, p, "b7", "x", true, 10)
	mustFinish(t, p, "b7")
}

func TestReportTouchedCounter(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		t.Run(fmt.Sprintf("tests=%d", n), func(t *testing.T) {
			p := mustNew(t, 5, 2, 2, 1, 3)
			tests := make([]string, n)
			for i := range tests {
				tests[i] = fmt.Sprintf("t%05d", i)
			}
			mustPlan(t, p, "b", tests, 4)
			for _, test := range []string{tests[0], tests[n/2], tests[n-1]} {
				mustReport(t, p, "b", test, true, 5)
				if p.touched > 2 {
					t.Fatalf("n=%d: touched = %d, want <= 2", n, p.touched)
				}
			}
		})
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() ([][][]string, []Verdict) {
		p := mustNew(t, 5, 2, 2, 1, 3)
		var shards [][][]string
		var verdicts []Verdict
		script := []struct {
			test string
			pass []bool // 每次尝试的通过与否
		}{
			{"a", []bool{true}},
			{"b", []bool{false, true}},
			{"c", []bool{false, true}},
			{"d", []bool{false, false}},
			{"e", []bool{true}},
		}
		for round := 0; round < 4; round++ {
			var names []string
			for _, s := range script {
				names = append(names, s.test)
			}
			build := fmt.Sprintf("b%d", round)
			sh, err := p.Plan(build, names, 2)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			shards = append(shards, sh)
			for _, s := range script {
				for _, pass := range s.pass {
					if err := p.Report(build, s.test, pass, 5); err != nil {
						t.Fatalf("Report: %v", err)
					}
				}
			}
			v, err := p.Finish(build)
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			verdicts = append(verdicts, v)
		}
		return shards, verdicts
	}
	s1, v1 := run()
	s2, v2 := run()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("shards differ across replay:\n%v\n%v", s1, s2)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("verdicts differ across replay:\n%v\n%v", v1, v2)
	}
}

func TestConcurrentUse(t *testing.T) {
	p := mustNew(t, 5, 2, 2, 1, 3)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			build := fmt.Sprintf("b%d", i)
			test := fmt.Sprintf("t%d", i%8)
			if _, err := p.Plan(build, []string{test}, 2); err != nil {
				t.Error(err)
				return
			}
			if i%3 == 0 {
				_ = p.Report(build, test, false, 1)
			}
			if err := p.Report(build, test, true, 5); err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Finish(build); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}
