package triage

import "testing"

// 正常基线：四项全 0 分。
func base() Vitals { return Vitals{HR: 80, SBP: 120, SpO2: 98, Consciousness: 'A'} }

func TestScoresBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Vitals)
		want  [4]int
		basis string
	}{
		{"hr39=3", func(v *Vitals) { v.HR = 39 }, [4]int{3, 0, 0, 0}, "hr<40 记 3"},
		{"hr40=2", func(v *Vitals) { v.HR = 40 }, [4]int{2, 0, 0, 0}, "40..49 记 2"},
		{"hr49=2", func(v *Vitals) { v.HR = 49 }, [4]int{2, 0, 0, 0}, "40..49 记 2"},
		{"hr50=0", func(v *Vitals) { v.HR = 50 }, [4]int{0, 0, 0, 0}, "50..100 记 0"},
		{"hr100=0", func(v *Vitals) { v.HR = 100 }, [4]int{0, 0, 0, 0}, "50..100 记 0"},
		{"hr101=1", func(v *Vitals) { v.HR = 101 }, [4]int{1, 0, 0, 0}, "101..110 记 1"},
		{"hr110=1", func(v *Vitals) { v.HR = 110 }, [4]int{1, 0, 0, 0}, "101..110 记 1"},
		{"hr111=2", func(v *Vitals) { v.HR = 111 }, [4]int{2, 0, 0, 0}, "111..130 记 2"},
		{"hr130=2", func(v *Vitals) { v.HR = 130 }, [4]int{2, 0, 0, 0}, "111..130 记 2"},
		{"hr131=3", func(v *Vitals) { v.HR = 131 }, [4]int{3, 0, 0, 0}, "hr>130 记 3"},
		{"sbp69=3", func(v *Vitals) { v.SBP = 69 }, [4]int{0, 3, 0, 0}, "sbp<70 记 3"},
		{"sbp70=2", func(v *Vitals) { v.SBP = 70 }, [4]int{0, 2, 0, 0}, "70..80 记 2"},
		{"sbp80=2", func(v *Vitals) { v.SBP = 80 }, [4]int{0, 2, 0, 0}, "70..80 记 2"},
		{"sbp81=1", func(v *Vitals) { v.SBP = 81 }, [4]int{0, 1, 0, 0}, "81..100 记 1"},
		{"sbp100=1", func(v *Vitals) { v.SBP = 100 }, [4]int{0, 1, 0, 0}, "81..100 记 1"},
		{"sbp101=0", func(v *Vitals) { v.SBP = 101 }, [4]int{0, 0, 0, 0}, "101..199 记 0"},
		{"sbp199=0", func(v *Vitals) { v.SBP = 199 }, [4]int{0, 0, 0, 0}, "101..199 记 0"},
		{"sbp200=2", func(v *Vitals) { v.SBP = 200 }, [4]int{0, 2, 0, 0}, ">=200 记 2"},
		{"spo2_84=3", func(v *Vitals) { v.SpO2 = 84 }, [4]int{0, 0, 3, 0}, "spo2<85 记 3"},
		{"spo2_85=2", func(v *Vitals) { v.SpO2 = 85 }, [4]int{0, 0, 2, 0}, "85..89 记 2"},
		{"spo2_89=2", func(v *Vitals) { v.SpO2 = 89 }, [4]int{0, 0, 2, 0}, "85..89 记 2"},
		{"spo2_90=1", func(v *Vitals) { v.SpO2 = 90 }, [4]int{0, 0, 1, 0}, "90..93 记 1"},
		{"spo2_93=1", func(v *Vitals) { v.SpO2 = 93 }, [4]int{0, 0, 1, 0}, "90..93 记 1"},
		{"spo2_94=0", func(v *Vitals) { v.SpO2 = 94 }, [4]int{0, 0, 0, 0}, ">=94 记 0"},
		{"consA=0", func(v *Vitals) { v.Consciousness = 'A' }, [4]int{0, 0, 0, 0}, "A 记 0"},
		{"consV=1", func(v *Vitals) { v.Consciousness = 'V' }, [4]int{0, 0, 0, 1}, "V 记 1"},
		{"consP=2", func(v *Vitals) { v.Consciousness = 'P' }, [4]int{0, 0, 0, 2}, "P 记 2"},
		{"consU=3", func(v *Vitals) { v.Consciousness = 'U' }, [4]int{0, 0, 0, 3}, "U 记 3"},
	}
	for _, c := range cases {
		v := base()
		c.mut(&v)
		got := Scores(v)
		t.Logf("输入=%+v 输出=%v 判定依据=%s", v, got, c.basis)
		if got != c.want {
			t.Errorf("%s: Scores=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestLevelRules(t *testing.T) {
	cases := []struct {
		name  string
		v     Vitals
		want  int
		basis string
	}{
		{"例1_m3压总分", Vitals{135, 120, 96, 'A'}, 1, "s=3 但 m=3，单项 3 分定 1 级"},
		{"例2_各1分", Vitals{105, 95, 92, 'V'}, 3, "s=4，3 级"},
		{"例3_s5", Vitals{120, 75, 93, 'A'}, 2, "s=5，2 级"},
		{"例4_s2", Vitals{110, 100, 94, 'A'}, 4, "s=2，4 级"},
		{"s7为1级", Vitals{131, 75, 93, 'V'}, 1, "s=3+2+0+1=6<7 但 m=3 → 1 级"},
		{"s7无单项3", Vitals{111, 75, 89, 'V'}, 1, "s=2+2+2+1=7 → 1 级"},
		{"s6为2级", Vitals{111, 75, 93, 'V'}, 2, "s=2+2+1+1=6 → 2 级"},
		{"s5为2级", Vitals{101, 81, 90, 'P'}, 2, "s=1+1+1+2=5 → 2 级"},
		{"s4为3级", Vitals{101, 81, 90, 'V'}, 3, "s=1+1+1+1=4 → 3 级"},
		{"s3为3级", Vitals{101, 81, 90, 'A'}, 3, "s=1+1+1+0=3 → 3 级"},
		{"全0为4级", base(), 4, "s=0 → 4 级"},
		{"意识U单项3", Vitals{80, 120, 98, 'U'}, 1, "m=3（意识 U）→ 1 级"},
		{"sbp200记2", Vitals{80, 200, 98, 'A'}, 4, "s=2 → 4 级（sbp=200 记 2 而非 3）"},
	}
	for _, c := range cases {
		got := Level(c.v)
		t.Logf("输入=%+v 输出=%d 判定依据=%s", c.v, got, c.basis)
		if got != c.want {
			t.Errorf("%s: Level=%d, want %d", c.name, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		v    Vitals
		want bool
	}{
		{"正常", base(), true},
		{"hr下限", Vitals{0, 120, 98, 'A'}, true},
		{"hr上限", Vitals{300, 120, 98, 'A'}, true},
		{"hr越界", Vitals{301, 120, 98, 'A'}, false},
		{"hr负", Vitals{-1, 120, 98, 'A'}, false},
		{"sbp上限", Vitals{80, 300, 98, 'A'}, true},
		{"sbp越界", Vitals{80, 301, 98, 'A'}, false},
		{"spo2上限", Vitals{80, 120, 100, 'A'}, true},
		{"spo2越界", Vitals{80, 120, 101, 'A'}, false},
		{"意识非法", Vitals{80, 120, 98, 'X'}, false},
	}
	for _, c := range cases {
		if got := c.v.Valid(); got != c.want {
			t.Errorf("%s: Valid=%v, want %v", c.name, got, c.want)
		}
	}
}
