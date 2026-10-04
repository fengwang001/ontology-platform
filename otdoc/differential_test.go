package otdoc

import (
	"math/rand"
	"testing"
)

const (
	diffAlphabet = "abX你好😀 "
	diffTrials   = 2000
	diffActions  = 16
)

func randomText(rng *rand.Rand, n int) string {
	rs := []rune(diffAlphabet)
	out := make([]rune, n)
	for k := range out {
		out[k] = rs[rng.Intn(len(rs))]
	}
	return string(out)
}

func randomOp(rng *rand.Rand, docLen int) Op {
	if docLen == 0 {
		op, err := normalize([]Comp{{Kind: Insert, Text: randomText(rng, rng.Intn(3)+1)}})
		if err != nil {
			panic(err)
		}
		return op
	}
	var raw []Comp
	pos := 0
	for pos < docLen {
		if pos > 0 && rng.Intn(3) == 0 {
			raw = append(raw, Comp{Kind: Insert, Text: randomText(rng, rng.Intn(2)+1)})
		}
		rem := docLen - pos
		n := rng.Intn(rem) + 1
		if rng.Intn(4) == 0 {
			raw = append(raw, Comp{Kind: Delete, N: n})
		} else {
			raw = append(raw, Comp{Kind: Retain, N: n})
		}
		pos += n
	}
	if rng.Intn(2) == 0 {
		raw = append(raw, Comp{Kind: Insert, Text: randomText(rng, rng.Intn(2)+1)})
	}
	op, err := normalize(raw)
	if err != nil {
		panic(err)
	}
	return op
}

type diffEntry struct {
	site    string
	seq     int
	baseRev int
	op      Op
	res     Result
	err     error
	verdict string
	wantOp  Op
}

// TestRandomDifferential replays 2000 random multi-site submission sequences
// against both Doc and the independent position-mapping reference transform,
// asserting identical revisions, documents, transformed ops, rejects and
// full history replay at every step. Every input/output/verdict is logged.
func TestRandomDifferential(t *testing.T) {
	sites := []string{"alice", "bob", "carol"}
	for trial := 0; trial < diffTrials; trial++ {
		rng := rand.New(rand.NewSource(int64(20261003 + trial)))
		maxLen := 1 + rng.Intn(12)
		doc, err := New(maxLen)
		if err != nil {
			t.Fatal(err)
		}
		floor := 0
		var refOps []Op
		refText := []string{""}
		lastSeq := map[string]int{}
		lastRes := map[string]Result{}
		var entries []diffEntry
		failed := false
		failf := func(format string, args ...any) {
			failed = true
			t.Errorf(format, args...)
		}

		for act := 0; act < diffActions; act++ {
			if rng.Intn(8) == 0 && doc.Rev() > floor {
				newFloor := floor + 1 + rng.Intn(doc.Rev()-floor)
				if err := doc.Compact(newFloor); err != nil {
					failf("trial %d compact: %v", trial, err)
				}
				drop := newFloor - floor
				refOps = append([]Op(nil), refOps[drop:]...)
				refText = append([]string(nil), refText[drop:]...)
				floor = newFloor
			}

			site := sites[rng.Intn(len(sites))]
			last := lastSeq[site]
			var seq int
			switch {
			case last > 0 && rng.Intn(5) == 0:
				seq = last
			case rng.Intn(7) == 5:
				seq = last + 2
			default:
				seq = last + 1
			}
			baseRev := floor + rng.Intn(len(refText))
			op := randomOp(rng, runeLen(refText[baseRev-floor]))
			res, subErr := doc.Submit(site, seq, baseRev, cloneOp(op))
			e := diffEntry{site: site, seq: seq, baseRev: baseRev,
				op: cloneOp(op), res: res, err: subErr}

			// ---- Independent reference classification ----
			switch {
			case last > 0 && seq == last:
				e.verdict = "duplicate"
			case (last == 0 && seq != 1) || seq > last+1 || seq < last:
				if seq <= last {
					e.verdict = "reject:stale"
				} else {
					e.verdict = "reject:gap"
				}
			default:
				d := refBuild(op, runeLen(refText[baseRev-floor]))
				chain := refOps[baseRev-floor:]
				for _, srv := range chain {
					d = refXform(srv, d)
				}
				wantOp := d.toOp()
				e.wantOp = wantOp
				headLen := runeLen(refText[len(refText)-1])
				switch {
				case resultLength(headLen, wantOp) > maxLen:
					e.verdict = "reject:toolarge"
				case isNoOp(wantOp):
					e.verdict = "accepted:noop"
				default:
					refText = append(refText, apply(refText[len(refText)-1], wantOp))
					refOps = append(refOps, wantOp)
					e.verdict = "accepted:applied"
				}
			}

			switch e.verdict {
			case "duplicate":
				rec := lastRes[site]
				if subErr != nil || res.Rev != rec.Rev ||
					res.Applied != rec.Applied || !opsEqual(res.Op, rec.Op) {
					failf("trial %d act %d duplicate %+v/%v vs %+v",
						trial, act, res, subErr, rec)
				}
			case "reject:gap":
				if subErr != ErrSeqGap {
					failf("trial %d act %d want ErrSeqGap got %v", trial, act, subErr)
				}
			case "reject:stale":
				if subErr != ErrStaleSeq {
					failf("trial %d act %d want ErrStaleSeq got %v", trial, act, subErr)
				}
			case "reject:toolarge":
				if subErr != ErrTooLarge {
					failf("trial %d act %d want ErrTooLarge got %v", trial, act, subErr)
				}
			case "accepted:noop":
				if subErr != nil || res.Applied || !opsEqual(res.Op, e.wantOp) {
					failf("trial %d act %d noop res=%+v err=%v want=%s",
						trial, act, res, subErr, opString(e.wantOp))
				}
				lastSeq[site] = seq
				lastRes[site] = res
			case "accepted:applied":
				if subErr != nil || !res.Applied || !opsEqual(res.Op, e.wantOp) {
					failf("trial %d act %d applied got %s/%v want %s in=%s",
						trial, act, opString(res.Op), subErr,
						opString(e.wantOp), opString(op))
				}
				if doc.Text() != refText[len(refText)-1] {
					failf("trial %d act %d text %q want %q",
						trial, act, doc.Text(), refText[len(refText)-1])
				}
				lastSeq[site] = seq
				lastRes[site] = res
			}
			entries = append(entries, e)
		}

		// Final cross-checks.
		if doc.Floor() != floor || doc.Rev() != floor+len(refOps) {
			failf("trial %d rev/floor: doc=%d/%d ref=%d/%d",
				trial, doc.Rev(), doc.Floor(), floor+len(refOps), floor)
		}
		if doc.Text() != refText[len(refText)-1] {
			failf("trial %d final text %q want %q",
				trial, doc.Text(), refText[len(refText)-1])
		}
		h, err := doc.History(floor, doc.Rev())
		if err != nil {
			failf("trial %d history: %v", trial, err)
		} else {
			replayed := refText[0]
			for _, hop := range h {
				if runeLen(replayed) != baseLength(hop) {
					failf("trial %d history base length mismatch: %s on %q",
						trial, opString(hop), replayed)
				}
				replayed = apply(replayed, hop)
			}
			if replayed != doc.Text() {
				failf("trial %d replay %q != %q", trial, replayed, doc.Text())
			}
		}
		// Log inputs, outputs and the basis of each judgment.
		t.Logf("[trial %d] maxLen=%d floor=%d rev=%d final=%q",
			trial, maxLen, doc.Floor(), doc.Rev(), doc.Text())
		for k, e := range entries {
			t.Logf("  #%d IN  site=%s seq=%d baseRev=%d op=%s",
				k, e.site, e.seq, e.baseRev, opString(e.op))
			if e.err != nil {
				t.Logf("      OUT err=%v verdict=%s", e.err, e.verdict)
			} else {
				t.Logf("      OUT rev=%d applied=%v op=%s verdict=%s",
					e.res.Rev, e.res.Applied, opString(e.res.Op), e.verdict)
			}
		}
		if failed {
			return
		}
	}
}
