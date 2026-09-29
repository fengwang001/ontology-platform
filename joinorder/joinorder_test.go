package joinorder

import (
	"fmt"
	"math/big"
	"math/bits"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

type naiveCandidate struct {
	text string
	rows *big.Rat
	cost *big.Rat
}

type naiveResult struct {
	text       string
	rows       *big.Rat
	cost       *big.Rat
	partitions int64
}

func TestRandomInputsMatchNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))
	selector := NewSelector()

	for caseIndex := 0; caseIndex < 200; caseIndex++ {
		n := 1 + rng.Intn(6)
		tables := make([]Table, n)
		for i := range tables {
			tables[i] = Table{Name: fmt.Sprintf("t%d", i), Rows: int64(rng.Intn(9))}
		}

		predicates := make([]Predicate, 0)
		for left := 0; left < n; left++ {
			for right := left + 1; right < n; right++ {
				if rng.Intn(2) == 0 {
					predicateCount := 1 + rng.Intn(2)
					for range predicateCount {
						denominator := int64(2 + rng.Intn(5))
						numerator := int64(1 + rng.Intn(int(denominator)))
						predicates = append(predicates, Predicate{
							LeftTable:   tables[left].Name,
							RightTable:  tables[right].Name,
							Numerator:   numerator,
							Denominator: denominator,
						})
					}
				}
			}
		}

		got, err := selector.SelectPlan(tables, predicates)
		if err != nil {
			t.Fatalf("case %d unexpected error: %v", caseIndex, err)
		}
		want := naiveBestPlan(t, tables, predicates)
		t.Logf("随机对拍 case=%d 输入表=%v 输入谓词=%v 输出=%s 行数=%s 代价=%s 划分=%d 判定依据=穷举所有合法连接树后比较代价与规范文本",
			caseIndex, tables, predicates, got.Plan.Text, got.Plan.Rows.RatString(), got.Plan.Cost.RatString(), got.PartitionsExamined)

		if got.Plan.Text != want.text || got.Plan.Rows.Cmp(want.rows) != 0 || got.Plan.Cost.Cmp(want.cost) != 0 || got.PartitionsExamined != want.partitions {
			t.Fatalf("case %d got (%s, %s, %s, %d), want (%s, %s, %s, %d)",
				caseIndex,
				got.Plan.Text, got.Plan.Rows.RatString(), got.Plan.Cost.RatString(), got.PartitionsExamined,
				want.text, want.rows.RatString(), want.cost.RatString(), want.partitions)
		}
	}
}

func TestTieUsesCanonicalText(t *testing.T) {
	tables := []Table{{Name: "A", Rows: 1}, {Name: "B", Rows: 1}, {Name: "C", Rows: 1}}
	predicates := []Predicate{
		{LeftTable: "A", RightTable: "B", Numerator: 1, Denominator: 1},
		{LeftTable: "B", RightTable: "C", Numerator: 1, Denominator: 1},
		{LeftTable: "A", RightTable: "C", Numerator: 1, Denominator: 1},
	}
	got, err := NewSelector().SelectPlan(tables, predicates)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("并列判定 输入=%v 谓词=%v 输出=%s 代价=%s 判定依据=所有连接树代价相等，取规范文本字典序最小", tables, predicates, got.Plan.Text, got.Plan.Cost.RatString())
	if got.Plan.Text != "((A B) C)" {
		t.Fatalf("text = %q", got.Plan.Text)
	}
}

func TestDisconnectedComponentsUseCartesianProducts(t *testing.T) {
	tables := []Table{{Name: "A", Rows: 2}, {Name: "B", Rows: 3}, {Name: "C", Rows: 5}, {Name: "D", Rows: 7}}
	got, err := NewSelector().SelectPlan(tables, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("不连通图 输入=%v 输出=%s 行数=%s 代价=%s 判定依据=单表作为分量，仅在分量合并阶段使用笛卡尔积", tables, got.Plan.Text, got.Plan.Rows.RatString(), got.Plan.Cost.RatString())
	if got.Plan.Text != "((A D) (B C))" ||
		got.Plan.Rows.Cmp(big.NewRat(210, 1)) != 0 ||
		got.Plan.Cost.Cmp(big.NewRat(239, 1)) != 0 {
		t.Fatalf("got text=%s rows=%s cost=%s", got.Plan.Text, got.Plan.Rows.RatString(), got.Plan.Cost.RatString())
	}
}

func TestPartitionsExaminedUpperBound(t *testing.T) {
	for n := 1; n <= 12; n++ {
		tables := make([]Table, n)
		for i := range tables {
			tables[i] = Table{Name: fmt.Sprintf("t%02d", i), Rows: 1}
		}
		got, err := NewSelector().SelectPlan(tables, nil)
		if err != nil {
			t.Fatal(err)
		}
		bound := int64(1)
		for range n {
			bound *= 3
		}
		t.Logf("划分上界 n=%d 划分=%d 上界=%d 判定依据=每对不相交子集对应一次被考察划分，数量不超过 3^n", n, got.PartitionsExamined, bound)
		if got.PartitionsExamined > bound {
			t.Fatalf("n=%d partitions=%d exceeds %d", n, got.PartitionsExamined, bound)
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	tests := []struct {
		name       string
		tables     []Table
		predicates []Predicate
		reason     string
	}{
		{name: "zero tables", reason: ReasonEmptyTableSet},
		{name: "thirteen tables", tables: tablesNamed("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"), reason: ReasonTooManyTables},
		{name: "empty table name", tables: []Table{{Name: "", Rows: 1}}, reason: ReasonEmptyTableName},
		{name: "duplicate table", tables: []Table{{Name: "a", Rows: 1}, {Name: "a", Rows: 2}}, reason: ReasonDuplicateTableName},
		{name: "unknown predicate table", tables: tablesNamed("a"), predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 1}}, reason: ReasonUnknownTable},
		{name: "same table predicate", tables: tablesNamed("a"), predicates: []Predicate{{LeftTable: "a", RightTable: "a", Numerator: 1, Denominator: 1}}, reason: ReasonSameTablePredicate},
		{name: "zero numerator", tables: tablesNamed("a", "b"), predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 0, Denominator: 1}}, reason: ReasonInvalidSelectivity},
		{name: "zero denominator", tables: tablesNamed("a", "b"), predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 1, Denominator: 0}}, reason: ReasonInvalidSelectivity},
		{name: "selectivity above one", tables: tablesNamed("a", "b"), predicates: []Predicate{{LeftTable: "a", RightTable: "b", Numerator: 2, Denominator: 1}}, reason: ReasonInvalidSelectivity},
		{name: "negative rows", tables: []Table{{Name: "a", Rows: -1}}, reason: ReasonNegativeRowCount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSelector().SelectPlan(tt.tables, tt.predicates)
			t.Logf("非法输入 case=%s 表=%v 谓词=%v 错误=%v 判定依据=完整校验失败即整体拒绝，不产出部分计划", tt.name, tt.tables, tt.predicates, err)
			if err == nil {
				t.Fatal("expected rejection")
			}
			validationErr, ok := err.(ValidationError)
			if !ok || validationErr.Reason != tt.reason {
				t.Fatalf("error = %#v, want reason %s", err, tt.reason)
			}
		})
	}
}

func TestConcurrentAndInputOrderDeterminism(t *testing.T) {
	tables := []Table{{Name: "A", Rows: 4}, {Name: "B", Rows: 2}, {Name: "C", Rows: 3}, {Name: "D", Rows: 5}}
	predicates := []Predicate{
		{LeftTable: "C", RightTable: "A", Numerator: 1, Denominator: 2},
		{LeftTable: "B", RightTable: "D", Numerator: 2, Denominator: 3},
		{LeftTable: "A", RightTable: "B", Numerator: 1, Denominator: 1},
	}
	baseline, err := NewSelector().SelectPlan(tables, predicates)
	if err != nil {
		t.Fatal(err)
	}

	const workerCount = 32
	var wg sync.WaitGroup
	errs := make(chan error, workerCount)
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			shuffledTables := append([]Table(nil), tables...)
			shuffledPredicates := append([]Predicate(nil), predicates...)
			shuffleSlice(shuffledTables, int64(worker+1))
			shuffleSlice(shuffledPredicates, int64(worker+101))
			got, err := NewSelector().SelectPlan(shuffledTables, shuffledPredicates)
			if err != nil {
				errs <- err
				return
			}
			if got.Plan.Text != baseline.Plan.Text ||
				got.Plan.Rows.Cmp(baseline.Plan.Rows) != 0 ||
				got.Plan.Cost.Cmp(baseline.Plan.Cost) != 0 ||
				got.PartitionsExamined != baseline.PartitionsExamined {
				errs <- fmt.Errorf("worker %d got %#v, want %#v", worker, got, baseline)
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("并发与顺序判定 基准输出=%s 行数=%s 代价=%s 划分=%d 判定依据=%d 个 goroutine 使用不同输入顺序得到逐字段一致结果",
		baseline.Plan.Text, baseline.Plan.Rows.RatString(), baseline.Plan.Cost.RatString(), baseline.PartitionsExamined, workerCount)
}

func tablesNamed(names ...string) []Table {
	tables := make([]Table, len(names))
	for i, name := range names {
		tables[i] = Table{Name: name, Rows: 1}
	}
	return tables
}

func shuffleSlice[T any](values []T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(values), func(i, j int) {
		values[i], values[j] = values[j], values[i]
	})
}

func naiveBestPlan(t *testing.T, tables []Table, predicates []Predicate) naiveResult {
	t.Helper()
	n := len(tables)
	sortedTables := append([]Table(nil), tables...)
	sort.Slice(sortedTables, func(i, j int) bool { return sortedTables[i].Name < sortedTables[j].Name })

	indexByName := make(map[string]int, n)
	names := make([]string, n)
	rows := make([]*big.Rat, n)
	for i, table := range sortedTables {
		indexByName[table.Name] = i
		names[i] = table.Name
		rows[i] = big.NewRat(table.Rows, 1)
	}

	selectivity := map[uint64]*big.Rat{}
	adjacency := make([][]int, n)
	for _, predicate := range predicates {
		left := indexByName[predicate.LeftTable]
		right := indexByName[predicate.RightTable]
		if left > right {
			left, right = right, left
		}
		key := edgeKey(left, right)
		factor := big.NewRat(predicate.Numerator, predicate.Denominator)
		if current, ok := selectivity[key]; ok {
			selectivity[key] = new(big.Rat).Mul(current, factor)
		} else {
			selectivity[key] = factor
		}
		adjacency[left] = append(adjacency[left], right)
		adjacency[right] = append(adjacency[right], left)
	}

	components := connectedComponents(adjacency)
	sort.Slice(components, func(i, j int) bool { return components[i] < components[j] })
	dp := map[uint][]*naiveCandidate{}
	var partitions int64

	naiveCrossFactor := func(leftMask, rightMask uint) *big.Rat {
		product := big.NewRat(1, 1)
		for left := 0; left < n; left++ {
			if leftMask&(1<<uint(left)) == 0 {
				continue
			}
			for right := 0; right < n; right++ {
				if rightMask&(1<<uint(right)) == 0 {
					continue
				}
				if factor, ok := selectivity[edgeKey(left, right)]; ok {
					product.Mul(product, factor)
				}
			}
		}
		return product
	}

	naiveJoin := func(left, right *naiveCandidate, leftMask, rightMask uint, cartesian bool) *naiveCandidate {
		resultRows := new(big.Rat).Mul(left.rows, right.rows)
		if !cartesian {
			resultRows.Mul(resultRows, naiveCrossFactor(leftMask, rightMask))
		}
		resultCost := new(big.Rat).Add(left.cost, right.cost)
		resultCost.Add(resultCost, resultRows)
		leftText, rightText := left.text, right.text
		if leftText > rightText {
			leftText, rightText = rightText, leftText
		}
		return &naiveCandidate{
			text: "(" + leftText + " " + rightText + ")",
			rows: resultRows,
			cost: resultCost,
		}
	}

	var enumerateComponent func(uint) []*naiveCandidate
	enumerateComponent = func(mask uint) []*naiveCandidate {
		if cached, ok := dp[mask]; ok {
			return cached
		}
		if mask&(mask-1) == 0 {
			index := bits.TrailingZeros32(uint32(mask))
			dp[mask] = []*naiveCandidate{{text: names[index], rows: new(big.Rat).Set(rows[index]), cost: big.NewRat(0, 1)}}
			return dp[mask]
		}
		if !isConnectedInducedSubset(adjacency, mask) {
			return nil
		}

		lowest := mask & (^mask + 1)
		remaining := mask ^ lowest
		rightMask := remaining
		for rightMask != 0 {
			partitions++
			leftMask := mask ^ rightMask
			leftPlans := enumerateComponent(leftMask)
			rightPlans := enumerateComponent(rightMask)
			cross := false
			for leftIndex := 0; leftIndex < n; leftIndex++ {
				if leftMask&(1<<uint(leftIndex)) == 0 {
					continue
				}
				for _, rightIndex := range adjacency[leftIndex] {
					if rightMask&(1<<uint(rightIndex)) != 0 {
						cross = true
					}
				}
			}
			if cross {
				for _, left := range leftPlans {
					for _, right := range rightPlans {
						dp[mask] = append(dp[mask], naiveJoin(left, right, leftMask, rightMask, false))
					}
				}
			}
			rightMask = (rightMask - 1) & remaining
		}
		return dp[mask]
	}

	componentRoots := make([][]*naiveCandidate, len(components))
	for i, component := range components {
		componentRoots[i] = enumerateComponent(component)
	}

	componentCount := len(components)
	componentDP := map[uint][]*naiveCandidate{}
	for i, roots := range componentRoots {
		componentDP[1<<uint(i)] = roots
	}
	for componentMask := uint(1); componentMask < 1<<uint(componentCount); componentMask++ {
		if componentMask&(componentMask-1) == 0 {
			continue
		}
		base := componentMask & (^componentMask + 1)
		remaining := componentMask ^ base
		rightMask := remaining
		for rightMask != 0 {
			partitions++
			leftMask := componentMask ^ rightMask
			for _, left := range componentDP[leftMask] {
				for _, right := range componentDP[rightMask] {
					componentDP[componentMask] = append(componentDP[componentMask], naiveJoin(left, right, 0, 0, true))
				}
			}
			rightMask = (rightMask - 1) & remaining
		}
	}

	fullMask := uint((1 << uint(componentCount)) - 1)
	candidates := componentDP[fullMask]
	if len(candidates) == 0 {
		t.Fatal("naive enumeration produced no plan")
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.cost.Cmp(best.cost) < 0 ||
			(candidate.cost.Cmp(best.cost) == 0 && candidate.text < best.text) {
			best = candidate
		}
	}
	return naiveResult{
		text:       best.text,
		rows:       best.rows,
		cost:       best.cost,
		partitions: partitions,
	}
}

var _ = fmt.Sprintf
var _ = big.NewRat
var _ = bits.TrailingZeros32
var _ = sort.Strings
var _ sync.WaitGroup
