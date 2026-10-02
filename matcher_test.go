package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type naiveMatcher struct {
	lastHour int
	commits  map[int]commit
}

func newNaiveMatcher() *naiveMatcher {
	return &naiveMatcher{lastHour: -1, commits: make(map[int]commit)}
}

func (m *naiveMatcher) add(candidate commit) error {
	if candidate.id < 0 || candidate.id > 1_000_000 ||
		candidate.family < 0 || candidate.family > 99 ||
		candidate.start < 0 ||
		candidate.hours < 1 || candidate.hours > 100_000 ||
		candidate.hourly < 1 || candidate.hourly > 1_000_000_000 ||
		candidate.upfront < 0 || candidate.upfront > 1_000_000_000_000 ||
		candidate.discount < 1 || candidate.discount > 9_999 {
		return ErrInvalidArgument
	}
	if _, exists := m.commits[candidate.id]; exists {
		return ErrDuplicateCommit
	}
	if candidate.start <= m.lastHour {
		return ErrCommitStarted
	}
	m.commits[candidate.id] = candidate
	return nil
}

func (m *naiveMatcher) apply(t int, lines []UsageLine) ([]HourReport, error) {
	if t < 0 || t > m.lastHour+10_000 || len(lines) > 1_000 {
		return nil, ErrInvalidArgument
	}
	for _, line := range lines {
		if line.Family < 1 || line.Family > 99 || line.OnDemand < 1 || line.OnDemand > 1_000_000_000 {
			return nil, ErrInvalidArgument
		}
	}
	if t <= m.lastHour {
		return nil, ErrTimeRegression
	}

	reports := make([]HourReport, 0, t-m.lastHour)
	for hour := m.lastHour + 1; hour <= t; hour++ {
		currentLines := []UsageLine{}
		if hour == t {
			currentLines = lines
		}
		reports = append(reports, m.processHour(hour, currentLines))
	}
	m.lastHour = t
	return reports, nil
}

func (m *naiveMatcher) processHour(hour int, lines []UsageLine) HourReport {
	active := make([]commit, 0)
	for _, candidate := range m.commits {
		if candidate.start <= hour && hour < candidate.start+candidate.hours {
			active = append(active, candidate)
		}
	}
	sortByRule(active)

	remaining := make([]int64, len(lines))
	for i, line := range lines {
		remaining[i] = line.OnDemand
	}
	report := HourReport{Hour: hour, Commits: make([]CommitReport, 0, len(active))}
	for _, candidate := range active {
		capacity := candidate.hourly
		covered := int64(0)
		factor := 10_000 - candidate.discount
		for i, line := range lines {
			if capacity == 0 {
				break
			}
			if remaining[i] == 0 || (candidate.family != 0 && candidate.family != line.Family) {
				continue
			}
			effective := ceilDiv(remaining[i]*factor, 10_000)
			if capacity >= effective {
				capacity -= effective
				covered += remaining[i]
				remaining[i] = 0
			} else {
				partial := capacity * 10_000 / factor
				if !(1 <= partial && partial < remaining[i]) {
					panic(fmt.Sprintf("invalid partial cover: q=%d p=%d", partial, remaining[i]))
				}
				remaining[i] -= partial
				covered += partial
				capacity = 0
			}
		}
		report.Bill += candidate.hourly + amortization(candidate, hour)
		report.Commits = append(report.Commits, CommitReport{
			ID:      candidate.id,
			Used:    candidate.hourly - capacity,
			Unused:  capacity,
			Covered: covered,
		})
	}
	for _, value := range remaining {
		report.Bill += value
	}
	return report
}

func sortByRule(commits []commit) {
	for i := 0; i < len(commits); i++ {
		for j := i + 1; j < len(commits); j++ {
			if lessCommit(commits[j], commits[i]) {
				commits[i], commits[j] = commits[j], commits[i]
			}
		}
	}
}

func mustAdd(t *testing.T, matcher *CommitMatcher, candidate commit) {
	t.Helper()
	if err := matcher.AddCommit(candidate.id, candidate.family, candidate.start, candidate.hours, candidate.hourly, candidate.upfront, candidate.discount); err != nil {
		t.Fatalf("AddCommit(%+v): %v", candidate, err)
	}
}

func assertReports(t *testing.T, expected, actual []HourReport) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("reports differ\nexpected: %+v\nactual:   %+v\n判定依据: 小时、账单及每承诺 used/unused/covered 全量一致", expected, actual)
	}
}

func TestProvidedExample(t *testing.T) {
	matcher := NewCommitMatcher()
	mustAdd(t, matcher, commit{id: 1, family: 0, start: 0, hours: 3, hourly: 100, upfront: 100, discount: 3000})
	mustAdd(t, matcher, commit{id: 2, family: 7, start: 0, hours: 2, hourly: 50, upfront: 0, discount: 5000})

	first, err := matcher.Apply(0, []UsageLine{{Family: 7, OnDemand: 120}, {Family: 5, OnDemand: 200}})
	if err != nil {
		t.Fatal(err)
	}
	expectedFirst := []HourReport{{
		Hour: 0,
		Bill: 261,
		Commits: []CommitReport{
			{ID: 2, Used: 50, Covered: 100},
			{ID: 1, Used: 100, Covered: 142},
		},
	}}
	assertReports(t, expectedFirst, first)

	second, err := matcher.Apply(3, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedSecond := []HourReport{
		{Hour: 1, Bill: 183, Commits: []CommitReport{{ID: 2, Unused: 50}, {ID: 1, Unused: 100}}},
		{Hour: 2, Bill: 134, Commits: []CommitReport{{ID: 1, Unused: 100}}},
		{Hour: 3, Bill: 0, Commits: []CommitReport{}},
	}
	assertReports(t, expectedSecond, second)
}

func TestTieDiscountAndPartialBoundary(t *testing.T) {
	matcher := NewCommitMatcher()
	mustAdd(t, matcher, commit{id: 10, family: 1, start: 0, hours: 1, hourly: 50, discount: 5000})
	mustAdd(t, matcher, commit{id: 2, family: 1, start: 0, hours: 1, hourly: 50, discount: 5000})

	reports, err := matcher.Apply(0, []UsageLine{{Family: 1, OnDemand: 101}})
	if err != nil {
		t.Fatal(err)
	}
	expected := []HourReport{{
		Hour: 0,
		Bill: 100,
		Commits: []CommitReport{
			{ID: 2, Used: 50, Covered: 100},
			{ID: 10, Used: 1, Unused: 49, Covered: 1},
		},
	}}
	assertReports(t, expected, reports)
}

func TestEffectivePriceBoundaries(t *testing.T) {
	matcher := NewCommitMatcher()
	mustAdd(t, matcher, commit{id: 1, family: 1, start: 0, hours: 1, hourly: 51, discount: 4900})
	exact, err := matcher.Apply(0, []UsageLine{{Family: 1, OnDemand: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if got := exact[0].Commits[0]; got != (CommitReport{ID: 1, Used: 51, Covered: 100}) {
		t.Fatalf("R==e should fully cover line, got %+v; 判定依据: ceil(100*5100/10000)=51", got)
	}

	matcher = NewCommitMatcher()
	mustAdd(t, matcher, commit{id: 1, family: 1, start: 0, hours: 1, hourly: 50, discount: 4900})
	partial, err := matcher.Apply(0, []UsageLine{{Family: 1, OnDemand: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if got := partial[0].Commits[0]; got != (CommitReport{ID: 1, Used: 50, Covered: 98}) {
		t.Fatalf("R==e-1 should partially cover, got %+v; 判定依据: floor(50*10000/5100)=98", got)
	}
}

func TestFamilyMatchingAndRejectionsDoNotChangeState(t *testing.T) {
	matcher := NewCommitMatcher()
	mustAdd(t, matcher, commit{id: 1, family: 7, start: 0, hours: 1, hourly: 100, discount: 5000})
	mustAdd(t, matcher, commit{id: 2, family: 0, start: 0, hours: 1, hourly: 100, discount: 2000})

	reports, err := matcher.Apply(0, []UsageLine{{Family: 8, OnDemand: 100}})
	if err != nil {
		t.Fatal(err)
	}
	expected := []HourReport{{
		Hour: 0,
		Bill: 200,
		Commits: []CommitReport{
			{ID: 1, Unused: 100},
			{ID: 2, Used: 80, Unused: 20, Covered: 100},
		},
	}}
	assertReports(t, expected, reports)

	if err := matcher.AddCommit(1, 0, 1, 1, 1, 0, 10); err != ErrDuplicateCommit {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := matcher.Apply(0, nil); err != ErrTimeRegression {
		t.Fatalf("regression error = %v", err)
	}
	if err := matcher.AddCommit(3, 0, 0, 1, 1, 0, 10); err != ErrCommitStarted {
		t.Fatalf("started error = %v", err)
	}
	if err := matcher.AddCommit(4, 100, 0, 0, 0, 0, 0); err != ErrInvalidArgument {
		t.Fatalf("invalid error = %v", err)
	}
	if matcher.lastHour != 0 || len(matcher.commits) != 2 {
		t.Fatalf("rejected operation changed state: lastHour=%d commits=%d", matcher.lastHour, len(matcher.commits))
	}
}

func TestValidationOrderAndSkipAcrossExpiry(t *testing.T) {
	matcher := NewCommitMatcher()
	if err := matcher.AddCommit(1, 0, 0, 0, 1, 0, 10); err != ErrInvalidArgument {
		t.Fatalf("invalid arguments must precede duplicate: %v", err)
	}
	mustAdd(t, matcher, commit{id: 1, family: 0, start: 0, hours: 2, hourly: 10, upfront: 11, discount: 1000})
	if _, err := matcher.Apply(0, []UsageLine{{Family: 0, OnDemand: 1}}); err != ErrInvalidArgument {
		t.Fatalf("invalid arguments must precede regression: %v", err)
	}
	if _, err := matcher.Apply(10_002, nil); err != ErrInvalidArgument {
		t.Fatalf("jump limit error = %v", err)
	}

	reports, err := matcher.Apply(2, []UsageLine{{Family: 3, OnDemand: 100}})
	if err != nil {
		t.Fatal(err)
	}
	expected := []HourReport{
		{Hour: 0, Bill: 15, Commits: []CommitReport{{ID: 1, Unused: 10}}},
		{Hour: 1, Bill: 16, Commits: []CommitReport{{ID: 1, Unused: 10}}},
		{Hour: 2, Bill: 100, Commits: []CommitReport{}},
	}
	assertReports(t, expected, reports)
}

func TestConcurrentAddsSerialize(t *testing.T) {
	matcher := NewCommitMatcher()
	var wait sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 50)
	for id := 0; id < 50; id++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			<-start
			err := matcher.AddCommit(id, id%99+1, 1, 1, 1, 0, int64(id+1))
			if err != nil {
				errs <- err
			}
		}(id)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	reports, err := matcher.Apply(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reports[1].Bill != 50 || len(reports[1].Commits) != 50 {
		t.Fatalf("concurrent additions did not serialize: bill=%d commits=%d", reports[1].Bill, len(reports[1].Commits))
	}
}

type testOperation struct {
	kind   string
	commit commit
	hour   int
	lines  []UsageLine
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		random := rand.New(rand.NewSource(seed))
		actualMatcher := NewCommitMatcher()
		expectedMatcher := newNaiveMatcher()
		usedIDs := make(map[int]bool)
		operations := make([]testOperation, 0, 18)
		outputs := make([]string, 0, 18)

		for step := 0; step < 16; step++ {
			if random.Intn(2) == 0 {
				candidate := randomCommit(random, expectedMatcher.lastHour, usedIDs)
				operations = append(operations, testOperation{kind: "add", commit: candidate})
			} else {
				hour := expectedMatcher.lastHour + 1 + random.Intn(5)
				operations = append(operations, testOperation{kind: "apply", hour: hour, lines: randomLines(random)})
			}
		}

		for index, operation := range operations {
			var actualErr, expectedErr error
			var actualReports, expectedReports []HourReport
			if operation.kind == "add" {
				candidate := operation.commit
				expectedErr = expectedMatcher.add(candidate)
				actualErr = actualMatcher.AddCommit(candidate.id, candidate.family, candidate.start, candidate.hours, candidate.hourly, candidate.upfront, candidate.discount)
			} else {
				expectedReports, expectedErr = expectedMatcher.apply(operation.hour, operation.lines)
				actualReports, actualErr = actualMatcher.Apply(operation.hour, operation.lines)
			}
			outputs = append(outputs, fmt.Sprintf("reports=%+v err=%v", actualReports, actualErr))

			if expectedErr != actualErr {
				t.Fatalf("seed=%d op=%d 输入=%+v\n期望输出=%+v 期望错误=%v\n实际输出=%+v 实际错误=%v\n判定依据=拒绝错误与报告必须完全一致", seed, index, operations, expectedReports, expectedErr, actualReports, actualErr)
			}
			if !reflect.DeepEqual(expectedReports, actualReports) {
				t.Fatalf("seed=%d op=%d 输入序列=%+v\n期望输出=%+v\n实际输出=%+v\n判定依据=朴素模拟逐行扫描与实现结果不同", seed, index, operations, expectedReports, actualReports)
			}
		}
		t.Logf("seed=%d\n输入=%+v\n输出=%+v\n判定依据=全部操作与朴素模拟的错误和小时报告逐一一致", seed, operations, outputs)
	}
}

func TestReplayDeterminism(t *testing.T) {
	operations := []testOperation{
		{kind: "add", commit: commit{id: 1, family: 0, start: 0, hours: 3, hourly: 37, upfront: 10, discount: 3333}},
		{kind: "add", commit: commit{id: 2, family: 4, start: 0, hours: 2, hourly: 21, upfront: 3, discount: 7777}},
		{kind: "apply", hour: 1, lines: []UsageLine{{Family: 4, OnDemand: 80}, {Family: 9, OnDemand: 44}}},
		{kind: "apply", hour: 4, lines: []UsageLine{{Family: 4, OnDemand: 10}}},
	}
	first := replayOperations(t, operations)
	second := replayOperations(t, operations)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay differs: first=%+v second=%+v", first, second)
	}
}

func replayOperations(t *testing.T, operations []testOperation) []HourReport {
	t.Helper()
	matcher := NewCommitMatcher()
	allReports := make([]HourReport, 0)
	for _, operation := range operations {
		if operation.kind == "add" {
			candidate := operation.commit
			if err := matcher.AddCommit(candidate.id, candidate.family, candidate.start, candidate.hours, candidate.hourly, candidate.upfront, candidate.discount); err != nil {
				t.Fatal(err)
			}
		} else {
			reports, err := matcher.Apply(operation.hour, operation.lines)
			if err != nil {
				t.Fatal(err)
			}
			allReports = append(allReports, reports...)
		}
	}
	return allReports
}

func randomCommit(random *rand.Rand, lastHour int, used map[int]bool) commit {
	var id int
	for {
		id = random.Intn(40)
		if !used[id] {
			used[id] = true
			break
		}
	}
	family := random.Intn(4)
	start := lastHour + 1 + random.Intn(8)
	return commit{
		id:       id,
		family:   family,
		start:    start,
		hours:    1 + random.Intn(6),
		hourly:   int64(1 + random.Intn(120)),
		upfront:  int64(random.Intn(13)),
		discount: int64(1 + random.Intn(9_999)),
	}
}

func randomLines(random *rand.Rand) []UsageLine {
	count := random.Intn(6)
	lines := make([]UsageLine, count)
	for i := range lines {
		lines[i] = UsageLine{
			Family:   1 + random.Intn(5),
			OnDemand: int64(1 + random.Intn(180)),
		}
	}
	return lines
}
