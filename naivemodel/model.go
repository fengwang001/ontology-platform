// Package naivemodel 是接触者推导的独立朴素参考实现：
// 每次都从全部住宿与病例出发全量重算，不共享 contacttracing 包的任何代码与数据结构，
// 作为随机差分测试的“事实基准”，并给出判定依据。
package naivemodel

// Stay 朴素住宿区间。
type Stay struct {
	Patient      string
	Room         string
	CheckIn      int64
	CheckOut     int64
	CheckOutOpen bool
}

// Case 朴素病例。
type Case struct {
	ID            string
	Patient       string
	OnsetAt       int64
	RegisteredAt  int64
	IsolatedAt    int64
	IsolationOpen bool
	Revoked       bool
}

// Model 保存全部输入快照。
type Model struct {
	Now   int64
	Stays []Stay
	Cases []*Case
}

const (
	minMinutes    int64 = 120
	leadMinutes   int64 = 48 * 60
	closeMinutes  int64 = 7 * 24 * 60
	secondMinutes int64 = 3 * 24 * 60
)

func overlap(aL, aR, bL, bR int64) int64 {
	l, r := aL, aR
	if bL > l {
		l = bL
	}
	if bR < r {
		r = bR
	}
	if r <= l {
		return 0
	}
	return r - l
}

// Source 是某病例下某患者的接触结论。
type Source struct {
	Kind          int // 1=密接 2=次密接
	LastContactAt int64
}

// CaseResult 是某病例的全量推导结果。
type CaseResult struct {
	Sources map[string]Source
}

type seg struct{ l, r int64 }

func stayEnd(s Stay, now int64) int64 {
	if s.CheckOutOpen {
		return now
	}
	return s.CheckOut
}

// DeriveCase 全量重算单个病例。
func (m *Model) DeriveCase(c *Case) CaseResult {
	res := CaseResult{Sources: map[string]Source{}}
	if c.Revoked {
		return res
	}
	iL := c.OnsetAt - leadMinutes
	if iL < 0 {
		iL = 0
	}
	iR := m.Now
	if !c.IsolationOpen {
		iR = c.IsolatedAt
	}
	if iR < iL {
		iR = iL
	}

	// 第一圈：对每个其他患者，逐病房逐住宿段求与病例传染期的正重叠并累计。
	patients := map[string]struct{}{}
	for _, st := range m.Stays {
		patients[st.Patient] = struct{}{}
	}

	type agg struct {
		total  int64
		lastR  int64
		firstL int64
	}
	closeInfo := map[string]*agg{}

	for other := range patients {
		if other == c.Patient {
			continue
		}
		a := &agg{firstL: 1 << 62}
		for _, os := range m.Stays {
			if os.Patient != other {
				continue
			}
			oe := stayEnd(os, m.Now)
			for _, cs := range m.Stays {
				if cs.Patient != c.Patient || cs.Room != os.Room {
					continue
				}
				ce := stayEnd(cs, m.Now)
				// 病例段先裁到传染期窗口。
				l, r := cs.CheckIn, ce
				if iL > l {
					l = iL
				}
				if iR < r {
					r = iR
				}
				if n := overlap(os.CheckIn, oe, l, r); n > 0 {
					// 重新求交用于记录端点。
					sl, sr := os.CheckIn, oe
					if l > sl {
						sl = l
					}
					if r < sr {
						sr = r
					}
					a.total += n
					if sl < a.firstL {
						a.firstL = sl
					}
					if sr > a.lastR {
						a.lastR = sr
					}
				}
			}
		}
		if a.total >= minMinutes {
			closeInfo[other] = a
			res.Sources[other] = Source{Kind: 1, LastContactAt: a.lastR}
		}
	}

	// 第二圈：次密接。
	type secAgg struct {
		total int64
		lastR int64
	}
	sec := map[string]*secAgg{}
	for closeP, ci := range closeInfo {
		eL, eR := ci.firstL, c.RegisteredAt
		if eR <= eL {
			continue
		}
		for other := range patients {
			if other == c.Patient || other == closeP {
				continue
			}
			if _, isClose := closeInfo[other]; isClose {
				continue
			}
			if _, done := res.Sources[other]; done {
				continue
			}
			for _, os := range m.Stays {
				if os.Patient != other {
					continue
				}
				oe := stayEnd(os, m.Now)
				for _, ks := range m.Stays {
					if ks.Patient != closeP || ks.Room != os.Room {
						continue
					}
					ke := stayEnd(ks, m.Now)
					l, r := ks.CheckIn, ke
					if eL > l {
						l = eL
					}
					if eR < r {
						r = eR
					}
					if n := overlap(os.CheckIn, oe, l, r); n > 0 {
						sl, sr := os.CheckIn, oe
						if l > sl {
							sl = l
						}
						if r < sr {
							sr = r
						}
						a := sec[other]
						if a == nil {
							a = &secAgg{}
							sec[other] = a
						}
						a.total += n
						if sr > a.lastR {
							a.lastR = sr
						}
					}
				}
			}
		}
	}
	for other, a := range sec {
		if a.total >= minMinutes {
			res.Sources[other] = Source{Kind: 2, LastContactAt: a.lastR}
		}
	}
	return res
}

// Status 朴素患者状态。
type Status struct {
	Kind      int // 0 无关 1 密接隔离中 2 次密接观察中 3 已解除
	ReleaseAt int64
}

// PatientStatus 全量扫描全部病例计算患者状态。
func (m *Model) PatientStatus(patient string) Status {
	var activeKind int
	var activeRelT, everRelT int64
	ever := false
	for _, c := range m.Cases {
		if c.Patient == patient || c.Revoked {
			continue
		}
		src, ok := m.DeriveCase(c).Sources[patient]
		if !ok {
			continue
		}
		var boundary int64
		if src.Kind == 1 {
			boundary = src.LastContactAt + closeMinutes
		} else {
			boundary = src.LastContactAt + secondMinutes
		}
		ever = true
		if boundary > everRelT {
			everRelT = boundary
		}
		if m.Now < boundary {
			if activeKind == 0 || src.Kind == 1 {
				activeKind = src.Kind
			}
			if boundary > activeRelT {
				activeRelT = boundary
			}
		}
	}
	if !ever {
		return Status{Kind: 0}
	}
	if activeKind != 0 {
		return Status{Kind: activeKind, ReleaseAt: activeRelT}
	}
	return Status{Kind: 3, ReleaseAt: everRelT}
}
