package ontology

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestFrameMatchesNaiveImplementation(t *testing.T) {
	random := rand.New(rand.NewSource(20261001))
	offsets := []int64{0, 1, 2, 3, 5, 7, 1 << 32, 1 << 62, 1<<62 + 1, 1<<63 - 1}

	for caseNumber := 0; caseNumber < 2000; caseNumber++ {
		keys := randomSortedKeys(random)
		groups := naiveGroups(keys)
		spec := randomValidSpec(random, offsets)
		i := random.Intn(len(keys))

		calculator := mustCalculator(t, len(keys)+1)
		for _, key := range keys {
			mustAppend(t, calculator, key)
		}
		got, err := calculator.Frame(i, spec)
		if err != nil {
			t.Fatalf("case %d: Frame returned %v; spec=%+v keys=%v", caseNumber, err, spec, keys)
		}
		want, basis := naiveFrame(keys, groups, i, spec)
		t.Logf("case=%d keys=%v i=%d spec={mode:%s start:%s end:%s exclude:%s} basis=%q output=%v naive=%v",
			caseNumber, keys, i, modeName(spec.Mode), boundName(spec.Start), boundName(spec.End),
			exclusionName(spec.Exclude), basis, got, want)

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d mismatch: got=%v want=%v keys=%v i=%d spec=%+v",
				caseNumber, got, want, keys, i, spec)
		}
	}
}

func randomSortedKeys(random *rand.Rand) []int64 {
	length := 1 + random.Intn(15)

	keys := make([]int64, length)
	base := int64(random.Intn(1<<30)) - 1<<29
	if random.Intn(4) == 0 {
		base = -1 << 62
	} else if random.Intn(4) == 0 {
		base = 1 << 62
	}
	keys[0] = base

	for row := 1; row < length; row++ {
		next := keys[row-1]
		if random.Intn(3) != 0 {
			deltaChoices := []int64{0, 1, 2, int64(random.Intn(9)), 1 << 20, 1 << 62}
			delta := deltaChoices[random.Intn(len(deltaChoices))]
			if keys[row-1] <= 1<<63-1-delta {
				next = keys[row-1] + delta
			}
		}
		keys[row] = next
	}
	return keys
}

func randomValidSpec(random *rand.Rand, offsets []int64) FrameSpec {
	mode := Mode(random.Intn(3))
	startType := BoundType(random.Intn(int(UnboundedFollowing) + 1))
	endType := BoundType(random.Intn(int(UnboundedFollowing) + 1))
	for startType == UnboundedFollowing ||
		endType == UnboundedPreceding ||
		startType > endType {
		startType = BoundType(random.Intn(int(UnboundedFollowing) + 1))
		endType = BoundType(random.Intn(int(UnboundedFollowing) + 1))
	}

	return FrameSpec{
		Mode:    mode,
		Start:   randomBound(random, startType, offsets),
		End:     randomBound(random, endType, offsets),
		Exclude: Exclusion(random.Intn(4)),
	}
}

func randomBound(random *rand.Rand, boundType BoundType, offsets []int64) Bound {
	bound := Bound{Type: boundType}
	if boundType == Preceding || boundType == Following {
		bound.N = offsets[random.Intn(len(offsets))]
	}
	return bound
}

func naiveGroups(keys []int64) []int64 {
	groups := make([]int64, len(keys))
	for row := 1; row < len(keys); row++ {
		groups[row] = groups[row-1]
		if keys[row] != keys[row-1] {
			groups[row]++
		}
	}
	return groups
}

func naiveFrame(keys []int64, groups []int64, i int, spec FrameSpec) ([]Interval, string) {
	if len(keys) == 0 {
		return nil, ""
	}

	included := make([]bool, len(keys))
	var decisions []string
	for row := range keys {
		d := naiveMetricDifference(keys, groups, spec.Mode, i, row)
		startOK := naiveStartAllows(d, spec.Start)
		endOK := naiveEndAllows(d, spec.End)
		excluded := naiveExcluded(groups, i, row, spec.Exclude)
		included[row] = startOK && endOK && !excluded
		decisions = append(decisions, fmt.Sprintf("j%d:d%s:st%t:en%t:ex%t", row, d.String(), startOK, endOK, excluded))
	}
	return mergeIncludedRows(included), strings.Join(decisions, ";")
}

func naiveMetricDifference(keys []int64, groups []int64, mode Mode, baseRow int, row int) *big.Int {
	switch mode {
	case Rows:
		return big.NewInt(int64(row - baseRow))
	case Groups:
		return new(big.Int).Sub(big.NewInt(groups[row]), big.NewInt(groups[baseRow]))
	default:
		return new(big.Int).Sub(big.NewInt(keys[row]), big.NewInt(keys[baseRow]))
	}
}

func naiveStartAllows(d *big.Int, bound Bound) bool {
	switch bound.Type {
	case UnboundedPreceding:
		return true
	case Preceding:
		return d.Cmp(new(big.Int).Neg(big.NewInt(bound.N))) >= 0
	case CurrentRowBound:
		return d.Sign() >= 0
	case Following:
		return d.Cmp(big.NewInt(bound.N)) >= 0
	default:
		return false
	}
}

func naiveEndAllows(d *big.Int, bound Bound) bool {
	switch bound.Type {
	case Preceding:
		return d.Cmp(new(big.Int).Neg(big.NewInt(bound.N))) <= 0
	case CurrentRowBound:
		return d.Sign() <= 0
	case Following:
		return d.Cmp(big.NewInt(bound.N)) <= 0
	default:
		return true
	}
}

func naiveExcluded(groups []int64, i int, row int, exclusion Exclusion) bool {
	switch exclusion {
	case ExcludeCurrentRow:
		return row == i
	case ExcludeGroup:
		return groups[row] == groups[i]
	case ExcludeTies:
		return groups[row] == groups[i] && row != i
	default:
		return false
	}
}

func mergeIncludedRows(included []bool) []Interval {
	var intervals []Interval
	for row, ok := range included {
		if !ok {
			continue
		}
		if len(intervals) > 0 && intervals[len(intervals)-1].End == row {
			intervals[len(intervals)-1].End = row + 1
		} else {
			intervals = append(intervals, Interval{Start: row, End: row + 1})
		}
	}
	return intervals
}

func modeName(mode Mode) string {
	switch mode {
	case Rows:
		return "ROWS"
	case Range:
		return "RANGE"
	default:
		return "GROUPS"
	}
}

func boundName(bound Bound) string {
	switch bound.Type {
	case UnboundedPreceding:
		return "UNBOUNDED PRECEDING"
	case Preceding:
		return fmt.Sprintf("%d PRECEDING", bound.N)
	case CurrentRowBound:
		return "CURRENT ROW"
	case Following:
		return fmt.Sprintf("%d FOLLOWING", bound.N)
	default:
		return "UNBOUNDED FOLLOWING"
	}
}

func exclusionName(exclusion Exclusion) string {
	switch exclusion {
	case ExcludeCurrentRow:
		return "CURRENT ROW"
	case ExcludeGroup:
		return "GROUP"
	case ExcludeTies:
		return "TIES"
	default:
		return "NONE"
	}
}
