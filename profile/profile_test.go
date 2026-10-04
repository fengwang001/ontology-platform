package profile

import "testing"

func TestValidParams(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		want bool
	}{
		{"ok", Params{DB: 5, MinInterval: 100, MaxInterval: 1000, Low: 0, High: 100}, true},
		{"db zero ok", Params{DB: 0, MinInterval: 1, MaxInterval: 1000, Low: -10, High: 10}, true},
		{"db neg", Params{DB: -1, MinInterval: 100, MaxInterval: 1000, Low: 0, High: 100}, false},
		{"db too big", Params{DB: MaxDB + 1, MinInterval: 100, MaxInterval: 1000, Low: 0, High: 100}, false},
		{"minI zero", Params{DB: 5, MinInterval: 0, MaxInterval: 1000, Low: 0, High: 100}, false},
		{"minI eq maxI", Params{DB: 5, MinInterval: 1000, MaxInterval: 1000, Low: 0, High: 100}, false},
		{"minI gt maxI", Params{DB: 5, MinInterval: 1001, MaxInterval: 1000, Low: 0, High: 100}, false},
		{"maxI too small", Params{DB: 5, MinInterval: 100, MaxInterval: 999, Low: 0, High: 100}, false},
		{"maxI too big", Params{DB: 5, MinInterval: 100, MaxInterval: MaxInterval + 1, Low: 0, High: 100}, false},
		{"lo gt hi", Params{DB: 5, MinInterval: 100, MaxInterval: 1000, Low: 101, High: 100}, false},
		{"low overflow", Params{DB: 5, MinInterval: 100, MaxInterval: 1000, Low: -MaxValue - 1, High: 100}, false},
		{"high overflow", Params{DB: 5, MinInterval: 100, MaxInterval: 1000, Low: 0, High: MaxValue + 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidParams(c.p); got != c.want {
				t.Fatalf("ValidParams = %v want %v", got, c.want)
			}
		})
	}
}

func TestRangeAndReplace(t *testing.T) {
	p := Params{Low: 0, High: 10}
	if p.Valid(-1) || p.Valid(11) || !p.Valid(0) || !p.Valid(10) {
		t.Fatalf("range check wrong")
	}
	c := NewCatalog()
	c.Set("a", p)
	got, ok := c.Get("a")
	if !ok || got != p {
		t.Fatalf("get got %v ok=%v", got, ok)
	}
	p2 := Params{DB: 1, MinInterval: 2, MaxInterval: 1000, Low: 5, High: 6}
	c.Set("a", p2)
	if got, _ := c.Get("a"); got != p2 {
		t.Fatalf("replace failed: %v", got)
	}
}
