package profile

import "testing"

func TestParamsValid(t *testing.T) {
	base := Params{DB: 5, MinI: 100, MaxI: 1000, Lo: 0, Hi: 100}
	cases := []struct {
		name string
		p    Params
		want bool
	}{
		{"base", base, true},
		{"db_zero", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: 0, Hi: 0}, true},
		{"db_max", Params{DB: MaxDB, MinI: 1, MaxI: 1000, Lo: 0, Hi: 0}, true},
		{"db_over", Params{DB: MaxDB + 1, MinI: 1, MaxI: 1000, Lo: 0, Hi: 0}, false},
		{"db_neg", Params{DB: -1, MinI: 1, MaxI: 1000, Lo: 0, Hi: 0}, false},
		{"mini_zero", Params{DB: 0, MinI: 0, MaxI: 1000, Lo: 0, Hi: 0}, false},
		{"mini_eq_maxi", Params{DB: 0, MinI: 1000, MaxI: 1000, Lo: 0, Hi: 0}, false},
		{"mini_gt_maxi", Params{DB: 0, MinI: 2000, MaxI: 1000, Lo: 0, Hi: 0}, false},
		{"maxi_below_1000", Params{DB: 0, MinI: 1, MaxI: 999, Lo: 0, Hi: 0}, false},
		{"maxi_min", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: 0, Hi: 0}, true},
		{"maxi_max", Params{DB: 0, MinI: 1, MaxI: MaxMaxI, Lo: 0, Hi: 0}, true},
		{"maxi_over", Params{DB: 0, MinI: 1, MaxI: MaxMaxI + 1, Lo: 0, Hi: 0}, false},
		{"lo_eq_hi", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: 7, Hi: 7}, true},
		{"lo_gt_hi", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: 8, Hi: 7}, false},
		{"range_abs_max", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: -MaxAbsV, Hi: MaxAbsV}, true},
		{"range_abs_over", Params{DB: 0, MinI: 1, MaxI: 1000, Lo: -MaxAbsV - 1, Hi: 0}, false},
	}
	for _, c := range cases {
		if got := c.p.Valid(); got != c.want {
			t.Errorf("%s: Params%+v.Valid()=%v, want %v", c.name, c.p, got, c.want)
		}
	}
}

func TestValueOK(t *testing.T) {
	for _, v := range []int64{0, 1, -1, MaxAbsV, -MaxAbsV} {
		if !ValueOK(v) {
			t.Errorf("ValueOK(%d)=false, want true", v)
		}
	}
	for _, v := range []int64{MaxAbsV + 1, -MaxAbsV - 1} {
		if ValueOK(v) {
			t.Errorf("ValueOK(%d)=true, want false", v)
		}
	}
}
