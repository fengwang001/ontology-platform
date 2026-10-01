package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveTarget 是按题目规则逐步直写的朴素模型，刻意不依赖被测实现。
type naiveTarget struct {
	healthy bool
	a, b    int64
	nd      int64
	fu      int64
	tr      []int64
}

type naiveModel struct {
	n, r, f, i, fi, di, wf, q int64
	maxNow                    int64
	targets                   []naiveTarget
	log                       strings.Builder
}

func newNaive(n, r, f, i, fi, di int64, init bool, wf, q int64) *naiveModel {
	m := &naiveModel{n: n, r: r, f: f, i: i, fi: fi, di: di, wf: wf, q: q}
	m.targets = make([]naiveTarget, n)
	for k := range m.targets {
		m.targets[k].healthy = init
	}
	return m
}

func (m *naiveModel) probe(id int, ok bool, now int64) error {
	fmt.Fprintf(&m.log, "input  Probe(target=%d ok=%v now=%d)\n", id, ok, now)
	if id < 0 || int64(id) >= m.n {
		fmt.Fprintf(&m.log, "output reject ErrTargetOutOfRange: %d not in [0,%d)\n", id, m.n)
		return ErrTargetOutOfRange
	}
	if now < 0 || now > maxNow {
		fmt.Fprintf(&m.log, "output reject ErrInvalidTime: now=%d outside [0,1e15]\n", now)
		return ErrInvalidTime
	}
	if now < m.maxNow {
		fmt.Fprintf(&m.log, "output reject ErrClockWentBackwards: now=%d < maxNow=%d\n", now, m.maxNow)
		return ErrClockWentBackwards
	}
	tgt := &m.targets[id]
	if now < tgt.nd {
		fmt.Fprintf(&m.log, "output reject ErrProbeTooEarly: now=%d < nd=%d\n", now, tgt.nd)
		return ErrProbeTooEarly
	}

	oldHealthy := tgt.healthy
	if tgt.healthy {
		if ok {
			tgt.b = 0
			fmt.Fprintf(&m.log, "judge  healthy+success: b reset to 0\n")
		} else {
			tgt.b++
			fmt.Fprintf(&m.log, "judge  healthy+failure: b=%d", tgt.b)
			if tgt.b >= m.f {
				tgt.healthy = false
				tgt.a, tgt.b = 0, 0
				tgt.tr = append(tgt.tr, now)
				fmt.Fprintf(&m.log, " >= F=%d -> TRANSITION down, tr=%v, a,b reset", m.f, tgt.tr)
			}
			m.log.WriteByte('\n')
		}
	} else {
		if !ok {
			tgt.a = 0
			fmt.Fprintf(&m.log, "judge  unhealthy+failure: a reset to 0\n")
		} else {
			tgt.a++
			g := int64(0)
			for _, tt := range tgt.tr {
				if tt+m.wf > now {
					g++
				}
			}
			rEff := m.r * (1 + min(g, 4))
			fmt.Fprintf(&m.log, "judge  unhealthy+success: a=%d, g=%d, Reff=%d", tgt.a, g, rEff)
			if tgt.a >= rEff {
				tgt.healthy = true
				tgt.a, tgt.b = 0, 0
				tgt.tr = append(tgt.tr, now)
				fmt.Fprintf(&m.log, " >= Reff -> TRANSITION up, tr=%v, a,b reset", tgt.tr)
			}
			m.log.WriteByte('\n')
		}
	}

	candidate := int64(0)
	fast := false
	switch {
	case tgt.healthy && tgt.b > 0:
		candidate, fast = m.fi, true
	case tgt.healthy:
		candidate = m.i
	case tgt.a > 0:
		candidate, fast = m.fi, true
	default:
		candidate = m.di
	}

	chosen := candidate
	if fast {
		x := int64(0)
		for k := range m.targets {
			if k != id && m.targets[k].fu > now {
				x++
			}
		}
		fmt.Fprintf(&m.log, "judge  fast candidate FI=%d, other active fu count x=%d, Q=%d", m.fi, x, m.q)
		if x >= m.q {
			if tgt.healthy {
				chosen = m.i
			} else {
				chosen = m.di
			}
			tgt.fu = 0
			fmt.Fprintf(&m.log, " -> quota full, fallback interval=%d, fu=0\n", chosen)
		} else {
			chosen = m.fi
			tgt.fu = now + m.fi
			fmt.Fprintf(&m.log, " -> take FI, fu=%d\n", tgt.fu)
		}
	} else {
		tgt.fu = 0
		fmt.Fprintf(&m.log, "judge  regular candidate interval=%d, fu=0", chosen)
		if oldHealthy != tgt.healthy {
			m.log.WriteString(" (chosen by post-transition state)")
		}
		m.log.WriteByte('\n')
	}
	tgt.nd = now + chosen
	if now > m.maxNow {
		m.maxNow = now
	}
	fmt.Fprintf(&m.log, "output accepted: healthy=%v a=%d b=%d nd=%d fu=%d tr=%v\n\n",
		tgt.healthy, tgt.a, tgt.b, tgt.nd, tgt.fu, tgt.tr)
	return nil
}

func (m *naiveModel) snapshot(id int) TargetState {
	tgt := &m.targets[id]
	tr := append([]int64(nil), tgt.tr...)
	return TargetState{Healthy: tgt.healthy, A: tgt.a, B: tgt.b, ND: tgt.nd, FU: tgt.fu, TR: tr}
}

// TestDifferentialRandom: 2000 组随机探测序列与朴素模型逐步对照。
func TestDifferentialRandom(t *testing.T) {
	const groups = 2000
	for seed := int64(1); seed <= groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := int64(1 + rng.Intn(4))
		r := int64(1 + rng.Intn(5))
		f := int64(1 + rng.Intn(5))
		i := int64(1 + rng.Intn(20))
		fi := int64(1 + rng.Intn(10))
		di := int64(1 + rng.Intn(40))
		wf := int64(1 + rng.Intn(60))
		q := int64(rng.Intn(int(n) + 1))
		init := rng.Intn(2) == 0

		hc, err := NewHealthChecker(n, r, f, i, fi, di, init, wf, q)
		if err != nil {
			t.Fatalf("seed=%d valid config rejected: %v", seed, err)
		}
		nm := newNaive(n, r, f, i, fi, di, init, wf, q)
		fmt.Fprintf(&nm.log,
			"=== seed=%d config N=%d R=%d F=%d I=%d FI=%d DI=%d Wf=%d Q=%d initialHealthy=%v ===\n",
			seed, n, r, f, i, fi, di, wf, q, init)

		ops := 30 + rng.Intn(21)
		nowCursor := make([]int64, n)
		globalNow := int64(0)
		for op := 0; op < ops; op++ {
			id := rng.Intn(int(n))
			ok := rng.Intn(2) == 0
			var now int64
			switch rng.Intn(10) {
			case 0, 1, 2: // 非法时间或回退，触发拒绝分支
				if rng.Intn(2) == 0 {
					now = -1
				} else if globalNow > 0 {
					now = globalNow - 1
				} else {
					now = 0
				}
			case 3, 4: // 过早探测
				now = nowCursor[id]
				if s, _ := hc.State(id); now < s.ND {
					// keep now below nd
				} else {
					now = s.ND
				}
			default: // 合法推进
				s, _ := hc.State(id)
				if s.ND > globalNow {
					now = s.ND
				} else {
					now = globalNow + int64(rng.Intn(5))
				}
			}

			badID := rng.Intn(15) == 0
			probeID := id
			if badID {
				probeID = int(n) + rng.Intn(3)
			}

			gotErr := hc.Probe(probeID, ok, now)
			wantErr := nm.probe(probeID, ok, now)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("seed=%d op=%d Probe(%d,%v,%d) error mismatch:\nwant=%v\ngot =%v\n--- naive trace ---\n%s",
					seed, op, probeID, ok, now, wantErr, gotErr, nm.log.String())
			}
			if gotErr == nil && !badID && now >= 0 {
				if now > globalNow {
					globalNow = now
				}
				s, _ := hc.State(id)
				nowCursor[id] = s.ND
			}

			for k := int64(0); k < n; k++ {
				got, gerr := hc.State(int(k))
				want := nm.snapshot(int(k))
				if gerr != nil || !equalState(got, want) {
					t.Fatalf("seed=%d op=%d state target %d mismatch:\nwant=%+v\ngot =%+v\n--- naive trace ---\n%s",
						seed, op, k, want, got, nm.log.String())
				}
			}
			// 被接受后，在该 now 处 fu > now 的目标数不超过 Q。
			if gotErr == nil && now >= 0 {
				active := int64(0)
				for k := int64(0); k < n; k++ {
					if nm.targets[k].fu > now {
						active++
					}
				}
				if active > q {
					t.Fatalf("seed=%d op=%d quota invariant violated: active=%d > Q=%d\n--- trace ---\n%s",
						seed, op, active, q, nm.log.String())
				}
			}
			if h := hc.Healthy(); !healthyEqual(h, nm, n) {
				t.Fatalf("seed=%d Healthy mismatch: got=%v\n--- trace ---\n%s", seed, h, nm.log.String())
			}
		}

		// 前 3 组打印完整输入、输出与判定依据。
		if seed <= 3 {
			t.Logf("\n%s", nm.log.String())
		}
	}
}

func healthyEqual(got []int, m *naiveModel, n int64) bool {
	k := 0
	for id := int64(0); id < n; id++ {
		if m.targets[id].healthy {
			if k >= len(got) || got[k] != int(id) {
				return false
			}
			k++
		}
	}
	return k == len(got)
}
