package triage

type Consciousness int

const (
	A Consciousness = iota
	V
	P
	U
)

type Vitals struct {
	HR   int
	SBP  int
	SPO2 int
	LOC  Consciousness
}

func ValidVitals(v Vitals) bool {
	if v.HR < 0 || v.HR > 300 || v.SBP < 0 || v.SBP > 300 || v.SPO2 < 0 || v.SPO2 > 100 {
		return false
	}
	switch v.LOC {
	case A, V, P, U:
		return true
	default:
		return false
	}
}

func Score(v Vitals) (hr, sbp, spo2, loc, sum, max int) {
	switch {
	case v.HR < 40 || v.HR > 130:
		hr = 3
	case v.HR < 50 || v.HR > 110:
		hr = 2
	case v.HR > 100:
		hr = 1
	}
	switch {
	case v.SBP < 70:
		sbp = 3
	case v.SBP <= 80:
		sbp = 2
	case v.SBP <= 100:
		sbp = 1
	case v.SBP < 200:
		sbp = 0
	default:
		sbp = 2
	}
	switch {
	case v.SPO2 < 85:
		spo2 = 3
	case v.SPO2 < 90:
		spo2 = 2
	case v.SPO2 < 94:
		spo2 = 1
	}
	switch v.LOC {
	case V:
		loc = 1
	case P:
		loc = 2
	case U:
		loc = 3
	}
	sum = hr + sbp + spo2 + loc
	for _, x := range [4]int{hr, sbp, spo2, loc} {
		if x > max {
			max = x
		}
	}
	return
}

func Level(v Vitals) int {
	_, _, _, _, sum, max := Score(v)
	switch {
	case max == 3 || sum >= 7:
		return 1
	case sum >= 5:
		return 2
	case sum >= 3:
		return 3
	default:
		return 4
	}
}
