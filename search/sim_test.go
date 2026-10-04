package search

import (
	"errors"
	"math/rand"
	"sort"
)

const simPmax = 3

type simSeg struct {
	id    int
	docs  []Doc
	delop map[string]int64
	alive bool
}

type simPIT struct {
	op   int64
	exp  int64
	segs []int
}

type simulator struct {
	rng      *rand.Rand
	now      int64
	nextOp   int64
	nextSeg  int
	nextPIT  int
	segs     map[int]*simSeg
	live     map[string]int
	pits     map[int]*simPIT
	released []int
}

func newSim(rng *rand.Rand) *simulator {
	return &simulator{
		rng:     rng,
		nextOp:  1,
		nextSeg: 1,
		nextPIT: 1,
		segs:    map[int]*simSeg{},
		live:    map[string]int{},
		pits:    map[int]*simPIT{},
	}
}

func simSort(docs []Doc) []Doc {
	out := append([]Doc(nil), docs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortVal != out[j].SortVal {
			return out[i].SortVal < out[j].SortVal
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *simulator) validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (s *simulator) landExpired() {
	var dead []int
	for id, p := range s.pits {
		if p.exp <= s.now {
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		delete(s.pits, id)
	}
	s.gc()
}

func (s *simulator) held(sid int) bool {
	for _, p := range s.pits {
		for _, x := range p.segs {
			if x == sid {
				return true
			}
		}
	}
	return false
}

func (s *simulator) gc() {
	var freed []int
	for sid, sg := range s.segs {
		if !sg.alive && !s.held(sid) {
			freed = append(freed, sid)
		}
	}
	sort.Ints(freed)
	for _, sid := range freed {
		delete(s.segs, sid)
		s.released = append(s.released, sid)
	}
}

func (s *simulator) begin(now int64) error {
	if !s.validNow(now) {
		return ErrInvalid
	}
	if now < s.now {
		return ErrClockRollback
	}
	s.now = now
	s.landExpired()
	return nil
}

func (s *simulator) allocOp() int64 {
	op := s.nextOp
	s.nextOp++
	return op
}

func (s *simulator) add(now int64, docs []Doc) (int, error) {
	if err := s.begin(now); err != nil {
		return 0, err
	}
	if len(docs) < 1 || len(docs) > 10000 {
		return 0, ErrInvalid
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if d.ID == "" {
			return 0, ErrInvalid
		}
		if seen[d.ID] {
			return 0, ErrInvalid
		}
		seen[d.ID] = true
		if _, ok := s.live[d.ID]; ok {
			return 0, ErrIDConflict
		}
	}
	op := s.allocOp()
	id := s.nextSeg
	s.nextSeg++
	ordered := simSort(docs)
	s.segs[id] = &simSeg{id: id, docs: ordered, delop: map[string]int64{}, alive: true}
	for _, d := range ordered {
		s.live[d.ID] = id
	}
	_ = op
	return id, nil
}

func (s *simulator) del(now int64, id string) error {
	if err := s.begin(now); err != nil {
		return err
	}
	if id == "" {
		return ErrInvalid
	}
	if _, ok := s.live[id]; !ok {
		return ErrDocNotFound
	}
	op := s.allocOp()
	sid := s.live[id]
	s.segs[sid].delop[id] = op
	delete(s.live, id)
	return nil
}

func (s *simulator) merge(now int64, segIDs []int) (int, error) {
	if err := s.begin(now); err != nil {
		return 0, err
	}
	if len(segIDs) < 2 || len(segIDs) > 10 {
		return 0, ErrInvalid
	}
	seen := map[int]bool{}
	for _, sid := range segIDs {
		if seen[sid] {
			return 0, ErrInvalid
		}
		seen[sid] = true
		sg, ok := s.segs[sid]
		if !ok || !sg.alive {
			return 0, ErrSegNotFound
		}
	}
	var survivors []Doc
	for _, sid := range segIDs {
		sg := s.segs[sid]
		for _, d := range sg.docs {
			if _, deleted := sg.delop[d.ID]; deleted {
				continue
			}
			survivors = append(survivors, d)
		}
		sg.alive = false
	}
	op := s.allocOp()
	s.gc()
	if len(survivors) == 0 {
		_ = op
		return 0, nil
	}
	id := s.nextSeg
	s.nextSeg++
	ordered := simSort(survivors)
	s.segs[id] = &simSeg{id: id, docs: ordered, delop: map[string]int64{}, alive: true}
	for _, d := range ordered {
		delete(s.live, d.ID)
		s.live[d.ID] = id
	}
	return id, nil
}

func (s *simulator) open(now int64, ka int64) (int, error) {
	if !s.validNow(now) {
		return 0, ErrInvalid
	}
	if ka < 1 || ka > 1_000_000_000 {
		return 0, ErrInvalid
	}
	if now < s.now {
		return 0, ErrClockRollback
	}
	s.now = now
	s.landExpired()
	if len(s.pits) >= simPmax {
		return 0, ErrPITLimit
	}
	op := s.allocOp()
	var segs []int
	for sid, sg := range s.segs {
		if sg.alive {
			segs = append(segs, sid)
		}
	}
	sort.Ints(segs)
	id := s.nextPIT
	s.nextPIT++
	s.pits[id] = &simPIT{op: op, exp: now + ka, segs: segs}
	return id, nil
}

func (s *simulator) close(now int64, pid int) error {
	if err := s.begin(now); err != nil {
		return err
	}
	if pid <= 0 {
		return ErrInvalid
	}
	if _, ok := s.pits[pid]; !ok {
		return ErrPITNotFound
	}
	delete(s.pits, pid)
	s.gc()
	return nil
}

func (s *simulator) scan(segIDs []int, op int64, after *Key, size int) []Entry {
	var out []Entry
	for _, sid := range segIDs {
		sg, ok := s.segs[sid]
		if !ok {
			continue
		}
		for idx, doc := range sg.docs {
			if dop, deleted := sg.delop[doc.ID]; deleted && dop <= op {
				continue
			}
			k := Key{SortVal: doc.SortVal, Seg: sid, Idx: idx}
			if after != nil && !((k.SortVal > after.SortVal) ||
				(k.SortVal == after.SortVal && (k.Seg > after.Seg ||
					(k.Seg == after.Seg && k.Idx > after.Idx)))) {
				continue
			}
			out = append(out, Entry{Doc: doc, Key: k})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Key, out[j].Key
		if a.SortVal != b.SortVal {
			return a.SortVal < b.SortVal
		}
		if a.Seg != b.Seg {
			return a.Seg < b.Seg
		}
		return a.Idx < b.Idx
	})
	if len(out) > size {
		out = out[:size]
	}
	return out
}

func (s *simulator) search(now int64, pid, size int, after *Key, ka int64) ([]Entry, error) {
	if size < 1 || size > 1000 || ka < 0 || ka > 1_000_000_000 {
		return nil, ErrInvalid
	}
	if pid == 0 && (after != nil || ka != 0) {
		return nil, ErrInvalid
	}
	if err := s.begin(now); err != nil {
		return nil, err
	}
	if pid == 0 {
		var segs []int
		for sid, sg := range s.segs {
			if sg.alive {
				segs = append(segs, sid)
			}
		}
		return s.scan(segs, 1<<62, nil, size), nil
	}
	p, ok := s.pits[pid]
	if !ok {
		return nil, ErrPITNotFound
	}
	if ka > 0 {
		if cand := now + ka; cand > p.exp {
			p.exp = cand
		}
	}
	return s.scan(append([]int(nil), p.segs...), p.op, after, size), nil
}

func classify(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, ErrInvalid):
		return "Invalid"
	case errors.Is(err, ErrClockRollback):
		return "ClockRollback"
	case errors.Is(err, ErrPITNotFound):
		return "PITNotFound"
	case errors.Is(err, ErrPITLimit):
		return "PITLimit"
	case errors.Is(err, ErrIDConflict):
		return "IDConflict"
	case errors.Is(err, ErrDocNotFound):
		return "DocNotFound"
	case errors.Is(err, ErrSegNotFound):
		return "SegNotFound"
	default:
		return "Unknown:" + err.Error()
	}
}
