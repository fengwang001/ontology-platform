// Package triage 实现急诊生命体征评分与定级（纯函数，无状态）。
package triage

// Vitals 为一组生命体征：心率、收缩压、血氧与意识（AVPU）。
type Vitals struct {
	HR            int  // 心率，0..300
	SBP           int  // 收缩压，0..300
	SpO2          int  // 血氧，0..100
	Consciousness byte // 意识：'A'、'V'、'P'、'U'
}

// Valid 报告生命体征各分量是否在合法范围内。
func (v Vitals) Valid() bool {
	if v.HR < 0 || v.HR > 300 || v.SBP < 0 || v.SBP > 300 || v.SpO2 < 0 || v.SpO2 > 100 {
		return false
	}
	switch v.Consciousness {
	case 'A', 'V', 'P', 'U':
		return true
	}
	return false
}

func hrScore(hr int) int {
	switch {
	case hr < 40 || hr > 130:
		return 3
	case hr < 50 || hr > 110: // 40..49 或 111..130
		return 2
	case hr > 100: // 101..110
		return 1
	default: // 50..100
		return 0
	}
}

func sbpScore(sbp int) int {
	switch {
	case sbp < 70:
		return 3
	case sbp <= 80: // 70..80
		return 2
	case sbp <= 100: // 81..100
		return 1
	case sbp < 200: // 101..199
		return 0
	default: // >= 200
		return 2
	}
}

func spo2Score(spo2 int) int {
	switch {
	case spo2 < 85:
		return 3
	case spo2 < 90: // 85..89
		return 2
	case spo2 < 94: // 90..93
		return 1
	default: // >= 94
		return 0
	}
}

func consciousnessScore(c byte) int {
	switch c {
	case 'A':
		return 0
	case 'V':
		return 1
	case 'P':
		return 2
	default: // 'U'
		return 3
	}
}

// Scores 返回四项单项分，顺序为心率、收缩压、血氧、意识。
func Scores(v Vitals) [4]int {
	return [4]int{hrScore(v.HR), sbpScore(v.SBP), spo2Score(v.SpO2), consciousnessScore(v.Consciousness)}
}

// Level 按总分与最大单项定级，返回 1..4；输入须满足 Valid。
// m=3 或 s>=7 为 1 级；否则 s>=5 为 2 级；s>=3 为 3 级；其余 4 级。
func Level(v Vitals) int {
	sum, max := 0, 0
	for _, s := range Scores(v) {
		sum += s
		if s > max {
			max = s
		}
	}
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
