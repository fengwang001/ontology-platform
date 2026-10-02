package fslog

import (
	"bytes"
	"sort"
	"strconv"
)

// Checkpoint is a (sequence number, key) pair held by a verifier.
type Checkpoint struct {
	Seq int64
	Key uint64
}

// Verdict classifies the outcome of Verify.
type Verdict int

const (
	VerdictOK Verdict = iota
	VerdictInvalidArg
	VerdictGap
	VerdictReplay
	VerdictAppendAfterSeal
	VerdictTimeRegression
	VerdictTamper
	VerdictContentMismatch
	VerdictCheckpointConflict
	VerdictTruncation
)

func (v Verdict) String() string {
	switch v {
	case VerdictOK:
		return "ok"
	case VerdictInvalidArg:
		return "invalid-arg"
	case VerdictGap:
		return "gap"
	case VerdictReplay:
		return "replay"
	case VerdictAppendAfterSeal:
		return "append-after-seal"
	case VerdictTimeRegression:
		return "time-regression"
	case VerdictTamper:
		return "tamper"
	case VerdictContentMismatch:
		return "content-mismatch"
	case VerdictCheckpointConflict:
		return "checkpoint-conflict"
	case VerdictTruncation:
		return "truncation"
	}
	return "unknown"
}

// Report is the outcome of a single one-pass verification.
type Report struct {
	Verdict      Verdict
	Pos          uint64
	Verified     uint64
	Unverifiable uint64
	EvolveCount  uint64
	RekeyCount   uint64
	Sealed       bool
}

// Verify checks entries against checkpoints in a single pass.
func Verify(entries []Entry, cps []Checkpoint, f Funcs) Report {
	if len(cps) == 0 || f.Evolve == nil || f.Rekey == nil || f.Mac == nil {
		return Report{Verdict: VerdictInvalidArg}
	}
	keys := make(map[int64]uint64, len(cps))
	seqs := make([]int64, 0, len(cps))
	for _, cp := range cps {
		if cp.Seq < 0 {
			return Report{Verdict: VerdictInvalidArg}
		}
		if _, dup := keys[cp.Seq]; dup {
			return Report{Verdict: VerdictInvalidArg}
		}
		keys[cp.Seq] = cp.Key
		seqs = append(seqs, cp.Seq)
	}
	sort.Slice(seqs, func(a, b int) bool { return seqs[a] < seqs[b] })

	m := uint64(len(entries))
	j0 := uint64(seqs[0])
	var rep Report
	rep.Unverifiable = j0
	if j0 > m {
		rep.Unverifiable = m
	}

	k := keys[seqs[0]]
	for t := j0; t < m; t++ {
		if t != j0 {
			if ck, ok := keys[int64(t)]; ok && ck != k {
				rep.Verdict = VerdictCheckpointConflict
				rep.Pos = t
				return rep
			}
		}
		e := entries[t]
		switch {
		case e.Index > t:
			rep.Verdict = VerdictGap
			rep.Pos = t
			return rep
		case e.Index < t:
			rep.Verdict = VerdictReplay
			rep.Pos = t
			return rep
		}
		if t > j0 {
			prev := entries[t-1]
			if prev.Typ == TypSeal {
				rep.Verdict = VerdictAppendAfterSeal
				rep.Pos = t
				return rep
			}
			if e.Ts < prev.Ts {
				rep.Verdict = VerdictTimeRegression
				rep.Pos = t
				return rep
			}
		}
		if e.Typ > TypRekey {
			rep.Verdict = VerdictTamper
			rep.Pos = t
			return rep
		}
		if e.Tag != f.Mac(k, t, e.Typ, e.Ts, e.Data) {
			rep.Verdict = VerdictTamper
			rep.Pos = t
			return rep
		}
		if e.Typ == TypSeal || e.Typ == TypRekey {
			if !bytes.Equal(e.Data, []byte(strconv.FormatUint(t, 10))) {
				rep.Verdict = VerdictContentMismatch
				rep.Pos = t
				return rep
			}
		}
		rep.Verified++
		if e.Typ == TypRekey {
			k = f.Rekey(k, t)
			rep.RekeyCount++
		} else {
			k = f.Evolve(k)
			rep.EvolveCount++
		}
	}
	if ck, ok := keys[int64(m)]; ok && ck != k {
		rep.Verdict = VerdictCheckpointConflict
		rep.Pos = m
		return rep
	}
	for _, s := range seqs {
		if uint64(s) > m {
			rep.Verdict = VerdictTruncation
			rep.Pos = m
			return rep
		}
	}
	rep.Verdict = VerdictOK
	rep.Sealed = m > 0 && j0 <= m-1 && entries[m-1].Typ == TypSeal
	return rep
}
