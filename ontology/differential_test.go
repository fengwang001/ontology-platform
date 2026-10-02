package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

type diffGen struct {
	rng    *rand.Rand
	cfg    Config
	tokens []*Token
}

var diffCaveatPool = []string{
	"exp:50",
	"exp:100",
	"exp:200",
	"exp:1000",
	"amt:0",
	"amt:1",
	"amt:10",
	"amt:1000000000000",
	"ops:read",
	"ops:read,write",
	"ops:list-1,x9",
	"res:/",
	"res:/a",
	"res:/a/b",
	"res:/ab",
	// unknown / malformed only enter via Attenuate
	"team:alpha",
	"exp:01",
	"res:a",
	"ops:A",
	"amt:",
}

func (g *diffGen) mintCaveats() [][]byte {
	n := g.rng.Intn(4)
	var out [][]byte
	for i := 0; i < n; i++ {
		c := diffCaveatPool[g.rng.Intn(15)] // only well-formed known for Mint
		out = append(out, []byte(c))
	}
	return out
}

func (g *diffGen) anyCaveat() []byte {
	return []byte(diffCaveatPool[g.rng.Intn(len(diffCaveatPool))])
}

func TestDifferentialRandom(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const sequences = 2000
	const maxOps = 24
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*1_000_003 + 7)
		g := &diffGen{rng: rand.New(rand.NewSource(seed))}
		g.cfg = Config{
			K:   []byte(fmt.Sprintf("k-%d", seq)),
			Mac: testMac,
			Cm:  6,
			Lc:  64,
			Rm:  1 + g.rng.Intn(6),
		}
		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d cfg=%+v\n", seq, g.cfg)

		svc, err := New(g.cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		nav := &naiveModel{cfg: g.cfg}

		var tokens []*Token
		var navTokens []*Token
		now := int64(0)
		nOps := 4 + g.rng.Intn(maxOps)
		for step := 0; step < nOps; step++ {
			// now: usually nondecreasing, occasionally a rollback.
			switch g.rng.Intn(6) {
			case 0:
				if now > 0 {
					now -= int64(g.rng.Intn(3))
					if now < 0 {
						now = 0
					}
				}
			default:
				now += int64(g.rng.Intn(12))
				if now > 1e15 {
					now = 1e15
				}
			}

			choice := g.rng.Intn(10)
			switch {
			case choice < 2 || len(tokens) == 0:
				id := []byte(fmt.Sprintf("id-%d-%d", seq, g.rng.Intn(4)))
				cavs := g.mintCaveats()
				fmt.Fprintf(&log, "step=%d Mint(id=%q cavs=%q now=%d)\n", step, id, cavs, now)
				rt, rerr := svc.Mint(id, cavs, now)
				nt, no := nav.mint(id, cavs, now)
				fmt.Fprintf(&log, "  -> mint %s\n", verdictLine(rerr, no))
				compareError(t, &log, "Mint", rerr, no.ok, no.errKind, 0, 0, 0)
				compareCounters(t, &log, svc, nav, "Mint")
				if (rerr != nil) == no.ok {
					t.Fatalf("%s", diffFail(&log, "Mint error mismatch %v vs %d", rerr, no.errKind))
				}
				if rerr == nil {
					if !tokenEqual(rt, nt) {
						t.Fatalf("%s", diffFail(&log, "Mint token mismatch real=%q naive=%q", rt.Sig, nt.Sig))
					}
					tokens = append(tokens, cloneToken(rt))
					navTokens = append(navTokens, cloneToken(nt))
				}
			case choice < 5:
				ti := g.rng.Intn(len(tokens))
				src := tokens[ti]
				nsrc := navTokens[ti]
				cav := g.anyCaveat()
				fmt.Fprintf(&log, "step=%d Attenuate(cav=%q n=%d)\n", step, cav, len(src.Caveats))
				ResetAttenuateCounter()
				rt, rerr := Attenuate(g.cfg, src, cav)
				// Naive attenuate mirrors the pure function rules.
				nt, nerr := naiveAttenuate(g.cfg, nsrc, cav)
				if nerr != nil {
					fmt.Fprintf(&log, "  -> attenuate rejected kind=%d\n", asErr(t, nerr).Kind)
				} else {
					fmt.Fprintf(&log, "  -> attenuate sig=%x\n", nt.Sig)
				}
				compareAttenuate(t, &log, rerr, nerr, rt, nt)
				if rerr == nil {
					tokens = append(tokens, cloneToken(rt))
					navTokens = append(navTokens, cloneToken(nt))
				}
			case choice < 8:
				ti := g.rng.Intn(len(tokens))
				tok := tokens[ti]
				ntok := navTokens[ti]
				req := randomRequest(g)
				fmt.Fprintf(&log, "step=%d Verify(cavs=%q op=%s res=%s amt=%d now=%d)\n",
					step, tok.Caveats, req.Op, req.Res, req.Amount, now)
				rerr := svc.Verify(tok, req, now)
				no := nav.verify(ntok, req, now)
				fmt.Fprintf(&log, "  -> verify %s\n", verdictLine(rerr, no))
				compareError(t, &log, "Verify", rerr, no.ok, no.errKind, no.prefix, no.cavIndex, no.cavProblem)
				compareCounters(t, &log, svc, nav, "Verify")
			default:
				ti := g.rng.Intn(len(tokens))
				tok := tokens[ti]
				ntok := navTokens[ti]
				k := g.rng.Intn(len(tok.Caveats) + 1)
				if g.rng.Intn(8) == 0 {
					k = len(tok.Caveats) + 1 // just out of range sometimes
				}
				fmt.Fprintf(&log, "step=%d Revoke(k=%d now=%d)\n", step, k, now)
				rr, rerr := svc.Revoke(tok, k, now)
				nrev, nexp, no := nav.revoke(ntok, k, now)
				if rerr != nil {
					fmt.Fprintf(&log, "  -> revoke rejected kind=%d\n", asErr(t, rerr).Kind)
				} else {
					fmt.Fprintf(&log, "  -> revoke revoked=%v expired=%v\n", rr.Revoked, rr.Expired)
				}
				compareError(t, &log, "Reject", rerr, no.ok, no.errKind, 0, 0, 0)
				compareCounters(t, &log, svc, nav, "Revoke")
				if rerr == nil {
					if rr.Revoked != nrev || rr.Expired != nexp {
						t.Fatalf("%s", diffFail(&log, "Revoke result real=%+v naive rev=%v exp=%v", rr, nrev, nexp))
					}
				}
			}
			// Cross-check active record count every step.
			st := svc.Snapshot()
			if st.ActiveRecords != len(nav.records) {
				t.Fatalf("%s", diffFail(&log, "active records real=%d naive=%d", st.ActiveRecords, len(nav.records)))
			}
			if st.Clock != nav.clock || st.ClockSet != nav.clockSet {
				t.Fatalf("%s", diffFail(&log, "clock real=(%d,%v) naive=(%d,%v)",
					st.Clock, st.ClockSet, nav.clock, nav.clockSet))
			}
		}
		// Under -v the first sequence is printed: inputs, outputs and the
		// reported basis for each decision.
		if seq == 0 && testing.Verbose() {
			t.Log(log.String())
		}
	}
}

func randomRequest(g *diffGen) Request {
	ops := []string{"read", "write", "list-1", "x9", "admin"}
	reses := []string{"/", "/a", "/a/b", "/ab", "/x", "/a/b/c"}
	return Request{
		Op:     ops[g.rng.Intn(len(ops))],
		Res:    reses[g.rng.Intn(len(reses))],
		Amount: int64(g.rng.Intn(13)),
	}
}

func naiveAttenuate(cfg Config, tok *Token, caveat []byte) (*Token, error) {
	if !validConfig(cfg) || tok == nil || len(tok.Sig) == 0 ||
		len(caveat) < 1 || len(caveat) > cfg.Lc {
		return nil, &Error{Kind: RejectInvalid}
	}
	if len(tok.Caveats) >= cfg.Cm {
		return nil, &Error{Kind: RejectLimit}
	}
	sig := cfg.Mac(tok.Sig, caveat)
	cavs := make([][]byte, 0, len(tok.Caveats)+1)
	cavs = append(cavs, tok.Caveats...)
	cavs = append(cavs, caveat)
	return &Token{ID: tok.ID, Caveats: cavs, Sig: sig}, nil
}

func compareAttenuate(t *testing.T, log *strings.Builder, rerr, nerr error, rt, nt *Token) {
	t.Helper()
	if (rerr == nil) != (nerr == nil) {
		t.Fatalf("%s", diffFail(log, "Attenuate error mismatch %v vs %v", rerr, nerr))
	}
	if rerr != nil {
		re := asErr(t, rerr)
		ne := asErr(t, nerr)
		if re.Kind != ne.Kind {
			t.Fatalf("%s", diffFail(log, "Attenuate kind mismatch %d vs %d", re.Kind, ne.Kind))
		}
		return
	}
	if !tokenEqual(rt, nt) {
		t.Fatalf("%s", diffFail(log, "Attenuate token mismatch %q vs %q", rt.Sig, nt.Sig))
	}
}

func compareError(t *testing.T, log *strings.Builder, op string, rerr error,
	nok bool, nkind RejectKind, nprefix, nindex int, nproblem CaveatProblem) {
	t.Helper()
	if (rerr == nil) != nok {
		t.Fatalf("%s", diffFail(log, "%s error presence mismatch real=%v naive kind=%d", op, rerr, nkind))
	}
	if rerr == nil {
		return
	}
	re := asErr(t, rerr)
	if re.Kind != nkind || re.Prefix != nprefix || re.Index != nindex || re.Problem != nproblem {
		t.Fatalf("%s", diffFail(log, "%s mismatch real=(k=%d j=%d i=%d p=%d) naive=(k=%d j=%d i=%d p=%d)",
			op, re.Kind, re.Prefix, re.Index, re.Problem, nkind, nprefix, nindex, nproblem))
	}
}

func compareCounters(t *testing.T, log *strings.Builder, svc *Service, nav *naiveModel, op string) {
	t.Helper()
	st := svc.Snapshot()
	if st.MacCalls != nav.macCalls {
		t.Fatalf("%s", diffFail(log, "%s macCalls real=%d naive=%d", op, st.MacCalls, nav.macCalls))
	}
	if st.RevocationLookups != nav.queries {
		t.Fatalf("%s", diffFail(log, "%s queries real=%d naive=%d", op, st.RevocationLookups, nav.queries))
	}
	if st.HeapPops != nav.pops {
		t.Fatalf("%s", diffFail(log, "%s pops real=%d naive=%d", op, st.HeapPops, nav.pops))
	}
}

func tokenEqual(a, b *Token) bool {
	if !bytesEqual(a.ID, b.ID) || !bytesEqual(a.Sig, b.Sig) || len(a.Caveats) != len(b.Caveats) {
		return false
	}
	for i := range a.Caveats {
		if !bytesEqual(a.Caveats[i], b.Caveats[i]) {
			return false
		}
	}
	return true
}

func cloneToken(t *Token) *Token {
	c := &Token{ID: append([]byte(nil), t.ID...), Sig: append([]byte(nil), t.Sig...)}
	for _, cav := range t.Caveats {
		c.Caveats = append(c.Caveats, append([]byte(nil), cav...))
	}
	return c
}

func diffFail(log *strings.Builder, format string, args ...any) string {
	return log.String() + fmt.Sprintf(format, args...)
}

func verdictLine(rerr error, no naiveOutcome) string {
	if rerr == nil {
		return "ok"
	}
	re := rerr.(*Error)
	switch re.Kind {
	case RejectCaveat:
		return fmt.Sprintf("reject kind=caveat index=%d problem=%d basis=%s",
			re.Index, re.Problem, caveatBasis(re))
	case RejectRevoked:
		return fmt.Sprintf("reject kind=revoked prefix=%d basis=table hit on s_j", re.Prefix)
	default:
		return fmt.Sprintf("reject kind=%d basis=%s", re.Kind, kindBasis(re.Kind))
	}
}

func kindBasis(k RejectKind) string {
	switch k {
	case RejectInvalid:
		return "parameter or structure validation"
	case RejectClockRollback:
		return "now < previously accepted maximum now"
	case RejectBadSignature:
		return "recomputed s0..sn != Sig"
	case RejectLimit:
		return "live record count would exceed Rm"
	default:
		return "unknown"
	}
}

func caveatBasis(e *Error) string {
	switch e.Problem {
	case CaveatUnknown:
		return "prefix not in exp/ops/res/amt"
	case CaveatMalformed:
		return "known prefix with value violating grammar"
	default:
		return "known well-formed caveat not satisfied by request"
	}
}

// TestDeterministicReplay runs one fixed sequence twice and compares every
// resulting token byte, decision, record count and reclamation count.
func TestDeterministicReplay(t *testing.T) {
	type entry struct {
		sig     []byte
		errKind RejectKind
		prefix  int
		index   int
		problem CaveatProblem
		expired bool
		revoked bool
		st      Stats
	}
	run := func() []entry {
		cfg := Config{K: []byte("replay"), Mac: testMac, Cm: 8, Lc: 64, Rm: 3}
		s := mustNew(t, cfg)
		var out []entry
		t0, err := s.Mint([]byte("r1"), [][]byte{[]byte("ops:read"), []byte("exp:50")}, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{sig: t0.Sig, st: s.Snapshot()})
		t1, err := Attenuate(cfg, t0, []byte("res:/a"))
		if err != nil {
			t.Fatal(err)
		}
		rr, err := s.Revoke(t1, 1, 10)
		if err != nil || !rr.Revoked {
			t.Fatalf("revoke: %v %+v", err, rr)
		}
		e1 := asErr(t, s.Verify(t1, Request{Op: "read", Res: "/a", Amount: 0}, 15))
		out = append(out, entry{errKind: e1.Kind, prefix: e1.Prefix, st: s.Snapshot()})
		rr, err = s.Revoke(t1, 4, 20)
		if err == nil {
			t.Fatalf("k out of range should fail, got rr=%+v", rr)
		}
		if e := asErr(t, err); e.Kind != RejectInvalid {
			t.Fatalf("k out of range: %v", err)
		}
		e2 := asErr(t, s.Verify(t1, Request{Op: "read", Res: "/a", Amount: 0}, 55))
		out = append(out, entry{errKind: e2.Kind, index: e2.Index, problem: e2.Problem, st: s.Snapshot()})
		t2, err := s.Mint([]byte("r2"), nil, 60)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{sig: t2.Sig, st: s.Snapshot()})
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatal("replay length differs")
	}
	for i := range a {
		if !bytesEqual(a[i].sig, b[i].sig) ||
			a[i].errKind != b[i].errKind || a[i].prefix != b[i].prefix ||
			a[i].index != b[i].index || a[i].problem != b[i].problem ||
			a[i].expired != b[i].expired || a[i].revoked != b[i].revoked ||
			a[i].st != b[i].st {
			t.Fatalf("replay differs at %d:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

// TestConcurrentSafety hammers a shared service; results must be equivalent
// to some serial order and the heap invariant must stay intact.
func TestConcurrentSafety(t *testing.T) {
	cfg := Config{K: []byte("c"), Mac: testMac, Cm: 8, Lc: 64, Rm: 50}
	s := mustNew(t, cfg)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := int64(w*1000 + i)
				tok, _ := s.Mint([]byte{byte('a' + w), byte(i)}, [][]byte{[]byte("exp:100000")}, now)
				if tok != nil {
					t1, aerr := Attenuate(cfg, tok, []byte("ops:read"))
					if aerr == nil {
						_, _ = s.Revoke(t1, 1, now+1)
						_ = s.Verify(t1, Request{Op: "read", Res: "/", Amount: 0}, now+2)
					}
				}
			}
		}()
	}
	wg.Wait()
	// Heap ordering must remain consistent after concurrent use.
	s.mu.Lock()
	for i := 1; i < len(s.heap); i++ {
		if s.heap[0].bound > s.heap[i].bound {
			t.Fatalf("heap invariant broken: %d > %d", s.heap[0].bound, s.heap[i].bound)
		}
	}
	s.mu.Unlock()
}
