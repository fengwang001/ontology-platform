package aggregate

import (
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
)

func makeRows(n int) []map[string]any {
	// Deliberately awkward magnitudes so a naive left-fold would depend on
	// arrival order; the canonical-sort reduction must not.
	values := []float64{
		1e16, 1.0, -1e16, 0.1, 0.2, -0.3, 12345.6789,
		-98765.4321, 3.0, -2.0, 1e-12, -1e-12, 100.5,
		0.5, -0.25, 0.125, 7.75, -3.5, 2.25,
	}
	groups := []string{"a", "b", "c"}
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{
			"g": groups[i%len(groups)],
			"v": values[i%len(values)],
		}
	}
	return rows
}

func groupBits(rows []map[string]any) (map[string]uint64, map[string]float64) {
	agg := NewAggregator([]string{"g"}, "v")
	for _, r := range rows {
		agg.Add(r)
	}
	res, err := agg.Snapshot()
	if err != nil {
		panic(err)
	}
	bits := map[string]uint64{}
	vals := map[string]float64{}
	for _, r := range res {
		name := r.Key.Columns[0].Value.(string)
		bits[name] = math.Float64bits(r.FloatSum)
		vals[name] = r.FloatSum
	}
	return bits, vals
}

func TestSumBitPatternOrderIndependent(t *testing.T) {
	base := makeRows(120)
	refBits, _ := groupBits(base)

	rng := rand.New(rand.NewPCG(1, 2))
	for round := 0; round < 20; round++ {
		shuffled := append([]map[string]any(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		bits, _ := groupBits(shuffled)
		if !reflect.DeepEqual(bits, refBits) {
			t.Fatalf("round %d: bit patterns differ: %v vs %v", round, bits, refBits)
		}
	}
}

// kahanSum is a compensated summation used only as a high-accuracy
// reference for the absolute-error bound below.
func kahanSum(xs []float64) float64 {
	var sum, comp float64
	for _, v := range xs {
		y := v - comp
		t := sum + y
		comp = (t - sum) - y
		sum = t
	}
	return sum
}

func TestSumNearMathematicalTotal(t *testing.T) {
	// Moderate, exactly-ish representable values per group so that any
	// reasonable IEEE 754 reduction stays within a tight absolute error of
	// the true total.
	data := map[string][]float64{
		"a": {0.5, -0.25, 0.125, 7.75, -3.5, 2.25, 1.0, -1.0},
		"b": {100.125, -0.625, 3.5, 4.25, -7.25, 0.0},
	}
	rng := rand.New(rand.NewPCG(42, 99))
	for round := 0; round < 10; round++ {
		agg := NewAggregator([]string{"g"}, "v")
		var ordered []map[string]any
		for g, vs := range data {
			for _, v := range vs {
				ordered = append(ordered, map[string]any{"g": g, "v": v})
			}
		}
		rng.Shuffle(len(ordered), func(i, j int) {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		})
		for _, r := range ordered {
			agg.Add(r)
		}
		res, _ := agg.Snapshot()
		for _, r := range res {
			name := r.Key.Columns[0].Value.(string)
			want := kahanSum(data[name])
			if math.Abs(r.FloatSum-want) > 1e-9 {
				t.Fatalf("round %d group %s: |%v - %v| exceeds 1e-9",
					round, name, r.FloatSum, want)
			}
		}
	}
}

func TestOutputStableAndMapIterationIndependent(t *testing.T) {
	rows := []map[string]any{
		{"g": "c", "v": 1.0},
		{"g": "a", "v": 1.0},
		{"g": "b", "v": 1.0},
	}
	var prev []Result
	for i := 0; i < 5; i++ {
		agg := NewAggregator([]string{"g"}, "v")
		for _, r := range rows {
			agg.Add(r)
		}
		cur, _ := agg.Snapshot()
		if prev != nil && !reflect.DeepEqual(cur, prev) {
			t.Fatalf("snapshot output not reproducible")
		}
		prev = cur
	}
	order := []string{}
	for _, r := range prev {
		order = append(order, r.Key.Columns[0].Value.(string))
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("want lexicographic %v, got %v", want, order)
	}
}

func TestMissingGroupsSortingRule(t *testing.T) {
	// Single column: present before "" before nil before absent.
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": nil, "v": 1})
	agg.Add(map[string]any{"v": 1})
	agg.Add(map[string]any{"k": "", "v": 1})
	agg.Add(map[string]any{"k": "z", "v": 1})
	res, _ := agg.Snapshot()
	got := make([]MissingKind, len(res))
	for i, r := range res {
		got[i] = r.Key.Columns[0].Kind
	}
	want := []MissingKind{Present, EmptyString, Null, Absent}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missing sort rule violated: %v", got)
	}
}

func TestSnapshotIsIndependentCopy(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "a", "v": 1.0})
	res1, _ := agg.Snapshot()
	agg.Add(map[string]any{"g": "a", "v": 2.0})
	agg.Add(map[string]any{"g": "b", "v": 9.0})
	res1[0].Key.Columns[0].Value = "mutated"
	res2, _ := agg.Snapshot()
	if res2[0].Key.Columns[0].Value != "a" || res2[0].Count != 2 {
		t.Fatalf("snapshot not isolated from later Adds/mutation: %+v", res2[0])
	}
	if len(res2) != 2 {
		t.Fatalf("want 2 groups, got %d", len(res2))
	}
}
