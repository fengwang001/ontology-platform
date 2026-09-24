// Package api is the public entry point to the temporal join engine.
package api

import (
	"errors"

	"ontology/tjoin"
)

// Joined is one emitted join result.
type Joined = tjoin.Joined

// The four mutually distinguishable sentinel failures.
var (
	ErrEmptyKey      = tjoin.ErrEmptyKey
	ErrLateVersion   = tjoin.ErrLateVersion
	ErrWatermarkBack = tjoin.ErrWatermarkBack
	ErrBufferFull    = tjoin.ErrBufferFull
)

// Joiner is the thread-safe temporal join API.
type Joiner struct{ t *tjoin.Table }

func New(maxBuffered int) *Joiner                           { return &Joiner{t: tjoin.New(maxBuffered)} }
func (j *Joiner) Upsert(k string, vf int64, v string) error { return j.t.Upsert(k, vf, v) }
func (j *Joiner) Delete(k string, vf int64) error           { return j.t.Delete(k, vf) }
func (j *Joiner) Feed(k string, ts int64) ([]Joined, error) { return j.t.Feed(k, ts) }
func (j *Joiner) Watermark(w int64) ([]Joined, error)       { return j.t.Watermark(w) }
func (j *Joiner) Flush() []Joined                           { return j.t.Flush() }
func (j *Joiner) Outputs() []Joined                         { return j.t.Outputs() }

type ent struct {
	val  string
	tomb bool
}

// naiveAsOf is the reference semantics: linear scan of the final accepted
// version set for the greatest ValidFrom <= ts (tombstones included).
func naiveAsOf(tab map[string]map[int64]ent, key string, ts int64) (string, bool) {
	best, have := int64(0), false
	for vf := range tab[key] {
		if vf <= ts && (!have || vf > best) {
			best, have = vf, true
		}
	}
	if !have {
		return "", false
	}
	e := tab[key][best]
	return e.val, !e.tomb
}

type tstep struct {
	kind byte // u upsert, d delete, f feed, w watermark
	key  string
	x    int64
	v    string
	want error
	out  []Joined
}

// script is the built-in sequence: the NOTES thirteen-step case interleaved
// with empty-key, buffer-full, late-version and watermark-back rejections.
var script = []tstep{
	{'u', "k", 10, "A", nil, nil}, {'f', "k", 12, "", nil, nil},
	{'u', "k", 20, "B", nil, nil}, {'f', "k", 25, "", nil, nil},
	{'f', "", 1, "", ErrEmptyKey, nil}, {'u', "", 1, "z", ErrEmptyKey, nil},
	{'w', "k", 11, "", nil, nil}, {'u', "k", 12, "C", nil, nil},
	{'f', "k", 22, "", nil, nil}, {'d', "k", 22, "", nil, nil},
	{'f', "k", 27, "", ErrBufferFull, nil},
	{'w', "k", 22, "", nil, []Joined{{Key: "k", Value: "C", TS: 12, Seq: 0, Found: true}, {Key: "k", TS: 22, Seq: 2}}},
	{'u', "k", 22, "X", ErrLateVersion, nil}, {'w', "k", 21, "", ErrWatermarkBack, nil},
	{'w', "k", 22, "", nil, nil}, {'f', "k", 8, "", nil, []Joined{{Key: "k", TS: 8, Seq: 3}}},
	{'u', "q", 23, "Q", nil, nil}, {'f', "q", 5, "", nil, []Joined{{Key: "q", TS: 5, Seq: 4}}},
	{'f', "q", 100, "", nil, nil}, {'u', "k", 30, "D", nil, nil},
	{'w', "k", 30, "", nil, []Joined{{Key: "k", TS: 25, Seq: 1}}},
}

// SelfCheck replays the built-in sequence through the exported API only. It
// verifies exact per-call errors and outputs, intra-release (TS, Seq) order,
// immutability of the emitted prefix, and—after Flush—agreement of every
// accepted event with naive AS OF (each sequence number appearing once).
func (j *Joiner) SelfCheck() bool {
	jj := New(3)
	tab := map[string]map[int64]ent{}
	var evs, prev []Joined
	for _, s := range script {
		var o []Joined
		var err error
		switch s.kind {
		case 'u':
			err = jj.Upsert(s.key, s.x, s.v)
		case 'd':
			err = jj.Delete(s.key, s.x)
		case 'f':
			o, err = jj.Feed(s.key, s.x)
		default:
			o, err = jj.Watermark(s.x)
		}
		if !errors.Is(err, s.want) || len(o) != len(s.out) {
			return false
		}
		for i := range s.out {
			if o[i] != s.out[i] { // expected slice is ordered by (TS, Seq)
				return false
			}
		}
		all := jj.Outputs() // invariant 3: emitted prefix never changes
		if len(all) < len(prev) {
			return false
		}
		for i := range prev {
			if all[i] != prev[i] {
				return false
			}
		}
		prev = append(prev[:0], all...)
		if err != nil { // invariant 4: rejected step changes no model state
			continue
		}
		switch s.kind {
		case 'u', 'd':
			if tab[s.key] == nil {
				tab[s.key] = map[int64]ent{}
			}
			tab[s.key][s.x] = ent{val: s.v, tomb: s.kind == 'd'}
		case 'f':
			evs = append(evs, Joined{Key: s.key, TS: s.x})
		}
	}
	jj.Flush()
	out := jj.Outputs()
	if len(out) != len(evs) { // invariant 1: one result per accepted event
		return false
	}
	seen := map[int]bool{}
	for _, r := range out {
		if seen[r.Seq] || r.Seq < 0 || r.Seq >= len(evs) {
			return false
		}
		seen[r.Seq] = true
		e := evs[r.Seq]
		val, found := naiveAsOf(tab, e.Key, e.TS)
		if r.Key != e.Key || r.TS != e.TS || r.Found != found || r.Value != val {
			return false
		}
	}
	return true
}
