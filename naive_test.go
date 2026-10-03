package ontology

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// naiveRef is an independent reference implementation. It rebuilds the
// per-code-point unit sequence explicitly, then cuts segments with the same
// greedy rule expressed as a list of (codePointIndex, units) pairs.
type naiveRef struct {
	p, t0, p1, p2, mi int64
	maxNow            int64
	accounts          map[string]*naiveAccount
}

type naiveAccount struct {
	balance      int64
	k, u, t      int64
	everSent     bool
	totalDepos   int64
	totalFeePaid int64
}

type naiveResult struct {
	encoding string
	segments []Segment
	fee      int64
}

func newNaiveRef(p, t0, p1, p2, mi int64) *naiveRef {
	return &naiveRef{p: p, t0: t0, p1: p1, p2: p2, mi: mi, accounts: map[string]*naiveAccount{}}
}

// unitSeq turns text into a unit per code point, explicitly choosing the
// encoding first and then materialising the whole sequence.
func unitSeq(text string) (string, []int, []rune, bool) {
	var runes []rune
	allGSM := true
	for _, r := range text {
		runes = append(runes, r)
		if !basicRune(r) && !extendedRune(r) {
			allGSM = false
		}
	}
	units := make([]int, len(runes))
	for i, r := range runes {
		switch {
		case allGSM && extendedRune(r):
			units[i] = 2
		case !allGSM && r > 0xFFFF:
			units[i] = 2
		default:
			units[i] = 1
		}
	}
	enc := EncodingUCS2
	if allGSM {
		enc = EncodingGSM
	}
	return enc, units, runes, true
}

// naiveCut packs the explicit unit sequence greedily into [start,end) ranges.
func naiveCut(enc string, units []int) []Segment {
	limit, cap := 70, 67
	if enc == EncodingGSM {
		limit, cap = 160, 153
	}
	total := 0
	for _, u := range units {
		total += u
	}
	if total <= limit {
		return []Segment{{0, len(units)}}
	}
	var segs []Segment
	start, used := 0, 0
	for i, u := range units {
		if used+u > cap {
			segs = append(segs, Segment{start, i})
			start, used = i, u
		} else {
			used += u
		}
	}
	segs = append(segs, Segment{start, len(units)})
	return segs
}

func (r *naiveRef) deposit(a string, x int64) error {
	if a == "" || x < 1 || x > 1e12 {
		return fmt.Errorf("invalid_argument")
	}
	acc := r.accounts[a]
	if acc == nil {
		acc = &naiveAccount{}
		r.accounts[a] = acc
	}
	if acc.balance+x > 1e15 {
		return fmt.Errorf("invalid_argument")
	}
	acc.balance += x
	acc.totalDepos += x
	return nil
}

// quote computes the result a send would have, without mutating state.
func (r *naiveRef) quote(a, text string, international bool, now int64) (*naiveResult, string) {
	if a == "" || text == "" || !utf8.ValidString(text) || now < 0 || now > 1e12 {
		return nil, "invalid_argument"
	}
	acc := r.accounts[a]
	if acc == nil {
		return nil, "account_not_found"
	}
	if now < r.maxNow {
		return nil, "clock_skew"
	}
	enc, units, _, _ := unitSeq(text)
	segs := naiveCut(enc, units)
	if len(segs) > 10 {
		return nil, "too_many_segments"
	}

	used, cap0 := int64(0), r.t0
	if acc.everSent {
		kNext := now / r.p
		if kNext == acc.k {
			used, cap0 = acc.u, acc.t
		} else {
			prev := int64(0)
			if kNext == acc.k+1 {
				prev = acc.u
			}
			used, cap0 = 0, r.t0+prev/4
		}
	}

	var s int64
	for j := 1; j <= len(segs); j++ {
		if used+int64(j) <= cap0 {
			s += r.p1
		} else {
			s += r.p2
		}
	}
	fee := s
	if international {
		fee = (s*r.mi + 99) / 100
	}
	if fee > acc.balance {
		return nil, "insufficient_balance"
	}
	return &naiveResult{encoding: enc, segments: segs, fee: fee}, ""
}

func (r *naiveRef) send(a, text string, international bool, now int64) (*naiveResult, string) {
	res, code := r.quote(a, text, international, now)
	if code != "" {
		return nil, code
	}
	acc := r.accounts[a]
	acc.balance -= res.fee
	acc.totalFeePaid += res.fee
	kNext := now / r.p
	if !acc.everSent {
		acc.u, acc.t, acc.k = int64(len(res.segments)), r.t0, kNext
		acc.everSent = true
	} else if kNext != acc.k {
		prev := int64(0)
		if kNext == acc.k+1 {
			prev = acc.u
		}
		acc.u, acc.t, acc.k = int64(len(res.segments)), r.t0+prev/4, kNext
	} else {
		acc.u += int64(len(res.segments))
	}
	if now > r.maxNow {
		r.maxNow = now
	}
	return res, ""
}

func segmentsString(segs []Segment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = fmt.Sprintf("[%d,%d)", s.Start, s.End)
	}
	return strings.Join(parts, ",")
}
