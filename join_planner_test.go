package ontology

import (
	"errors"
	"math/big"
	"math/rand"
	"sync"
	"testing"
)

func TestSingleTable(t *testing.T) {
	query := Query{Tables: []Table{{Name: "users", RowCount: 10}}}

	plan, err := ChoosePlan(query)
	if err != nil {
		t.Fatalf("ChoosePlan returned error: %v", err)
	}
	t.Logf("input=%#v output=%#v reason=single-table plan has zero join cost", query, plan)
	if plan.Text != "users" {
		t.Fatalf("Text = %q, want %q", plan.Text, "users")
	}
	if plan.Cost.Sign() != 0 {
		t.Fatalf("Cost = %s, want 0", plan.Cost.RatString())
	}
	if plan.Partitions != 0 {
		t.Fatalf("Partitions = %d, want 0", plan.Partitions)
	}
}

func TestTieBreaksByCanonicalText(t *testing.T) {
	query := Query{
		Tables: []Table{
			{Name: "a", RowCount: 10},
			{Name: "b", RowCount: 10},
			{Name: "c", RowCount: 10},
		},
		Predicates: []Predicate{
			{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 2},
			{LeftTable: "a", RightTable: "c", Numerator: 1, Denominator: 2},
			{LeftTable: "b", RightTable: "c", Numerator: 1, Denominator: 2},
		},
	}

	plan, err := ChoosePlan(query)
	if err != nil {
		t.Fatalf("ChoosePlan returned error: %v", err)
	}
	t.Logf("input=%#v output=%#v reason=all join orders cost 175, lexicographically smallest canonical text wins", query, plan)
	if plan.Text != "((a b) c)" {
		t.Fatalf("Text = %q, want %q", plan.Text, "((a b) c)")
	}
	if plan.Cost.Cmp(big.NewRat(175, 1)) != 0 {
		t.Fatalf("Cost = %s, want 175", plan.Cost.RatString())
	}
}

func TestDisconnectedComponentsJoinInternallyThenCartesianMerge(t *testing.T) {
	query := Query{
		Tables: []Table{
			{Name: "a", RowCount: 10},
			{Name: "b", RowCount: 10},
			{Name: "c", RowCount: 10},
			{Name: "d", RowCount: 10},
		},
		Predicates: []Predicate{
			{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 2},
			{LeftTable: "c", RightTable: "d", Numerator: 1, Denominator: 2},
		},
	}

	plan, err := ChoosePlan(query)
	if err != nil {
		t.Fatalf("ChoosePlan returned error: %v", err)
	}
	t.Logf("input=%#v output=%#v reason=both components finish their 50-row joins before the 2500-row Cartesian merge", query, plan)
	if plan.Text != "((a b) (c d))" {
		t.Fatalf("Text = %q, want %q", plan.Text, "((a b) (c d))")
	}
	if plan.OutputRows.Cmp(big.NewRat(2500, 1)) != 0 {
		t.Fatalf("OutputRows = %s, want 2500", plan.OutputRows.RatString())
	}
	if plan.Cost.Cmp(big.NewRat(2600, 1)) != 0 {
		t.Fatalf("Cost = %s, want 2600", plan.Cost.RatString())
	}
}

func TestMultipleCrossPredicatesMultiplyExactly(t *testing.T) {
	query := Query{
		Tables: []Table{
			{Name: "a", RowCount: 6},
			{Name: "b", RowCount: 10},
		},
		Predicates: []Predicate{
			{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 2},
			{LeftTable: "b", RightTable: "a", Numerator: 2, Denominator: 3},
		},
	}

	plan, err := ChoosePlan(query)
	if err != nil {
		t.Fatalf("ChoosePlan returned error: %v", err)
	}
	t.Logf("input=%#v output=%#v reason=6*10*(1/2)*(2/3)=20 exact rational rows and cost", query, plan)
	if plan.OutputRows.Cmp(big.NewRat(20, 1)) != 0 {
		t.Fatalf("OutputRows = %s, want 20", plan.OutputRows.RatString())
	}
	if plan.Cost.Cmp(big.NewRat(20, 1)) != 0 {
		t.Fatalf("Cost = %s, want 20", plan.Cost.RatString())
	}
}

func TestOrderIndependentAndSafeForConcurrentUse(t *testing.T) {
	query := Query{
		Tables: []Table{
			{Name: "d", RowCount: 4},
			{Name: "a", RowCount: 1},
			{Name: "c", RowCount: 3},
			{Name: "b", RowCount: 2},
		},
		Predicates: []Predicate{
			{LeftTable: "d", RightTable: "c", Numerator: 1, Denominator: 2},
			{LeftTable: "b", RightTable: "a", Numerator: 1, Denominator: 2},
			{LeftTable: "a", RightTable: "d", Numerator: 1, Denominator: 3},
		},
	}

	const goroutineCount = 32
	plans := make([]Plan, goroutineCount)
	var waitGroup sync.WaitGroup
	for i := range plans {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			plan, err := ChoosePlan(query)
			if err != nil {
				t.Errorf("goroutine %d returned error: %v", index, err)
				return
			}
			plans[index] = plan
		}(i)
	}
	waitGroup.Wait()

	reference := plans[0]
	for i, plan := range plans[1:] {
		if plan.Text != reference.Text ||
			plan.Cost.Cmp(reference.Cost) != 0 ||
			plan.OutputRows.Cmp(reference.OutputRows) != 0 ||
			plan.Partitions != reference.Partitions {
			t.Fatalf("concurrent plan %d = %#v, want %#v", i+1, plan, reference)
		}
	}

	shuffledText := reference.Text
	orderedTables := []Table{
		{Name: "a", RowCount: 1},
		{Name: "b", RowCount: 2},
		{Name: "c", RowCount: 3},
		{Name: "d", RowCount: 4},
	}
	orderedPlan, err := ChoosePlan(Query{
		Tables: orderedTables,
		Predicates: []Predicate{
			{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 2},
			{LeftTable: "a", RightTable: "d", Numerator: 1, Denominator: 3},
			{LeftTable: "c", RightTable: "d", Numerator: 1, Denominator: 2},
		},
	})
	if err != nil {
		t.Fatalf("ordered ChoosePlan returned error: %v", err)
	}
	t.Logf("input=%#v output=%s reason=shuffled tables and predicates reproduce the ordered plan byte-for-byte", query, shuffledText)
	if orderedPlan.Text != shuffledText || orderedPlan.Cost.Cmp(reference.Cost) != 0 {
		t.Fatalf("ordered plan = %#v, want shuffled plan %#v", orderedPlan, reference)
	}
}

func TestPartitionCountBoundedByThreeToTableCount(t *testing.T) {
	cases := []struct {
		name       string
		isolated   bool
		exactCount int64
	}{
		{name: "connected-chain", isolated: false, exactCount: -1},
		{name: "all-isolated", isolated: true, exactCount: 261625},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tables := make([]Table, 12)
			var predicates []Predicate
			for i := range tables {
				tables[i] = Table{Name: string(rune('a' + i)), RowCount: int64(i + 1)}
				if !testCase.isolated && i > 0 {
					predicates = append(predicates, Predicate{
						LeftTable:   tables[i-1].Name,
						RightTable:  tables[i].Name,
						Numerator:   1,
						Denominator: 2,
					})
				}
			}

			plan, err := ChoosePlan(Query{Tables: tables, Predicates: predicates})
			if err != nil {
				t.Fatalf("ChoosePlan returned error: %v", err)
			}
			bound := int64(531441)
			t.Logf("input-table-count=%d predicate-count=%d output-partitions=%d reason=count is at most 3^n=%d", len(tables), len(predicates), plan.Partitions, bound)
			if plan.Partitions < 0 || plan.Partitions > bound {
				t.Fatalf("Partitions = %d, want range [0,%d]", plan.Partitions, bound)
			}
			if testCase.exactCount >= 0 && plan.Partitions != testCase.exactCount {
				t.Fatalf("Partitions = %d, want exact component-union count %d", plan.Partitions, testCase.exactCount)
			}
		})
	}
}

func TestInvalidInputReasons(t *testing.T) {
	validTables := []Table{
		{Name: "a", RowCount: 1},
		{Name: "b", RowCount: 1},
	}
	tooManyTables := make([]Table, 13)
	for i := range tooManyTables {
		tooManyTables[i] = Table{Name: string(rune('a' + i)), RowCount: 1}
	}

	testCases := []struct {
		name  string
		query Query
		want  error
	}{
		{name: "empty query", query: Query{}, want: ErrEmptyQuery},
		{name: "too many tables", query: Query{Tables: tooManyTables}, want: ErrTooManyTables},
		{name: "empty table name", query: Query{Tables: []Table{{Name: "", RowCount: 1}, {Name: "b", RowCount: 1}}}, want: ErrEmptyTableName},
		{name: "duplicate table", query: Query{Tables: []Table{{Name: "a", RowCount: 1}, {Name: "a", RowCount: 2}}}, want: ErrDuplicateTableName},
		{
			name:  "unknown predicate",
			query: Query{Tables: validTables, Predicates: []Predicate{{LeftTable: "a", RightTable: "z", Numerator: 1, Denominator: 1}}},
			want:  ErrUnknownPredicate,
		},
		{
			name:  "self predicate",
			query: Query{Tables: validTables, Predicates: []Predicate{{LeftTable: "a", RightTable: "a", Numerator: 1, Denominator: 1}}},
			want:  ErrSelfPredicate,
		},
		{
			name:  "selectivity above one",
			query: Query{Tables: validTables, Predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 2, Denominator: 1}}},
			want:  ErrInvalidSelectivity,
		},
		{
			name:  "zero selectivity numerator",
			query: Query{Tables: validTables, Predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 0, Denominator: 1}}},
			want:  ErrInvalidSelectivity,
		},
		{
			name:  "negative selectivity denominator",
			query: Query{Tables: validTables, Predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: -2}}},
			want:  ErrInvalidSelectivity,
		},
		{
			name:  "negative row count",
			query: Query{Tables: []Table{{Name: "a", RowCount: -1}, {Name: "b", RowCount: 1}}},
			want:  ErrNegativeRowCount,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := ChoosePlan(testCase.query)
			t.Logf("input=%#v output=%#v error=%v reason=whole query rejected before any partial plan is returned", testCase.query, plan, err)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
			if plan != (Plan{}) {
				t.Fatalf("plan = %#v, want zero value", plan)
			}
		})
	}
}

func TestMatchesNaiveExhaustivePlansUpToSixTables(t *testing.T) {
	random := rand.New(rand.NewSource(902))
	for iteration := 0; iteration < 200; iteration++ {
		tableCount := random.Intn(6) + 1
		tables := make([]Table, tableCount)
		for i := range tables {
			tables[i] = Table{
				Name:     string(rune('a' + i)),
				RowCount: int64(random.Intn(21)),
			}
		}

		var predicates []Predicate
		for left := 0; left < tableCount; left++ {
			for right := left + 1; right < tableCount; right++ {
				if random.Intn(2) == 0 {
					denominator := int64(random.Intn(5) + 1)
					numerator := int64(random.Intn(int(denominator)) + 1)
					predicates = append(predicates, Predicate{
						LeftTable:   tables[left].Name,
						RightTable:  tables[right].Name,
						Numerator:   numerator,
						Denominator: denominator,
					})
				}
			}
		}

		query := Query{Tables: tables, Predicates: predicates}
		plan, err := ChoosePlan(query)
		if err != nil {
			t.Fatalf("iteration %d: ChoosePlan returned error: %v", iteration, err)
		}

		normalized, err := normalizeQuery(query)
		if err != nil {
			t.Fatalf("iteration %d: normalizeQuery returned error: %v", iteration, err)
		}
		naive := naiveBestPlan(normalized)
		t.Logf(
			"iteration=%d input=%#v output=%s/%s reason=compared against every valid full binary join tree; naive=%s/%s",
			iteration,
			query,
			plan.Cost.RatString(),
			plan.Text,
			naive.cost.RatString(),
			naive.text,
		)

		if plan.Cost.Cmp(naive.cost) != 0 {
			t.Fatalf("iteration %d: Cost = %s, want %s", iteration, plan.Cost.RatString(), naive.cost.RatString())
		}
		if plan.OutputRows.Cmp(naive.rows) != 0 {
			t.Fatalf("iteration %d: OutputRows = %s, want %s", iteration, plan.OutputRows.RatString(), naive.rows.RatString())
		}
		if plan.Text != naive.text {
			t.Fatalf("iteration %d: Text = %q, want %q", iteration, plan.Text, naive.text)
		}
	}
}

func naiveBestPlan(query normalizedQuery) candidatePlan {
	all := naiveAllPlans(query, uint16(1<<len(query.names))-1)
	best := all[0]
	for _, candidate := range all[1:] {
		if better(&candidate, &best) {
			best = candidate
		}
	}
	return best
}

func naiveAllPlans(query normalizedQuery, mask uint16) []candidatePlan {
	if mask&(mask-1) == 0 {
		index := bitIndex(mask)
		return []candidatePlan{{
			text: query.names[index],
			rows: new(big.Rat).SetInt64(query.rowCounts[index]),
			cost: new(big.Rat),
		}}
	}

	anchor := uint16(mask & -mask)
	remaining := mask ^ anchor
	var plans []candidatePlan
	for rightMask := remaining; rightMask != 0; rightMask = (rightMask - 1) & remaining {
		leftMask := mask ^ rightMask
		if componentMask(query, mask) == mask {
			if componentMask(query, leftMask) != leftMask || componentMask(query, rightMask) != rightMask {
				continue
			}
		} else if hasCrossPredicate(query, leftMask, rightMask) {
			continue
		}

		for _, left := range naiveAllPlans(query, leftMask) {
			for _, right := range naiveAllPlans(query, rightMask) {
				rows := joinRows(query, leftMask, rightMask, left, right)
				cost := new(big.Rat).Add(left.cost, right.cost)
				cost.Add(cost, rows)
				plans = append(plans, candidatePlan{
					text: canonicalJoin(left.text, right.text),
					rows: rows,
					cost: cost,
				})
			}
		}
	}
	return plans
}

func componentMask(query normalizedQuery, mask uint16) uint16 {
	anchor := uint16(mask & -mask)
	seen := anchor
	frontier := anchor
	for frontier != 0 {
		bit := uint16(frontier & -frontier)
		frontier ^= bit
		for index := range query.names {
			nextBit := uint16(1 << index)
			if mask&nextBit == 0 || seen&nextBit != 0 {
				continue
			}
			low := bitIndex(bit)
			high := index
			if low > high {
				low, high = high, low
			}
			if query.selectivities[low][high] != nil {
				seen |= nextBit
				frontier |= nextBit
			}
		}
	}
	return seen
}

func hasCrossPredicate(query normalizedQuery, leftMask uint16, rightMask uint16) bool {
	for leftBits := leftMask; leftBits != 0; leftBits &= leftBits - 1 {
		leftIndex := bitIndex(uint16(leftBits & -leftBits))
		for rightBits := rightMask; rightBits != 0; rightBits &= rightBits - 1 {
			rightIndex := bitIndex(uint16(rightBits & -rightBits))
			low, high := leftIndex, rightIndex
			if low > high {
				low, high = high, low
			}
			if query.selectivities[low][high] != nil {
				return true
			}
		}
	}
	return false
}
