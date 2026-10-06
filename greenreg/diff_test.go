package greenreg

import (
	"fmt"
	"math/rand"
	"testing"
)

type refModel interface {
	RegisterFacility(id, holder string, start int64) error
	SetTermination(id string, end int64) error
	RegisterGeneration(id string, period, qty int64) (issued, revoked []int64, err error)
	Transfer(from, to string, serials []int64) error
	RegisterUsage(user string, period, qty int64) error
	Retire(user string, period int64, serials []int64) error
	Snapshot() string
}

var _ refModel = (*Registry)(nil)
var _ refModel = (*naiveModel)(nil)

type opResult struct {
	code    ErrCode
	fail    int64
	hasFail bool
	issued  []int64
	revoked []int64
}

type op struct {
	name string
	run  func(m refModel) opResult
	text string
}

func resFromErr(err error) opResult {
	r := opResult{}
	if err != nil {
		var re *RegistryError
		if asErr(err, &re) {
			r.code = re.Code
			if re.Fail != nil {
				r.fail, r.hasFail = re.Fail.Cert, true
			}
		}
	}
	return r
}

func asErr(err error, target **RegistryError) bool {
	if err == nil {
		return false
	}
	if re, ok := err.(*RegistryError); ok {
		*target = re
		return true
	}
	return false
}

func TestRandomDifferential(t *testing.T) {
	const (
		seedCount = 200
		maxOps    = 200
	)
	var failLog string
	for seed := int64(0); seed < seedCount; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{UnitQty: int64(1 + rng.Intn(4)), MaxAgePeriods: int64(rng.Intn(4))}
		a := New(cfg)
		b := NewNaive(cfg)
		logBuf := fmt.Sprintf("seed=%d cfg=%+v\n", seed, cfg)

		var facs []string
		var users = []string{"u1", "u2", "u3"}

		nFac := 1 + rng.Intn(3)
		for i := 0; i < nFac; i++ {
			id := fmt.Sprintf("F%d", i)
			start := int64(rng.Intn(3))
			holder := "h" + id
			facs = append(facs, id)
			ra := resFromErr(a.RegisterFacility(id, holder, start))
			rb := resFromErr(b.RegisterFacility(id, holder, start))
			logBuf += fmt.Sprintf("fac %s holder=%s start=%d -> %v\n", id, holder, start, ra.code)
			if !sameResult(ra, rb) {
				t.Fatalf("seed %d facility mismatch code a=%d b=%d\n%s", seed, ra.code, rb.code, logBuf)
			}
		}

		for step := 0; step < maxOps; step++ {
			o := randomOp(rng, facs, users)
			logBuf += "OP " + o.text + "\n"
			ra := o.run(a)
			rb := o.run(b)
			logBuf += fmt.Sprintf("  prod: code=%d fail=%d(%v) issued=%v revoked=%v\n",
				ra.code, ra.fail, ra.hasFail, ra.issued, ra.revoked)
			logBuf += fmt.Sprintf("  naive:code=%d fail=%d(%v) issued=%v revoked=%v\n",
				rb.code, rb.fail, rb.hasFail, rb.issued, rb.revoked)
			if !sameResult(ra, rb) {
				t.Fatalf("seed %d step %d result mismatch on %q\n%s\nPROD SNAP:\n%s\nNAIVE SNAP:\n%s",
					seed, step, o.text, logBuf, a.Snapshot(), b.Snapshot())
			}
			if ra.code == 0 && a.Snapshot() != b.Snapshot() {
				t.Fatalf("seed %d step %d snapshot mismatch on %q\n%s\nPROD:\n%s\nNAIVE:\n%s",
					seed, step, o.text, logBuf, a.Snapshot(), b.Snapshot())
			}
		}
		if a.Snapshot() != b.Snapshot() {
			t.Fatalf("seed %d final snapshot mismatch\n%s\nPROD:\n%s\nNAIVE:\n%s",
				seed, logBuf, a.Snapshot(), b.Snapshot())
		}
		failLog = logBuf
	}
	// Print one full trace to demonstrate the input/decision/output log.
	t.Log("\n" + failLog)
}

func sameResult(a, b opResult) bool {
	if a.code != b.code || a.hasFail != b.hasFail || (a.hasFail && a.fail != b.fail) {
		return false
	}
	return eqInt64(a.issued, b.issued) && eqInt64(a.revoked, b.revoked)
}

func eqInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pickCert(rng *rand.Rand, maxSerial int64) int64 {
	// bias toward real serials; occasionally produce invalid ones
	if rng.Intn(8) == 0 {
		return int64(rng.Intn(20)) + 1
	}
	if maxSerial <= 0 {
		return int64(rng.Intn(5)) + 1
	}
	return int64(rng.Intn(int(maxSerial))) + 1
}

func randomBatch(rng *rand.Rand, maxSerial int64) []int64 {
	n := 1 + rng.Intn(3)
	out := make([]int64, n)
	for i := range out {
		out[i] = pickCert(rng, maxSerial)
	}
	return out
}

func randomOp(rng *rand.Rand, facs, users []string) op {
	id := facs[rng.Intn(len(facs))]
	user := users[rng.Intn(len(users))]
	period := int64(rng.Intn(7))
	unit := int64(1 + rng.Intn(4))
	maxSerial := int64(50)
	switch rng.Intn(7) {
	case 0:
		q := int64(1 + rng.Intn(12))
		text := fmt.Sprintf("RegisterGeneration %s p=%d q=%d", id, period, q)
		return op{text: text, run: func(m refModel) opResult {
			is, rv, err := m.RegisterGeneration(id, period, q)
			r := resFromErr(err)
			r.issued, r.revoked = is, rv
			return r
		}}
	case 1:
		var end int64
		if rng.Intn(3) != 0 {
			end = period + 1
		}
		text := fmt.Sprintf("SetTermination %s end=%d", id, end)
		return op{text: text, run: func(m refModel) opResult { return resFromErr(m.SetTermination(id, end)) }}
	case 2:
		batch := randomBatch(rng, maxSerial)
		to := users[rng.Intn(len(users))]
		if rng.Intn(5) == 0 {
			to = "h" + id
		}
		text := fmt.Sprintf("Transfer h%s -> %s certs=%v", id, to, batch)
		from := "h" + id
		return op{text: text, run: func(m refModel) opResult { return resFromErr(m.Transfer(from, to, batch)) }}
	case 3:
		q := int64(rng.Intn(10))
		text := fmt.Sprintf("RegisterUsage %s p=%d q=%d", user, period, q)
		return op{text: text, run: func(m refModel) opResult { return resFromErr(m.RegisterUsage(user, period, q)) }}
	case 4:
		batch := randomBatch(rng, maxSerial)
		text := fmt.Sprintf("Retire %s p=%d certs=%v", user, period, batch)
		return op{text: text, run: func(m refModel) opResult { return resFromErr(m.Retire(user, period, batch)) }}
	case 5:
		// transfers among users to create diverse holdings
		batch := randomBatch(rng, maxSerial)
		from := users[rng.Intn(len(users))]
		to := users[rng.Intn(len(users))]
		text := fmt.Sprintf("Transfer %s -> %s certs=%v", from, to, batch)
		return op{text: text, run: func(m refModel) opResult { return resFromErr(m.Transfer(from, to, batch)) }}
	default:
		q := int64(1+rng.Intn(10)) * unit
		text := fmt.Sprintf("RegisterGeneration %s p=%d q=%d", id, period, q)
		return op{text: text, run: func(m refModel) opResult {
			is, rv, err := m.RegisterGeneration(id, period, q)
			r := resFromErr(err)
			r.issued, r.revoked = is, rv
			return r
		}}
	}
}
