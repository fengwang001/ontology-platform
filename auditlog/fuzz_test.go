package auditlog

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

type simOp struct {
	kind string // "A", "R", "S"
	ts   int64
	data string
	err  string
}

// buildScenario replays a random valid-ish write sequence against the real
// log while independently tracking (index, key) so checkpoints can be minted.
func buildScenario(rng *rand.Rand, cap int) (*Log, []simOp, map[int64]uint64, []*Entry) {
	k0 := rng.Uint64() % 5000
	l, err := New(k0, cap, 8, linearEvolve, linearRekey, linearMac)
	if err != nil {
		panic(err)
	}
	var ops []simOp
	ts := int64(0)
	maxOps := rng.Intn(cap-1) + 1 // leave room; may seal or not
	sealed := false
	for n := 0; n < maxOps && !sealed; n++ {
		ts += int64(rng.Intn(3)) // nondecreasing, sometimes equal
		roll := rng.Intn(100)
		var op simOp
		switch {
		case roll < 55:
			d := string(rune('a' + rng.Intn(6)))
			op = simOp{kind: "A", ts: ts, data: d,
				err: errName(l.Append(ts, []byte(d)))}
		case roll < 80:
			op = simOp{kind: "R", ts: ts, err: errName(l.Rekey(ts))}
		default:
			op = simOp{kind: "S", ts: ts, err: errName(l.Seal(ts))}
			if op.err == "" {
				sealed = true
			}
		}
		ops = append(ops, op)
	}
	// Recompute honest key-at-index independently from k0 and the real
	// entries, and cross-check every pre-seal Export along the way.
	es := l.Entries()
	honest := map[int64]uint64{0: k0}
	k := k0
	for _, e := range es {
		if e.Typ == TypRekey {
			k = linearRekey(k, e.Index)
		} else {
			k = linearEvolve(k)
		}
		honest[e.Index+1] = k
	}
	if !sealed {
		if i, got, err := l.Export(); err != nil || i != int64(len(es)) || got != honest[i] {
			panic(fmt.Sprintf("export mismatch i=%d got=%d want=%d err=%v", i, got, honest[i], err))
		}
	}
	return l, ops, honest, es
}

func errName(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidArg:
		return "arg"
	case ErrSealed:
		return "sealed"
	case ErrTimeRollback:
		return "rollback"
	case ErrCapFull:
		return "full"
	default:
		return "other"
	}
}

// mutateEntries returns a corrupted copy. j0 is known so the caller can
// choose whether mutations land inside the verifiable segment. honest maps
// index to the key *before* that index and is used for keyed forgeries.
func mutateEntries(rng *rand.Rand, src []*Entry, j0 int64, honest map[int64]uint64) (out []*Entry, desc string) {
	out = cloneEntries(src)
	if len(out) == 0 {
		return out, "none"
	}
	// Choose a target in [j0, len) when possible, else anywhere.
	lo := int(j0)
	if lo > len(out)-1 {
		lo = 0
	}
	t := lo + rng.Intn(len(out)-lo)
	e := out[t]
	switch rng.Intn(9) {
	case 0:
		e.Tag ^= 1
		desc = fmt.Sprintf("flip-tag@%d", t)
	case 1:
		if len(e.Data) > 0 {
			e.Data[0]++
		} else {
			e.Data = []byte("z")
		}
		desc = fmt.Sprintf("flip-data@%d", t)
	case 2:
		e.Ts++ // could stay ordered; harmless if so
		desc = fmt.Sprintf("bump-ts@%d", t)
	case 3:
		e.Ts-- // rollback relative to previous (except at j0)
		desc = fmt.Sprintf("drop-ts@%d", t)
	case 4:
		e.Typ = int32(9)
		desc = fmt.Sprintf("badtyp@%d", t)
	case 5:
		e.Index += int64(2 + rng.Intn(2))
		desc = fmt.Sprintf("gap@%d->%d", t, e.Index)
	case 6:
		e.Index -= int64(1 + rng.Intn(2))
		if e.Index < 0 {
			e.Index = 0
		}
		desc = fmt.Sprintf("replay@%d->%d", t, e.Index)
	case 7:
		// Forge a consistent tag over wrong seal/rekey payload: tag passes,
		// payload-decimal check fails -> content_mismatch.
		if e.Typ != TypSeal && e.Typ != TypRekey {
			e.Tag ^= 1
			desc = fmt.Sprintf("flip-tag@%d", t)
		} else {
			e.Data = []byte(strconv.FormatInt(e.Index+1, 10))
			e.Tag = linearMac(honest[e.Index], e.Index, e.Typ, e.Ts, e.Data)
			desc = fmt.Sprintf("wrong-payload@%d", t)
		}
	default:
		// Append a syntactically valid data record after a seal somewhere in
		// the verifiable part -> append_after_seal.
		// Honest/truncated logs only end with a seal; pick it when present.
		sealPos := len(out) - 1
		if len(out) > 0 && out[sealPos].Typ == TypSeal && sealPos >= lo {
			next := int64(sealPos + 1)
			data := []byte("post-seal")
			out = out[:sealPos+1]
			out = append(out, &Entry{
				Index: next,
				Typ:   TypData,
				Ts:    out[sealPos].Ts,
				Data:  data,
				Tag:   linearMac(honest[next], next, TypData, out[sealPos].Ts, data),
			})
			desc = fmt.Sprintf("append-after-seal@%d", next)
		} else {
			e.Tag ^= 1
			desc = fmt.Sprintf("flip-tag@%d", t)
		}
	}
	return out, desc
}

func pickCheckpoints(rng *rand.Rand, n int, honest map[int64]uint64, finalM int) []Checkpoint {
	// Candidate indices: honest keys plus a couple beyond the end.
	var idxs []int64
	for i := range honest {
		idxs = append(idxs, i)
	}
	// Dedup-free: honest map is already unique.
	k := 1 + rng.Intn(3)
	used := map[int64]bool{}
	var cps []Checkpoint
	for tries := 0; len(cps) < k && tries < 20; tries++ {
		var idx int64
		if rng.Intn(5) == 0 && finalM > 0 {
			idx = int64(finalM + rng.Intn(3)) // beyond end -> truncation/conflict
		} else {
			idx = idxs[rng.Intn(len(idxs))]
		}
		if used[idx] {
			continue
		}
		used[idx] = true
		key := honest[idx]
		if rng.Intn(8) == 0 {
			key++ // deliberately wrong -> conflict
		}
		cps = append(cps, Checkpoint{Index: idx, Key: key})
	}
	if len(cps) == 0 {
		cps = []Checkpoint{{0, honest[0]}}
	}
	return cps
}

func cpString(cps []Checkpoint) string {
	s := "{"
	for i, c := range cps {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("(%d,%d)", c.Index, c.Key)
	}
	return s + "}"
}

func TestDifferentialAgainstNaive2000(t *testing.T) {
	root := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 2000; iter++ {
		rng := rand.New(rand.NewSource(root.Int63()))
		cap := 2 + rng.Intn(14)
		l, ops, honest, es := buildScenario(rng, cap)

		presented := cloneEntries(es)
		desc := "clean"
		// Decide checkpoints first so mutations can target the verified part.
		cps := pickCheckpoints(rng, cap, honest, len(presented))
		j0 := cps[0].Index
		for _, c := range cps {
			if c.Index < j0 {
				j0 = c.Index
			}
		}
		// Truncate the tail sometimes.
		if len(presented) > 0 && rng.Intn(3) == 0 {
			cut := rng.Intn(len(presented) + 1)
			presented = presented[:cut]
			desc = fmt.Sprintf("truncate->%d", cut)
		}
		if rng.Intn(2) == 0 {
			presented, desc = mutateEntries(rng, presented, j0, honest)
		}

		got, gerr := l.Verify(presented, cps)
		want, werr := naiveVerify(presented, cps, linearEvolve, linearRekey, linearMac)

		if testing.Verbose() {
			fmt.Printf("case %4d: ops=%v m=%d cps=%s mutation=%s => kind=%s pos=%d ver=%d u=%d ev=%d rk=%d sealed=%t err=%v\n",
				iter, ops, len(presented), cpString(cps), desc,
				got.Kind, got.Pos, got.Verified, got.Unverifiable,
				got.EvolveCalls, got.RekeyCalls, got.Sealed, gerr)
		}

		if (gerr == nil) != (werr == nil) || gerr != werr {
			t.Fatalf("case %d error mismatch: got %v want %v\nops=%v cps=%s mut=%s",
				iter, gerr, werr, ops, cpString(cps), desc)
		}
		if gerr != nil {
			continue
		}
		if got != want {
			t.Fatalf("case %d report mismatch:\n got=%+v\nwant=%+v\nops=%v\ncps=%s\nmut=%s\nm=%d",
				iter, got, want, ops, cpString(cps), desc, len(presented))
		}
		// Invariant: evolve/rekey counts equal verified entry-type counts.
		var nRk int64
		for i := j0; i < int64(len(presented)) && i-j0 < got.Verified; i++ {
			if presented[i].Typ == TypRekey {
				nRk++
			}
		}
		if got.RekeyCalls != nRk || got.EvolveCalls != got.Verified-nRk {
			t.Fatalf("case %d counter invariant broken: %+v nRk=%d", iter, got, nRk)
		}
		if got.Unverifiable != min64(j0, int64(len(presented))) {
			t.Fatalf("case %d unverifiable wrong: %+v", iter, got)
		}
	}
}
