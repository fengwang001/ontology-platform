package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// randomText builds text from pools that exercise B, X, UCS2 BMP and
// supplementary code points, occasionally injecting invalid UTF-8.
func randomText(rng *rand.Rand) string {
	const poolB = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n.,!?:;-_@#%&*()'\"+=/<>$"
	const poolX = "{}[]~^|\\€"
	pools := []rune{}
	mode := rng.Intn(6)
	switch mode {
	case 0, 1:
		pools = []rune(poolB)
	case 2:
		pools = append([]rune(poolB), []rune(poolX)...)
	case 3:
		pools = append([]rune(poolB), '汉', '字', 'あ', 'é')
	case 4:
		pools = []rune("汉字あ😀€𝕏{}a")
	default:
		pools = []rune(poolB + poolX + "汉字é😀𝕏")
	}
	n := rng.Intn(600) + 1
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(pools[rng.Intn(len(pools))])
	}
	text := sb.String()
	// Roughly 3% of texts get corrupted with an invalid UTF-8 byte.
	if rng.Intn(33) == 0 && len(text) > 1 {
		b := []byte(text)
		b[rng.Intn(len(b))] = 0xFF
		return string(b)
	}
	return text
}

// TestNaiveComparison replays 2000 random texts and operation sequences and
// asserts the production meter matches the naive reference field by field.
// Each input, output and the basis for the verdict is logged.
func TestNaiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const groups = 2000

	for g := 0; g < groups; g++ {
		P := int64(1 + rng.Intn(200))
		T0 := int64(rng.Intn(6))
		p1 := int64(rng.Intn(20))
		p2 := int64(rng.Intn(20))
		MI := int64(100 + rng.Intn(901))
		m, err := NewMeter(P, T0, p1, p2, MI)
		if err != nil {
			t.Fatalf("group %d NewMeter: %v", g, err)
		}
		ref := newNaiveRef(P, T0, p1, p2, MI)

		// Text-only Split checks are independent of accounts.
		text := randomText(rng)
		t.Logf("g=%d OP=Split IN=%q", g, text)
		gotSplit, gotErr := m.Split(text)
		_, refErrCode := ref.quote("__split_probe__", text, false, 0)
		if gotErr != nil {
			code := string(gotErr.(*Error).Code)
			t.Logf("g=%d OUT=error(%s) VERDICT=%s", g, code, code)
		} else {
			refEnc, refUnits, _, _ := unitSeq(text)
			refSegs := naiveCut(refEnc, refUnits)
			if len(refSegs) > 10 {
				t.Fatalf("g=%d Split should have failed with >10 segments", g)
			}
			if gotSplit.Encoding != refEnc || len(gotSplit.Segments) != len(refSegs) {
				t.Fatalf("g=%d Split mismatch got=%+v ref enc=%s segs=%s",
					g, gotSplit, refEnc, segmentsString(refSegs))
			}
			for i := range refSegs {
				if gotSplit.Segments[i] != refSegs[i] {
					t.Fatalf("g=%d segment %d got=%+v ref=%+v",
						g, i, gotSplit.Segments[i], refSegs[i])
				}
			}
			t.Logf("g=%d OUT=%s %s VERDICT=matches naive unit-sequence cut",
				g, gotSplit.Encoding, segmentsString(gotSplit.Segments))
		}
		if gotErr != nil && refErrCode == "" {
			t.Fatalf("g=%d Split rejected but reference accepted: %v", g, gotErr)
		}

		// A small account-scoped operation sequence.
		const ops = 5
		account := fmt.Sprintf("acc%d", g)
		for opi := 0; opi < ops; opi++ {
			switch rng.Intn(10) {
			case 0, 1, 2:
				x := int64(1 + rng.Intn(5000))
				got := m.Deposit(account, x)
				want := ref.deposit(account, x)
				t.Logf("g=%d OP=Deposit IN=(%s,%d) OUT=%s VERDICT=%s",
					g, account, x, errLabel(got), errMatch(got, want))
				if (got == nil) != (want == nil) {
					t.Fatalf("g=%d Deposit(%d) got=%v ref=%v", g, x, got, want)
				}
			default:
				msg := randomText(rng)
				international := rng.Intn(2) == 0
				now := int64(rng.Intn(600))
				if rng.Intn(5) == 0 {
					now = int64(rng.Intn(300)) // possible clock skew
				}
				qres, qerr := m.Quote(account, msg, international, now)
				rres, rcode := ref.quote(account, msg, international, now)
				t.Logf("g=%d OP=Quote IN=(%s,intl=%v,now=%d,text=%q) OUT=%s VERDICT=%s",
					g, account, international, now, msg, quoteLabel(qres, qerr),
					quoteVerdict(qres, qerr, rres, rcode))
				if !sameOutcome(qres, qerr, rres, rcode) {
					t.Fatalf("g=%d Quote mismatch got=(%+v,%v) ref=(%+v,%s)",
						g, qres, qerr, rres, rcode)
				}

				sres, serr := m.Send(account, msg, international, now)
				nres, ncode := ref.send(account, msg, international, now)
				t.Logf("g=%d OP=Send IN=(%s,intl=%v,now=%d,text=%q) OUT=%s VERDICT=%s",
					g, account, international, now, msg, sendLabel(sres, serr),
					sendVerdict(sres, serr, nres, ncode))
				if !sameOutcome(sres, serr, nres, ncode) {
					t.Fatalf("g=%d Send mismatch got=(%+v,%v) ref=(%+v,%s)",
						g, sres, serr, nres, ncode)
				}
				assertModelState(t, g, m, ref, account)
			}
		}

		// Global invariant: deposits == balances + fees.
		refTotalBalance, refTotalDeposits, refTotalFees := int64(0), int64(0), int64(0)
		for _, acc := range ref.accounts {
			refTotalBalance += acc.balance
			refTotalDeposits += acc.totalDepos
			refTotalFees += acc.totalFeePaid
		}
		if refTotalBalance+refTotalFees != refTotalDeposits {
			t.Fatalf("g=%d reference invariant broken", g)
		}
	}
}

func errLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return "error(" + string(err.(*Error).Code) + ")"
}

func errMatch(got, want error) string {
	if (got == nil) == (want == nil) {
		return "matches reference"
	}
	return "MISMATCH"
}

func quoteLabel(r *SendResult, err error) string {
	if err != nil {
		return "error(" + string(err.(*Error).Code) + ")"
	}
	return fmt.Sprintf("{enc=%s,segs=%d,fee=%d}", r.Encoding, r.Segments, r.Fee)
}

func sendLabel(r *SendResult, err error) string { return quoteLabel(r, err) }

func quoteVerdict(r *SendResult, err error, rr *naiveResult, code string) string {
	if sameOutcome(r, err, rr, code) {
		return "matches reference; read-only"
	}
	return "MISMATCH"
}

func sendVerdict(r *SendResult, err error, rr *naiveResult, code string) string {
	if sameOutcome(r, err, rr, code) {
		return "matches reference; state applied"
	}
	return "MISMATCH"
}

func sameOutcome(r *SendResult, err error, rr *naiveResult, code string) bool {
	if err != nil {
		return string(err.(*Error).Code) == code
	}
	if code != "" || rr == nil {
		return false
	}
	return r.Encoding == rr.encoding && r.Segments == len(rr.segments) && r.Fee == rr.fee
}

func assertModelState(t *testing.T, g int, m *Meter, ref *naiveRef, account string) {
	t.Helper()
	got, ok1 := m.accounts[account]
	want, ok2 := ref.accounts[account]
	if ok1 != ok2 {
		t.Fatalf("g=%d account existence got=%v ref=%v", g, ok1, ok2)
	}
	if !ok1 {
		return
	}
	if got.balance != want.balance || got.k != want.k || got.u != want.u || got.t != want.t {
		t.Fatalf("g=%d state got=(bal=%d,k=%d,u=%d,t=%d) ref=(bal=%d,k=%d,u=%d,t=%d)",
			g, got.balance, got.k, got.u, got.t, want.balance, want.k, want.u, want.t)
	}
	if m.maxNow != ref.maxNow {
		t.Fatalf("g=%d maxNow got=%d ref=%d", g, m.maxNow, ref.maxNow)
	}
}
