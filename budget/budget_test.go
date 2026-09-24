package budget

import "testing"

func TestMeterCounts(t *testing.T) {
	cases := []struct {
		limit int64
		steps int
		want  int64
	}{
		{0, 0, 0},
		{0, 5, 5},
		{10, 10, 10},
		{3, 7, 7},
	}
	for _, c := range cases {
		m := New(c.limit)
		for i := 0; i < c.steps; i++ {
			m.Step()
		}
		if m.steps != c.want || m.Steps() != c.want {
			t.Errorf("limit=%d steps=%d: got %d, want %d",
				c.limit, c.steps, m.steps, c.want)
		}
	}
}

func TestMeterLimit(t *testing.T) {
	cases := []struct {
		limit int64
		want  []bool
	}{
		{3, []bool{true, true, true, false, false}},
		{1, []bool{true, false}},
		{0, []bool{true, true, true, true}},
		{-1, []bool{true, true}},
	}
	for _, c := range cases {
		m := New(c.limit)
		for i, want := range c.want {
			if got := m.Step(); got != want {
				t.Errorf("limit=%d step %d: got %v, want %v",
					c.limit, i, got, want)
			}
		}
	}
}

func TestMetersIndependent(t *testing.T) {
	a, b := New(0), New(0)
	for i := 0; i < 3; i++ {
		a.Step()
	}
	for i := 0; i < 7; i++ {
		b.Step()
	}
	if a.Steps() != 3 || b.Steps() != 7 {
		t.Errorf("meters interfered: %d, %d", a.Steps(), b.Steps())
	}
}
