package registeralloc

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func TestNewRejectsInvalidK(t *testing.T) {
	if _, err := New(0); err != ErrInvalidRegisterCount {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidRegisterCount)
	}
}

type operation struct {
	id    int
	start int
	end   int
	query bool
}

type expectedResult struct {
	placement Placement
	err       error
}

type simulatedInterval struct {
	start     int
	end       int
	placement Placement
}

type naiveSimulator struct {
	intervals    map[int]simulatedInterval
	active       map[int]simulatedInterval
	free         map[int]struct{}
	lastStart    int
	hasLastStart bool
	spillCount   int
}

func newNaiveSimulator(k int) *naiveSimulator {
	free := make(map[int]struct{}, k)
	for register := 0; register < k; register++ {
		free[register] = struct{}{}
	}

	return &naiveSimulator{
		intervals: make(map[int]simulatedInterval),
		active:    make(map[int]simulatedInterval),
		free:      free,
	}
}

func (s *naiveSimulator) step(op operation) expectedResult {
	if op.query {
		current, exists := s.intervals[op.id]
		if !exists {
			return expectedResult{err: ErrIntervalNotFound}
		}
		return expectedResult{placement: current.placement}
	}

	if _, exists := s.intervals[op.id]; exists {
		return expectedResult{err: ErrDuplicateInterval}
	}
	if op.start >= op.end {
		return expectedResult{err: ErrInvalidRange}
	}
	if s.hasLastStart && op.start < s.lastStart {
		return expectedResult{err: ErrNonMonotonicStart}
	}

	for activeID, current := range s.active {
		if current.end <= op.start {
			s.free[current.placement.Register] = struct{}{}
			delete(s.active, activeID)
		}
	}

	placement := Placement{}
	if register, found := smallestSimulatedFreeRegister(s.free); found {
		delete(s.free, register)
		placement = Placement{Kind: InRegister, Register: register}
	} else {
		victimID := op.id
		victimEnd := op.end
		for activeID, current := range s.active {
			switch {
			case current.end > victimEnd:
				victimID = activeID
				victimEnd = current.end
			case current.end == victimEnd && victimID != op.id && activeID < victimID:
				victimID = activeID
			}
		}

		if victimID == op.id {
			placement = Placement{Kind: Spilled, SpillSlot: s.spillCount}
			s.spillCount++
		} else {
			victim := s.active[victimID]
			register := victim.placement.Register
			victim.placement = Placement{Kind: Spilled, SpillSlot: s.spillCount}
			s.spillCount++
			s.intervals[victimID] = victim
			delete(s.active, victimID)
			placement = Placement{Kind: InRegister, Register: register}
		}
	}

	next := simulatedInterval{start: op.start, end: op.end, placement: placement}
	s.intervals[op.id] = next
	if placement.Kind == InRegister {
		s.active[op.id] = next
	}
	s.lastStart = op.start
	s.hasLastStart = true

	return expectedResult{placement: placement}
}

func smallestSimulatedFreeRegister(free map[int]struct{}) (int, bool) {
	register := -1
	for candidate := range free {
		if register == -1 || candidate < register {
			register = candidate
		}
	}
	if register == -1 {
		return 0, false
	}
	return register, true
}

func replayAgainstNaive(t *testing.T, k int, ops []operation) {
	t.Helper()

	allocator, err := New(k)
	if err != nil {
		t.Fatalf("New(%d) returned error: %v", k, err)
	}
	simulator := newNaiveSimulator(k)

	for index, op := range ops {
		expected := simulator.step(op)
		if op.query {
			got, gotErr := allocator.Query(op.id)
			assertResult(t, index, op, expected, got, gotErr)
			continue
		}

		got, gotErr := allocator.Add(op.id, op.start, op.end)
		assertResult(t, index, op, expected, got, gotErr)
	}
}

func assertResult(t *testing.T, index int, op operation, expected expectedResult, got Placement, gotErr error) {
	t.Helper()

	if !errors.Is(gotErr, expected.err) {
		t.Fatalf("step %d %s: error = %v, want %v", index, describeOperation(op), gotErr, expected.err)
	}
	if gotErr == nil && !reflect.DeepEqual(got, expected.placement) {
		t.Fatalf("step %d %s: placement = %+v, want %+v", index, describeOperation(op), got, expected.placement)
	}

	if op.query {
		t.Logf("判定: 查询不存在=%v；输入=%s；输出=%s", gotErr != nil, describeOperation(op), describePlacement(got, gotErr))
		return
	}

	reason := "成功"
	switch {
	case errors.Is(gotErr, ErrDuplicateInterval):
		reason = "拒绝：编号已存在，后续规则不检查且状态不变"
	case errors.Is(gotErr, ErrInvalidRange):
		reason = "拒绝：起点必须严格小于终点，状态不变"
	case errors.Is(gotErr, ErrNonMonotonicStart):
		reason = "拒绝：起点小于上一次成功添加的起点，状态不变"
	case got.Kind == InRegister:
		reason = "释放 end<=start 后取最小空闲寄存器"
	default:
		reason = "无空闲寄存器；按最大终点、新区间优先、活跃编号最小规则溢出"
	}
	t.Logf("判定: %s；输入=%s；输出=%s", reason, describeOperation(op), describePlacement(got, gotErr))
}

func describeOperation(op operation) string {
	if op.query {
		return "Query(" + itoa(op.id) + ")"
	}
	return "Add(" + itoa(op.id) + ", " + itoa(op.start) + ", " + itoa(op.end) + ")"
}

func describePlacement(placement Placement, err error) string {
	if err != nil {
		return "error=" + err.Error()
	}
	if placement.Kind == Spilled {
		return "spill slot " + itoa(placement.SpillSlot)
	}
	return "register " + itoa(placement.Register)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func TestReuseRegisterWhenEndEqualsNewStart(t *testing.T) {
	ops := []operation{
		{id: 1, start: 0, end: 2},
		{id: 2, start: 1, end: 3},
		{id: 3, start: 2, end: 4},
		{id: 1, query: true},
	}

	replayAgainstNaive(t, 2, ops)
}

func TestNewIntervalSpillsOnEqualEnd(t *testing.T) {
	ops := []operation{
		{id: 1, start: 0, end: 5},
		{id: 2, start: 1, end: 5},
		{id: 3, start: 2, end: 5},
		{id: 1, query: true},
		{id: 2, query: true},
		{id: 3, query: true},
	}

	replayAgainstNaive(t, 2, ops)
}

func TestActiveTieChoosesSmallestID(t *testing.T) {
	ops := []operation{
		{id: 8, start: 0, end: 10},
		{id: 3, start: 1, end: 9},
		{id: 5, start: 2, end: 9},
		{id: 7, start: 3, end: 8},
		{id: 3, query: true},
		{id: 7, query: true},
	}

	replayAgainstNaive(t, 2, ops)
}

func TestKOneRepeatedSpillsAndRegisterReuse(t *testing.T) {
	ops := []operation{
		{id: 1, start: 0, end: 10},
		{id: 2, start: 1, end: 9},
		{id: 3, start: 2, end: 8},
		{id: 4, start: 10, end: 11},
		{id: 1, query: true},
		{id: 4, query: true},
	}

	replayAgainstNaive(t, 1, ops)
}

func TestSpilledRegisterIsReusedBeforeNewSpill(t *testing.T) {
	ops := []operation{
		{id: 1, start: 0, end: 10},
		{id: 2, start: 0, end: 9},
		{id: 3, start: 1, end: 8},
		{id: 2, query: true},
		{id: 3, query: true},
	}

	replayAgainstNaive(t, 2, ops)
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	allocator, err := New(1)
	if err != nil {
		t.Fatal(err)
	}

	first, err := allocator.Add(1, 2, 4)
	if err != nil || first != (Placement{Kind: InRegister, Register: 0}) {
		t.Fatalf("first Add = %+v, %v", first, err)
	}

	invalidOperations := []operation{
		{id: 1, start: 5, end: 4},
		{id: 2, start: 5, end: 4},
		{id: 3, start: 1, end: 2},
		{id: 4, start: 3, end: 3},
	}
	wantErrors := []error{
		ErrDuplicateInterval,
		ErrInvalidRange,
		ErrNonMonotonicStart,
		ErrInvalidRange,
	}

	for index, op := range invalidOperations {
		got, gotErr := allocator.Add(op.id, op.start, op.end)
		if !errors.Is(gotErr, wantErrors[index]) {
			t.Fatalf("invalid Add %s: error = %v, want %v", describeOperation(op), gotErr, wantErrors[index])
		}
		if got != (Placement{}) {
			t.Fatalf("invalid Add %s returned non-zero placement %+v", describeOperation(op), got)
		}
		t.Logf("判定: %s；输入=%s；输出=%s", wantErrors[index], describeOperation(op), gotErr)
	}

	next, err := allocator.Add(5, 3, 5)
	if err != nil || next != (Placement{Kind: Spilled, SpillSlot: 0}) {
		t.Fatalf("Add after rejects = %+v, %v; rejected operations changed state", next, err)
	}
	if _, err := allocator.Query(42); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("Query missing id error = %v, want %v", err, ErrIntervalNotFound)
	}
	if got, err := allocator.Query(1); err != nil || got != first {
		t.Fatalf("Query existing interval after failed operations = %+v, %v", got, err)
	}
}

func TestConcurrentAddsAndQueriesAreSerializable(t *testing.T) {
	allocator, err := New(4)
	if err != nil {
		t.Fatal(err)
	}

	first, err := allocator.Add(0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	concurrentPlacements := make(chan Placement, 3)
	waitGroup.Add(4)
	for concurrentID := 1; concurrentID <= 3; concurrentID++ {
		id := concurrentID
		go func() {
			defer waitGroup.Done()
			placement, addErr := allocator.Add(id, 0, 2)
			if addErr != nil {
				t.Errorf("concurrent Add(%d) returned error: %v", id, addErr)
				return
			}
			concurrentPlacements <- placement
		}()
	}
	go func() {
		defer waitGroup.Done()
		for queryIndex := 0; queryIndex < 100; queryIndex++ {
			placement, queryErr := allocator.Query(0)
			if queryErr != nil || placement != first {
				t.Errorf("concurrent Query(0) = %+v, %v", placement, queryErr)
				return
			}
		}
	}()
	waitGroup.Wait()
	close(concurrentPlacements)

	usedRegisters := map[int]bool{0: true}
	for placement := range concurrentPlacements {
		if placement.Kind != InRegister || usedRegisters[placement.Register] {
			t.Fatalf("concurrent placements overlap or spill: register=%d, kind=%d", placement.Register, placement.Kind)
		}
		if placement.Register < 1 || placement.Register > 3 {
			t.Fatalf("concurrent placement used register %d, want one of 1,2,3", placement.Register)
		}
		usedRegisters[placement.Register] = true
	}

	second, err := allocator.Add(4, 0, 100)
	if err != nil || second != (Placement{Kind: Spilled, SpillSlot: 0}) {
		t.Fatalf("Add(4) = %+v, %v", second, err)
	}
	t.Logf("判定: 并发读与添加由锁串行化；最终新区间=%s，长区间=%s", describePlacement(second, nil), describePlacement(first, nil))
}

func TestRandomizedSequenceMatchesNaiveSimulation(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	const k = 3
	const addCount = 60

	allocator, err := New(k)
	if err != nil {
		t.Fatal(err)
	}
	simulator := newNaiveSimulator(k)

	start := 0
	for nextID := 0; nextID < addCount; nextID++ {
		op := operation{
			id:    nextID,
			start: start,
			end:   start + 1 + random.Intn(6),
		}
		expected := simulator.step(op)
		got, gotErr := allocator.Add(op.id, op.start, op.end)
		assertResult(t, nextID*2, op, expected, got, gotErr)

		if nextID > 0 {
			query := operation{id: random.Intn(nextID + 2), query: true}
			expectedQuery := simulator.step(query)
			got, gotErr := allocator.Query(query.id)
			assertResult(t, nextID*2+1, query, expectedQuery, got, gotErr)
		}

		start += random.Intn(3)
	}
}
