package triage

import "testing"

func TestValidVitals(t *testing.T) {
	cases := []struct {
		name string
		v    Vitals
		want bool
	}{
		{"normal", Vitals{80, 120, 98, A}, true},
		{"hr low", Vitals{-1, 120, 98, A}, false},
		{"hr high", Vitals{301, 120, 98, A}, false},
		{"sbp high", Vitals{80, 301, 98, A}, false},
		{"spo2 high", Vitals{80, 120, 101, A}, false},
		{"loc bad", Vitals{80, 120, 98, Consciousness(9)}, false},
		{"boundary zeros", Vitals{0, 0, 0, U}, true},
		{"boundary max", Vitals{300, 300, 100, U}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidVitals(c.v); got != c.want {
				t.Fatalf("ValidVitals(%+v)=%v want %v", c.v, got, c.want)
			}
		})
	}
}

func TestScore(t *testing.T) {
	cases := []struct {
		name                        string
		v                           Vitals
		hrP, sbpP, spo2P, locP, sum int
	}{
		{"hr boundaries", Vitals{HR: 135, SBP: 120, SPO2: 96, LOC: A}, 3, 0, 0, 0, 3},
		{"all ones", Vitals{HR: 105, SBP: 95, SPO2: 92, LOC: V}, 1, 1, 1, 1, 4},
		{"mixed 5", Vitals{HR: 120, SBP: 75, SPO2: 93, LOC: A}, 2, 2, 1, 0, 5},
		{"low total 2", Vitals{HR: 110, SBP: 100, SPO2: 94, LOC: A}, 1, 1, 0, 0, 2},
		{"hr 39/40/49/50", Vitals{HR: 39, SBP: 150, SPO2: 95, LOC: A}, 3, 0, 0, 0, 3},
		{"hr 130/131", Vitals{HR: 131, SBP: 150, SPO2: 95, LOC: A}, 3, 0, 0, 0, 3},
		{"sbp 69/70/80/81", Vitals{HR: 80, SBP: 69, SPO2: 95, LOC: A}, 0, 3, 0, 0, 3},
		{"sbp 199/200", Vitals{HR: 80, SBP: 200, SPO2: 95, LOC: A}, 0, 2, 0, 0, 2},
		{"spo2 84/85/89/90/93/94", Vitals{HR: 80, SBP: 150, SPO2: 84, LOC: A}, 0, 0, 3, 0, 3},
		{"loc U", Vitals{HR: 80, SBP: 150, SPO2: 95, LOC: U}, 0, 0, 0, 3, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hr, sbp, sp, loc, sum, _ := Score(c.v)
			if hr != c.hrP || sbp != c.sbpP || sp != c.spo2P || loc != c.locP || sum != c.sum {
				t.Fatalf("Score(%+v)=(%d,%d,%d,%d;sum=%d) want (%d,%d,%d,%d;%d)",
					c.v, hr, sbp, sp, loc, sum, c.hrP, c.sbpP, c.spo2P, c.locP, c.sum)
			}
		})
	}
}

func TestLevelBoundary(t *testing.T) {
	cases := []struct {
		name string
		v    Vitals
		want int
		why  string
	}{
		{"m3 beats low sum", Vitals{135, 120, 96, A}, 1, "hr=3,s=3,m=3"},
		{"sum 4 level3", Vitals{105, 95, 92, V}, 3, "s=4,m=1"},
		{"sum 5 level2", Vitals{120, 75, 93, A}, 2, "s=5,m=2"},
		{"sum 2 level4", Vitals{110, 100, 94, A}, 4, "s=2,m=1"},
		{"sum 7 level1", Vitals{HR: 120, SBP: 69, SPO2: 92, LOC: V}, 1, "2+3+1+1=7"},
		{"sbp200 2pts", Vitals{HR: 80, SBP: 200, SPO2: 96, LOC: A}, 4, "s=2: sbp=2"},
		{"sbp199 0pts", Vitals{HR: 80, SBP: 199, SPO2: 96, LOC: A}, 4, "s=0: sbp=0"},
		{"hr exact 40", Vitals{40, 150, 95, A}, 4, "hr=2 => s=2"},
		{"hr exact 130", Vitals{130, 150, 95, A}, 4, "hr=2"},
		{"hr exact 49", Vitals{49, 150, 95, A}, 4, "hr=2"},
		{"hr exact 111", Vitals{111, 150, 95, A}, 4, "hr=2"},
		{"hr exact 101", Vitals{101, 150, 95, A}, 4, "hr=1"},
		{"sbp exact 70", Vitals{80, 70, 95, A}, 4, "sbp=2"},
		{"sbp exact 81", Vitals{80, 81, 95, A}, 4, "sbp=1"},
		{"spo2 exact 85", Vitals{80, 150, 85, A}, 4, "spo2=2"},
		{"spo2 exact 90", Vitals{80, 150, 90, A}, 4, "spo2=1"},
		{"spo2 exact 94", Vitals{80, 150, 94, A}, 4, "spo2=0"},
		{"loc P", Vitals{80, 150, 95, P}, 4, "loc=2,s=2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Level(c.v); got != c.want {
				t.Fatalf("Level(%s)=%d want %d (%s)", c.name, got, c.want, c.why)
			}
		})
	}
}
