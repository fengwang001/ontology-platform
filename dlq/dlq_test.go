package dlq

import (
	"math"
	"strconv"
	"testing"
)

func TestFiringOrder(t *testing.T) {
	cases := []struct {
		name  string
		tasks []struct {
			id string
			t  int64
		}
		now  int64
		want []string
	}{
		{
			"ties broken by registration seq",
			[]struct {
				id string
				t  int64
			}{{"a", 5}, {"c", 5}, {"b", 5}},
			10, []string{"a", "c", "b"},
		},
		{
			"mixed fireAt, eight-step subset",
			[]struct {
				id string
				t  int64
			}{{"a", 5}, {"b", 3}, {"c", 5}, {"d", 2}},
			10, []string{"d", "b", "a", "c"},
		},
		{
			"negative fireAt fires at zero",
			[]struct {
				id string
				t  int64
			}{{"p", -3}, {"q", -10}, {"r", 0}},
			0, []string{"q", "p", "r"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			for _, task := range tc.tasks {
				h.Schedule(task.id, task.t)
			}
			fired := h.PopDue(tc.now)
			if len(fired) != len(tc.want) {
				t.Fatalf("got %v, want %v", fired, tc.want)
			}
			for i, e := range fired {
				if e.ID != tc.want[i] {
					t.Fatalf("position %d: got %s, want %s (full %v)", i, e.ID, tc.want[i], tc.want)
				}
			}
		})
	}
}

func TestPopChecksSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		for i := 0; i < m; i++ {
			h.Schedule("x"+strconv.Itoa(i), 1_000_000_000)
		}
		h.Schedule("early", 0)
		fired := h.PopDue(0)
		if len(fired) != 1 || fired[0].ID != "early" {
			t.Fatalf("m=%d: fired %v, want [early]", m, fired)
		}
		bound := 4.0 * math.Ceil(math.Log2(float64(m+1)))
		if float64(h.checks) > bound {
			t.Fatalf("m=%d: inspected %d nodes, bound %.0f (linear scan?)", m, h.checks, bound)
		}
		if h.checks == 0 {
			t.Fatalf("counter never advanced")
		}
	}
}

func TestCanceledSkipped(t *testing.T) {
	h := New()
	a := h.Schedule("a", 1)
	b := h.Schedule("b", 2)
	h.Cancel(a)
	fired := h.PopDue(10)
	if len(fired) != 1 || fired[0] != b {
		t.Fatalf("got %v, want only b", fired)
	}
}
