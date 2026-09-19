package ontology

import "testing"

// leaf builds a comparison leaf whose evaluation yields a fixed tri:
// t-* properties hold int64(1) (Eq 1 -> true), f-* hold int64(1)
// (Eq 2 -> false), u-* are absent (unknown).
func leaf(tag string) *Compare {
	switch tag {
	case "t":
		return &Compare{Property: "t", Op: Eq, Literal: int64(1)}
	case "f":
		return &Compare{Property: "f", Op: Eq, Literal: int64(2)}
	default:
		return &Compare{Property: "u", Op: Eq, Literal: int64(1)}
	}
}

var triFixture = map[string]any{"t": int64(1), "f": int64(1)}

var kleeneAnd = map[[2]string]struct {
	out    Tri
	leaves int
}{
	{"t", "t"}: {True, 2},
	{"t", "f"}: {False, 2},
	{"t", "u"}: {Unknown, 2},
	{"f", "t"}: {False, 1}, // definite False short-circuits
	{"f", "f"}: {False, 1},
	{"f", "u"}: {False, 1},
	{"u", "t"}: {Unknown, 2}, // Unknown never short-circuits
	{"u", "f"}: {False, 2},
	{"u", "u"}: {Unknown, 2},
}

var kleeneOr = map[[2]string]struct {
	out    Tri
	leaves int
}{
	{"t", "t"}: {True, 1}, // definite True short-circuits
	{"t", "f"}: {True, 1},
	{"t", "u"}: {True, 1},
	{"f", "t"}: {True, 2},
	{"f", "f"}: {False, 2},
	{"f", "u"}: {Unknown, 2},
	{"u", "t"}: {True, 2}, // Unknown never short-circuits
	{"u", "f"}: {Unknown, 2},
	{"u", "u"}: {Unknown, 2},
}

func TestKleeneTruthTable(t *testing.T) {
	ev := NewEvaluator(64)
	tags := []string{"t", "f", "u"}

	for _, left := range tags {
		for _, right := range tags {
			key := [2]string{left, right}

			want := kleeneAnd[key]
			got, err := ev.Eval(triFixture, &And{Children: []Predicate{leaf(left), leaf(right)}})
			if err != nil || got.Value != want.out || got.Leaves != want.leaves {
				t.Fatalf("And(%s,%s) = (%s,%d,%v), want (%s,%d)",
					left, right, got.Value, got.Leaves, err, want.out, want.leaves)
			}

			want = kleeneOr[key]
			got, err = ev.Eval(triFixture, &Or{Children: []Predicate{leaf(left), leaf(right)}})
			if err != nil || got.Value != want.out || got.Leaves != want.leaves {
				t.Fatalf("Or(%s,%s) = (%s,%d,%v), want (%s,%d)",
					left, right, got.Value, got.Leaves, err, want.out, want.leaves)
			}
		}
	}
}

func TestNotTruthTable(t *testing.T) {
	ev := NewEvaluator(64)
	cases := map[string]Tri{"t": False, "f": True, "u": Unknown}
	for tag, want := range cases {
		got, err := ev.Eval(triFixture, &Not{Child: leaf(tag)})
		if err != nil || got.Value != want || got.Leaves != 1 {
			t.Fatalf("Not(%s) = (%s,%d,%v), want (%s,1)",
				tag, got.Value, got.Leaves, err, want)
		}
	}
}
