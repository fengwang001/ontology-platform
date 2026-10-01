package limit_test

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"testing"

	"ontology/limit"
)

// model is a naive reference implementation of the specification, written
// independently of the controller and using big.Int arithmetic throughout.
// It exists only to cross-check the controller on random operation
// sequences.
type model struct {
	lambda int64
	prices map[string]int64
	groups map[string]string
	limits map[string]int64
	pos    map[string]map[string]int64
	coll   map[string]int64
	marks  map[string]uint64
	seq    uint64
}

func newModel(lambda int64) *model {
	return &model{
		lambda: lambda,
		prices: make(map[string]int64),
		groups: make(map[string]string),
		limits: make(map[string]int64),
		pos:    make(map[string]map[string]int64),
		coll:   make(map[string]int64),
		marks:  make(map[string]uint64),
	}
}

// exposure computes E = max(0, W + sum_g ceil(lambda*H_g/10000) - G).
func (m *model) exposure(cp string) *big.Int {
	w := new(big.Int)
	longs := make(map[string]*big.Int)
	shorts := make(map[string]*big.Int)
	for inst, q := range m.pos[cp] {
		if q == 0 {
			continue
		}
		px := big.NewInt(m.prices[inst])
		v := new(big.Int).Mul(big.NewInt(q), px)
		w.Add(w, v)
		g := m.groups[inst]
		if q > 0 {
			if longs[g] == nil {
				longs[g] = new(big.Int)
			}
			longs[g].Add(longs[g], v)
		} else {
			if shorts[g] == nil {
				shorts[g] = new(big.Int)
			}
			shorts[g].Sub(shorts[g], v)
		}
	}
	total := new(big.Int).Set(w)
	for g, l := range longs {
		s := shorts[g]
		if s == nil {
			continue
		}
		h := new(big.Int).Set(l)
		if s.Cmp(l) < 0 {
			h.Set(s)
		}
		buf := new(big.Int).Mul(big.NewInt(m.lambda), h)
		buf.Add(buf, big.NewInt(9999))
		buf.Quo(buf, big.NewInt(10000))
		total.Add(total, buf)
	}
	e := total.Sub(total, big.NewInt(m.coll[cp]))
	if e.Sign() < 0 {
		return new(big.Int)
	}
	return e
}

func (m *model) recheck() {
	names := make([]string, 0, len(m.limits))
	for name := range m.limits {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		over := m.exposure(name).Cmp(big.NewInt(m.limits[name])) > 0
		if over {
			if m.marks[name] == 0 {
				m.seq++
				m.marks[name] = m.seq
			}
		} else {
			delete(m.marks, name)
		}
	}
}

func (m *model) register(cp string, l int64) error {
	if cp == "" || l < 0 || l > 1_000_000_000_000_000 {
		return limit.ErrInvalidParam
	}
	if _, ok := m.limits[cp]; ok {
		return limit.ErrDuplicateCounterparty
	}
	m.limits[cp] = l
	m.pos[cp] = make(map[string]int64)
	m.recheck()
	return nil
}

func (m *model) setPrice(inst string, px int64) error {
	if inst == "" || px < 1 || px > 1_000_000 {
		return limit.ErrInvalidParam
	}
	if _, ok := m.prices[inst]; !ok {
		if len(m.prices) >= 1000 {
			return limit.ErrInvalidParam
		}
		m.groups[inst] = inst
	}
	m.prices[inst] = px
	m.recheck()
	return nil
}

func (m *model) setGroup(inst, g string) error {
	if inst == "" || g == "" {
		return limit.ErrInvalidParam
	}
	if _, ok := m.prices[inst]; !ok {
		return limit.ErrNoPrice
	}
	m.groups[inst] = g
	m.recheck()
	return nil
}

func (m *model) trade(cp, inst string, n int64) (error, string) {
	if cp == "" || inst == "" || n == 0 || n > 1_000_000 || n < -1_000_000 {
		return limit.ErrInvalidParam, "invalid parameter"
	}
	if _, ok := m.limits[cp]; !ok {
		return limit.ErrNotRegistered, "counterparty not registered"
	}
	if _, ok := m.prices[inst]; !ok {
		return limit.ErrNoPrice, "instrument has no price"
	}
	q := m.pos[cp][inst] + n
	if q > 1_000_000 || q < -1_000_000 {
		return limit.ErrInvalidParam, "position bound exceeded"
	}
	before := m.exposure(cp)
	m.pos[cp][inst] = q
	after := m.exposure(cp)
	l := big.NewInt(m.limits[cp])
	if after.Cmp(l) <= 0 || after.Cmp(before) <= 0 {
		m.recheck()
		return nil, fmt.Sprintf("accept: E'=%s <= L=%s or E' <= E=%s", after, l, before)
	}
	m.pos[cp][inst] -= n
	return limit.ErrLimitExceeded, fmt.Sprintf("reject: E'=%s > L=%s and E' > E=%s", after, l, before)
}

func (m *model) post(cp string, g int64) error {
	if cp == "" || g < 1 || g > 1_000_000_000_000 {
		return limit.ErrInvalidParam
	}
	if _, ok := m.limits[cp]; !ok {
		return limit.ErrNotRegistered
	}
	if m.coll[cp]+g > 1_000_000_000_000_000 {
		return limit.ErrInvalidParam
	}
	m.coll[cp] += g
	m.recheck()
	return nil
}

func (m *model) release(cp string, g int64) (error, string) {
	if cp == "" || g < 1 {
		return limit.ErrInvalidParam, "invalid parameter"
	}
	if _, ok := m.limits[cp]; !ok {
		return limit.ErrNotRegistered, "counterparty not registered"
	}
	if g > m.coll[cp] {
		return limit.ErrInsufficientCollateral, "insufficient collateral"
	}
	before := m.exposure(cp)
	m.coll[cp] -= g
	after := m.exposure(cp)
	l := big.NewInt(m.limits[cp])
	if after.Cmp(l) <= 0 || after.Cmp(before) <= 0 {
		m.recheck()
		return nil, fmt.Sprintf("accept: E'=%s <= L=%s or E' <= E=%s", after, l, before)
	}
	m.coll[cp] += g
	return limit.ErrLimitExceeded, fmt.Sprintf("reject: E'=%s > L=%s and E' > E=%s", after, l, before)
}

type modelBreach struct {
	name string
	exp  *big.Int
	seq  uint64
}

func (m *model) breaches() []modelBreach {
	var out []modelBreach
	for name, seq := range m.marks {
		out = append(out, modelBreach{name, m.exposure(name), seq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// TestRandomAgainstModel replays 2000 random operation sequences against
// both the controller and the naive model, requiring identical accept /
// reject decisions, exposures and breach lists at every step. Each step is
// logged with its inputs, outputs and decision basis.
func TestRandomAgainstModel(t *testing.T) {
	cps := []string{"A", "B", "C", "D", "E", "F", "G"}
	insts := []string{"X", "Y", "Z", "U", "V", "W"}
	groups := []string{"g1", "g2", "g3"}
	lambdas := []int64{0, 1, 337, 1000, 5000, 9999, 10000}
	limits := []int64{0, 1, 10, 50, 100, 1000, 100_000, 1_000_000_000_000_000}

	pick := func(r *rand.Rand, s []string) string { return s[r.Intn(len(s))] }
	randQty := func(r *rand.Rand) int64 {
		switch r.Intn(20) {
		case 0:
			return 0 // invalid
		case 1:
			return 1_000_000
		case 2:
			return -1_000_000
		case 3:
			return 2_000_000 // invalid
		default:
			return int64(r.Intn(41) - 20)
		}
	}

	for trial := 0; trial < 2000; trial++ {
		r := rand.New(rand.NewSource(int64(trial)))
		lambda := lambdas[r.Intn(len(lambdas))]
		c, err := limit.New(lambda)
		if err != nil {
			t.Fatalf("trial %d: New(%d): %v", trial, lambda, err)
		}
		m := newModel(lambda)
		t.Logf("trial %d: New(lambda=%d)", trial, lambda)

		steps := 20 + r.Intn(30)
		for step := 0; step < steps; step++ {
			var gotErr, wantErr error
			var desc, basis string
			switch r.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14:
				cp := pick(r, cps)
				l := limits[r.Intn(len(limits))]
				if r.Intn(50) == 0 {
					l = -1 // invalid
				}
				desc = fmt.Sprintf("Register(%s, %d)", cp, l)
				gotErr = c.Register([]byte(cp), l)
				wantErr = m.register(cp, l)
			case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
				inst := pick(r, insts)
				px := int64(r.Intn(100) + 1)
				if r.Intn(50) == 0 {
					px = 0 // invalid
				}
				desc = fmt.Sprintf("SetPrice(%s, %d)", inst, px)
				gotErr = c.SetPrice([]byte(inst), px)
				wantErr = m.setPrice(inst, px)
			case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
				inst := pick(r, insts)
				g := pick(r, groups)
				if r.Intn(50) == 0 {
					g = "" // invalid
				}
				desc = fmt.Sprintf("SetGroup(%s, %s)", inst, g)
				gotErr = c.SetGroup([]byte(inst), []byte(g))
				wantErr = m.setGroup(inst, g)
			case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49,
				50, 51, 52, 53, 54, 55, 56, 57, 58, 59,
				60, 61, 62, 63, 64, 65, 66, 67, 68, 69,
				70, 71, 72, 73, 74:
				cp, inst := pick(r, cps), pick(r, insts)
				n := randQty(r)
				desc = fmt.Sprintf("Trade(%s, %s, %d)", cp, inst, n)
				gotErr = c.Trade([]byte(cp), []byte(inst), n)
				wantErr, basis = m.trade(cp, inst, n)
			case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89:
				cp := pick(r, cps)
				g := int64(r.Intn(500) + 1)
				if r.Intn(50) == 0 {
					g = 0 // invalid
				}
				desc = fmt.Sprintf("Post(%s, %d)", cp, g)
				gotErr = c.Post([]byte(cp), g)
				wantErr = m.post(cp, g)
			default:
				cp := pick(r, cps)
				g := int64(r.Intn(600))
				desc = fmt.Sprintf("Release(%s, %d)", cp, g)
				gotErr = c.Release([]byte(cp), g)
				wantErr, basis = m.release(cp, g)
			}

			if (gotErr == nil) != (wantErr == nil) ||
				(gotErr != nil && gotErr.Error() != wantErr.Error()) {
				t.Fatalf("trial %d step %d: %s: controller=%v model=%v",
					trial, step, desc, gotErr, wantErr)
			}
			t.Logf("trial %d step %d: %s -> %v (%s)", trial, step, desc, gotErr, basis)

			for _, cp := range cps {
				gotExp, gotErr := c.Exposure([]byte(cp))
				_, registered := m.limits[cp]
				if !registered {
					if !errors.Is(gotErr, limit.ErrNotRegistered) {
						t.Fatalf("trial %d step %d: Exposure(%s) err=%v, want ErrNotRegistered",
							trial, step, cp, gotErr)
					}
					continue
				}
				if gotErr != nil {
					t.Fatalf("trial %d step %d: Exposure(%s): %v", trial, step, cp, gotErr)
				}
				if wantExp := m.exposure(cp); gotExp.Cmp(wantExp) != 0 {
					t.Fatalf("trial %d step %d: Exposure(%s)=%s, model=%s",
						trial, step, cp, gotExp, wantExp)
				}
			}

			gotBreaches := c.Breaches()
			wantBreaches := m.breaches()
			if len(gotBreaches) != len(wantBreaches) {
				t.Fatalf("trial %d step %d: %d breaches, model has %d",
					trial, step, len(gotBreaches), len(wantBreaches))
			}
			for i, wb := range wantBreaches {
				gb := gotBreaches[i]
				if string(gb.Counterparty) != wb.name ||
					gb.Exposure.Cmp(wb.exp) != 0 || gb.Seq != wb.seq {
					t.Fatalf("trial %d step %d: breach[%d]=(%s,%s,%d), model=(%s,%s,%d)",
						trial, step, i, gb.Counterparty, gb.Exposure, gb.Seq,
						wb.name, wb.exp, wb.seq)
				}
			}
		}
	}
}
