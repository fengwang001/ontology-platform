package ontology

import (
	"errors"
	"sync"
	"testing"
)

func newTestExecutor(t *testing.T, local, epoch, global, threshold, ban int64, program int) *Executor {
	t.Helper()
	executor, err := NewExecutor(local, epoch, global, threshold, ban, program)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func TestStarPatternStepsFromSpecification(t *testing.T) {
	prog, err := compileMustParse(t, `a*b`)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(prog.code); got != 5 {
		t.Fatalf("instructions = %d, want 5", got)
	}
	cases := []struct {
		input string
		memo  bool
		kind  string
		end   int
		steps int64
	}{
		{"aab", false, OutcomeMatch, 3, 10},
		{"aac", false, OutcomeNoMatch, 0, 24},
		{"aac", true, OutcomeNoMatch, 0, 14},
	}
	for _, tc := range cases {
		outcome := runProgram(prog, []byte(tc.input), 1000, tc.memo, OutcomeLocalLimited)
		if outcome.kind != tc.kind || outcome.end != tc.end || outcome.steps != tc.steps {
			t.Fatalf("input=%q memo=%v: got (%s,%d,%d), want (%s,%d,%d)",
				tc.input, tc.memo, outcome.kind, outcome.end, outcome.steps, tc.kind, tc.end, tc.steps)
		}
	}
}

func TestLimitAtExactlyLambda(t *testing.T) {
	prog, _ := compileMustParse(t, `a*b`)
	limited := runProgram(prog, []byte("aac"), 23, false, OutcomeLocalLimited)
	if limited.kind != OutcomeLocalLimited || limited.steps != 23 {
		t.Fatalf("limit 23: got %+v", limited)
	}
	complete := runProgram(prog, []byte("aac"), 24, false, OutcomeLocalLimited)
	if complete.kind != OutcomeNoMatch || complete.steps != 24 {
		t.Fatalf("limit 24: got %+v", complete)
	}
}

func TestSuccessRequiringLambdaPlusOne(t *testing.T) {
	prog, _ := compileMustParse(t, `aa`)
	limited := runProgram(prog, []byte("aa"), 2, false, OutcomeLocalLimited)
	if limited.kind != OutcomeLocalLimited || limited.steps != 2 {
		t.Fatalf("two-step match at lambda 2 should limit before Match: %+v", limited)
	}
	success := runProgram(prog, []byte("aa"), 3, false, OutcomeLocalLimited)
	if success.kind != OutcomeMatch || success.start != 0 || success.end != 2 || success.steps != 3 {
		t.Fatalf("three-step match: %+v", success)
	}
}

func TestUnboundedAndZeroOneQuantifierBoundaries(t *testing.T) {
	matchCase(t, `a{0,}b`, "b", true)
	matchCase(t, `a{0,}b`, "aab", true)
	matchCase(t, `a{0,1}b`, "b", true)
	matchCase(t, `a{0,1}b`, "ab", true)
	prog, err := compileMustParse(t, `a{0,1}b`)
	if err != nil {
		t.Fatal(err)
	}
	outcome := runProgram(prog, []byte("aab"), 1000, false, OutcomeLocalLimited)
	if outcome.kind != OutcomeMatch || outcome.start != 1 || outcome.end != 3 {
		t.Fatalf("search interval = %+v, want [1,3)", outcome)
	}
	matchCase(t, `a{100,}`, string(repeatBytes('a', 100)), true)
	matchCase(t, `a{100,}`, string(repeatBytes('a', 99)), false)
}

func repeatBytes(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}

func TestBudgetAndBanEscalationExample(t *testing.T) {
	executor := newTestExecutor(t, 10, 100, 20, 2, 50, 1000)
	if err := executor.Register("p", []byte(`a*b`), false); err != nil {
		t.Fatal(err)
	}

	r1, err := executor.Match("p", []byte("aac"), 0)
	if err != nil || r1.Kind != OutcomeLocalLimited || r1.Steps != 10 || executor.remaining != 10 {
		t.Fatalf("first local limit: result=%+v err=%v rem=%d", r1, err, executor.remaining)
	}
	r2, err := executor.Match("p", []byte("aac"), 10)
	if err != nil || r2.Kind != OutcomeLocalLimited || r2.Steps != 10 || executor.remaining != 0 {
		t.Fatalf("second local limit: result=%+v err=%v rem=%d", r2, err, executor.remaining)
	}
	status, _ := executor.Status("p", 10)
	if status.BanCount != 1 || status.BannedUntil != 60 || status.ConsecutiveLocalLimits != 0 || !status.Banned {
		t.Fatalf("status at 10 = %+v", status)
	}
	if _, err := executor.Match("p", []byte("aac"), 20); !errors.Is(err, ErrBanned) || executor.maxNow != 10 {
		t.Fatalf("banned call: err=%v maxNow=%d", err, executor.maxNow)
	}
	if _, err := executor.Match("p", []byte("aab"), 60); !errors.Is(err, ErrGlobalBudget) || executor.maxNow != 10 {
		t.Fatalf("zero global call: err=%v maxNow=%d", err, executor.maxNow)
	}
	r4, err := executor.Match("p", []byte("aab"), 100)
	if err != nil || r4.Kind != OutcomeMatch || r4.Start != 0 || r4.End != 3 || r4.Steps != 10 {
		t.Fatalf("epoch boundary match: result=%+v err=%v", r4, err)
	}
	if executor.currentEpoch != 1 || executor.remaining != 10 {
		t.Fatalf("epoch=%d remaining=%d", executor.currentEpoch, executor.remaining)
	}
	statusAtUnban, _ := executor.Status("p", 60)
	if statusAtUnban.Banned {
		t.Fatalf("status at exactly unban time = %+v", statusAtUnban)
	}
}

func TestLocalAndGlobalLimitClassification(t *testing.T) {
	cases := []struct {
		remaining int64
		kind      string
		steps     int64
		after     int64
	}{
		{10, OutcomeLocalLimited, 10, 0},
		{9, OutcomeGlobalLimited, 9, 0},
		{11, OutcomeLocalLimited, 10, 1},
	}
	for _, tc := range cases {
		executor := newTestExecutor(t, 10, 100, 20, 2, 50, 1000)
		executor.remaining = tc.remaining
		_ = executor.Register("p", []byte(`a*b`), false)
		result, err := executor.Match("p", []byte("aac"), 0)
		if err != nil || result.Kind != tc.kind || result.Steps != tc.steps || executor.remaining != tc.after {
			t.Fatalf("remaining=%d: result=%+v err=%v after=%d", tc.remaining, result, err, executor.remaining)
		}
	}

	thresholdExecutor := newTestExecutor(t, 10, 100, 20, 2, 50, 1000)
	_ = thresholdExecutor.Register("p", []byte(`a*b`), false)
	for range 2 {
		_, _ = thresholdExecutor.Match("p", []byte("aac"), 0)
	}
	status, _ := thresholdExecutor.Status("p", 0)
	if status.BanCount != 1 || status.BannedUntil != 50 {
		t.Fatalf("ban status = %+v", status)
	}
}

func TestLazyAndGreedyIntervals(t *testing.T) {
	cases := []struct {
		pattern string
		end     int
	}{
		{`a*`, 3},
		{`a*?`, 0},
		{`a+`, 3},
		{`a+?`, 1},
	}
	for _, tc := range cases {
		prog, err := compileMustParse(t, tc.pattern)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.pattern, err)
		}
		outcome := runProgram(prog, []byte("aaa"), 100, false, OutcomeLocalLimited)
		if outcome.kind != OutcomeMatch || outcome.end != tc.end {
			t.Fatalf("%s: got %+v, want end %d", tc.pattern, outcome, tc.end)
		}
	}
}

func TestQuantifierInstructionCount(t *testing.T) {
	cases := []struct {
		pattern string
		count   int
	}{
		{`a{2,4}`, 2*1 + 2*(1+1) + 1},
		{`a{3,}`, 3 + 3 + 1},
		{`a{0,1}`, 0 + 1*2 + 1},
	}
	for _, tc := range cases {
		prog, err := compileMustParse(t, tc.pattern)
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if len(prog.code) != tc.count {
			t.Fatalf("%s instructions=%d want %d: %#v", tc.pattern, len(prog.code), tc.count, prog.code)
		}
	}
}

func TestBoundedQuantifierOptionalSkipsGoToExit(t *testing.T) {
	root, err := parsePattern([]byte(`a{2,4}`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compilePattern(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	exit := len(prog.code) - 1
	splitIndexes := []int{2, 4}
	for _, pc := range splitIndexes {
		if prog.code[pc].op != opSplit || prog.code[pc].second != exit {
			t.Fatalf("pc=%d inst=%+v exit=%d", pc, prog.code[pc], exit)
		}
	}
	if prog.code[0].op != opChar || prog.code[1].op != opChar ||
		prog.code[3].op != opChar || prog.code[5].op != opChar {
		t.Fatalf("unexpected body layout: %#v", prog.code)
	}
}

func TestMemoizedAndPlainResultsAgreeWithoutLimit(t *testing.T) {
	patterns := []string{
		`a*b`, `(a|b)*abb`, `a{1,3}?b`, `[a-c]*d`, `ab|cd`, `^a*$`, `(ab?)*c?`,
	}
	inputs := []string{"", "a", "b", "ab", "aab", "cab", "abb", "ababc", "aaa", "cc"}
	for _, pattern := range patterns {
		prog, err := compileMustParse(t, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range inputs {
			plain := runProgram(prog, []byte(input), 1_000_000, false, OutcomeLocalLimited)
			memo := runProgram(prog, []byte(input), 1_000_000, true, OutcomeLocalLimited)
			if plain.kind != memo.kind || plain.start != memo.start || plain.end != memo.end {
				t.Fatalf("%s on %q: plain=%+v memo=%+v", pattern, input, plain, memo)
			}
		}
	}
}

func TestCharacterClassAndAssertions(t *testing.T) {
	matchCase(t, `[a-c-]`, "b-", true)
	matchCase(t, `[^a-c]`, "d", true)
	matchCase(t, `[^a-c]`, "b", false)
	matchCase(t, `[]`, "a", false)
	matchCase(t, `[^]`, "\n", true)
	matchCase(t, `.`, "\n", false)
	matchCase(t, `$`, "", true)
	matchCase(t, `^`, "", true)
	matchCase(t, `a$`, "ba", true)
	matchCase(t, `^a`, "ab", true)
}

func TestMemoizationBoundsCatastrophicPattern(t *testing.T) {
	prog, err := compileMustParse(t, `(a+)+b`)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("aaaaaaaaaaaaaaaaaaaa")
	plain := runProgram(prog, input, 1_000_000, false, OutcomeLocalLimited)
	if plain.kind != OutcomeLocalLimited {
		t.Fatalf("non-memoized unexpectedly finished: %+v", plain)
	}
	var dispatched int64
	memoized := runProgramWithDispatchCount(prog, input, 1_000_000, true, OutcomeLocalLimited, &dispatched)
	if memoized.kind != OutcomeNoMatch {
		t.Fatalf("memoized result = %+v", memoized)
	}
	if dispatched != memoized.steps {
		t.Fatalf("dispatched=%d steps=%d", dispatched, memoized.steps)
	}
	bound := int64(len(prog.code)) * int64(len(input)+1)
	if dispatched > bound {
		t.Fatalf("dispatched=%d exceeds bound=%d", dispatched, bound)
	}
}

func TestBanDurationEscalationAndRejectionOrder(t *testing.T) {
	executor := newTestExecutor(t, 10, 1_000_000, 10_000, 1, 50, 1000)
	_ = executor.Register("p", []byte(`a*b`), false)
	_ = executor.Register("q", []byte(`b`), false)

	for ban := int64(1); ban <= 4; ban++ {
		now := int64(1) << (ban + 5)
		result, err := executor.Match("p", []byte("aac"), now)
		if err != nil || result.Kind != OutcomeLocalLimited {
			t.Fatalf("ban %d: result=%+v err=%v", ban, result, err)
		}
		status, _ := executor.Status("p", now)
		wantDuration := []int64{0, 50, 100, 200, 400}[ban]
		if status.BanCount != ban || status.BannedUntil != now+wantDuration {
			t.Fatalf("ban %d status=%+v want until %d", ban, status, now+wantDuration)
		}
	}
}

func TestGlobalLimitLeavesCounterAndRejectionsDoNotAdvanceClock(t *testing.T) {
	executor := newTestExecutor(t, 10, 1_000_000, 5, 2, 50, 1000)
	_ = executor.Register("p", []byte(`a*b`), false)
	executor.patterns["p"].localMiss = 1
	result, err := executor.Match("p", []byte("aac"), 10)
	if err != nil || result.Kind != OutcomeGlobalLimited || result.Steps != 5 {
		t.Fatalf("global result=%+v err=%v", result, err)
	}
	status, _ := executor.Status("p", 10)
	if status.ConsecutiveLocalLimits != 1 || status.BanCount != 0 {
		t.Fatalf("global limit changed counters: %+v", status)
	}
	if _, err := executor.Match("p", nil, 11); !errors.Is(err, ErrGlobalBudget) || executor.maxNow != 10 {
		t.Fatalf("global zero: err=%v maxNow=%d", err, executor.maxNow)
	}
	if _, err := executor.Match("missing", nil, 9); !errors.Is(err, ErrNotFound) || executor.maxNow != 10 {
		t.Fatalf("missing rewind: err=%v maxNow=%d", err, executor.maxNow)
	}
	if _, err := executor.Match("p", nil, 9); !errors.Is(err, ErrClockRewind) || executor.maxNow != 10 {
		t.Fatalf("rewind: err=%v maxNow=%d", err, executor.maxNow)
	}

	executor.patterns["p"].bannedUntil = 20
	if _, err := executor.Match("p", nil, 9); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind precedes ban: %v", err)
	}
	if _, err := executor.Match("p", nil, 15); !errors.Is(err, ErrBanned) {
		t.Fatalf("ban precedes global: %v", err)
	}
}

func TestConcurrentCallsAreSafe(t *testing.T) {
	executor := newTestExecutor(t, 100, 1000, 1000, 2, 50, 1000)
	if err := executor.Register("p", []byte(`a*b`), true); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			for step := 0; step < 20; step++ {
				now := int64(id*1000 + step)
				_, _ = executor.Match("p", []byte("aab"), now)
				_, _ = executor.Status("p", now)
			}
		}(worker)
	}
	wait.Wait()
}

func TestSyntaxAndNullableRejection(t *testing.T) {
	syntaxPatterns := []string{`(`, `)`, `[`, `[z-a]`, `a**`, `a?*`, `a*?+`, `^*`, `a{2,1}`, `a{101,}`}
	for _, pattern := range syntaxPatterns {
		if _, err := parsePattern([]byte(pattern)); !errors.Is(err, ErrPatternSyntax) {
			t.Fatalf("%s: err=%v want syntax", pattern, err)
		}
	}
	nullablePatterns := []string{`(a?){0,2}?`, `()*`, `(a|)*`, `(^)*`, `(a?)*`}
	for _, pattern := range nullablePatterns {
		if _, err := parsePattern([]byte(pattern)); !errors.Is(err, ErrNullableRepeat) {
			t.Fatalf("%s: err=%v want nullable", pattern, err)
		}
	}
}

func compileMustParse(t *testing.T, pattern string) (*program, error) {
	t.Helper()
	root, err := parsePattern([]byte(pattern))
	if err != nil {
		return nil, err
	}
	return compilePattern(root, 1000)
}

func matchCase(t *testing.T, pattern, input string, want bool) {
	t.Helper()
	prog, err := compileMustParse(t, pattern)
	if err != nil {
		t.Fatal(err)
	}
	outcome := runProgram(prog, []byte(input), 1000, false, OutcomeLocalLimited)
	got := outcome.kind == OutcomeMatch
	if got != want {
		t.Fatalf("%s on %q: match=%v want %v (%+v)", pattern, input, got, want, outcome)
	}
}
