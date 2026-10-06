package kvlog

import (
	"os"
	"path/filepath"
	"sort"
)

// Open recovers the store in dir. The recovered key directory and read
// results are a deterministic function of the on-disk files.
func Open(dir string, cfg Config) (*Engine, error) {
	if cfg.MaxSegmentBytes <= 0 {
		cfg.MaxSegmentBytes = 1 << 20
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	e := &Engine{
		dir:       filepath.Clean(dir),
		cfg:       cfg,
		dir2:      newKeyDir(),
		segIndex:  make(map[int]*segment),
		nextSeq:   1,
		nextSegID: 1,
		logger:    nopLogger{},
	}
	if err := e.recoverLocked(); err != nil {
		return nil, err
	}
	e.logger.Logf("open dir=%s segments=%d keys=%d nextSeq=%d nextSeg=%d torn=%d",
		e.dir, len(e.segs), e.dir2.len(), e.nextSeq, e.nextSegID, e.tornBytes)
	return e, nil
}

func (e *Engine) recoverLocked() error {
	states, err := listSegments(e.dir)
	if err != nil {
		return err
	}
	if err := e.resolveCommittedMerges(states); err != nil {
		return err
	}
	if states, err = listSegments(e.dir); err != nil {
		return err
	}

	var maxID int
	var maxSeq uint64
	var activeState *segState
	for _, st := range states {
		if st.id > maxID {
			maxID = st.id
		}
		if !st.sealed && len(st.replaces) == 0 {
			if activeState != nil {
				return newError(KindSegmentCorrupt, "recover", st.id, -1,
					"multiple unsealed active segments")
			}
			activeState = st
		}
	}

	// Entries are installed in ascending segment id. Keydir.put keeps the
	// record with the largest seq per key, so installation order cannot
	// distort the final directory.
	for _, st := range states {
		if !st.sealed {
			continue
		}
		s, rerr := e.recoverSealed(st)
		if rerr != nil {
			return rerr
		}
		e.segs = append(e.segs, s)
		e.segIndex[s.id] = s
		maxSeq = e.maxObservedSeq(s, maxSeq)
	}

	if activeState != nil {
		s, rerr := e.recoverActive(activeState)
		if rerr != nil {
			return rerr
		}
		e.segs = append(e.segs, s)
		e.segIndex[s.id] = s
		maxSeq = e.maxObservedSeq(s, maxSeq)

		f, ferr := os.OpenFile(e.segPath(activeState.id), os.O_RDWR|os.O_APPEND, 0o644)
		if ferr != nil {
			return ferr
		}
		e.activeFile = f
	}

	e.nextSegID = maxID + 1
	if maxSeq == 0 {
		e.nextSeq = 1
	} else {
		e.nextSeq = maxSeq + 1
	}
	return nil
}

func (e *Engine) installFrame(s *segment, f frame) {
	s.latest = putLatest(s.latest, f.rec.key, f)
	e.dir2.put(string(f.rec.key), locator{
		segment: s.id,
		offset:  int64(f.offset),
		length:  f.length,
		seq:     f.rec.seq,
		tomb:    f.rec.tomb,
	})
}

func checkIncreasing(frames []frame, segID int) *Error {
	var last uint64
	for _, f := range frames {
		if f.rec.seq <= last {
			return newError(KindSegmentCorrupt, "recover", segID, f.offset,
				"sequence not strictly increasing within segment")
		}
		last = f.rec.seq
	}
	return nil
}

func (e *Engine) recoverSealed(st *segState) (*segment, error) {
	s := &segment{id: st.id, sealed: true, latest: make(map[string]frame)}

	if st.hasHint {
		raw, err := os.ReadFile(filepath.Join(e.dir, segHintName(st.id)))
		if err != nil {
			return nil, err
		}
		h, ok := decodeHint(raw, st.id)
		if ok && h.validBytes == st.size {
			bad := false
			for _, en := range h.entries {
				if en.offset < 0 || en.length <= 0 ||
					en.offset+int64(en.length) > h.validBytes || en.seq == 0 {
					bad = true
					break
				}
			}
			if !bad {
				ordered := append([]hintEntry(nil), h.entries...)
				sort.Slice(ordered, func(i, j int) bool {
					return ordered[i].offset < ordered[j].offset
				})
				var last uint64
				for _, en := range ordered {
					if en.seq <= last {
						return nil, newError(KindSegmentCorrupt, "recover", st.id,
							int(en.offset),
							"sequence not strictly increasing within segment")
					}
					last = en.seq
					e.installFrame(s, frame{
						offset: int(en.offset),
						length: en.length,
						rec:    record{seq: en.seq, key: en.key, tomb: en.tomb},
					})
				}
				s.size = h.validBytes
				s.hintAdopted = true
				e.logger.Logf("recover seg=%d sealed hint-adopted entries=%d size=%d",
					st.id, len(h.entries), h.validBytes)
				return s, nil
			}
		}
		e.logger.Logf("recover seg=%d hint rejected; full scan", st.id)
	}

	data, err := e.readSegData(st.id)
	if err != nil {
		return nil, err
	}
	res := scanRecords(data, false)
	if res.outcome == scanCorrupt {
		return nil, newError(KindSegmentCorrupt, "recover", st.id, res.badOffset,
			"record verification failed")
	}
	if int64(res.validBytes) != st.size {
		return nil, newError(KindSegmentCorrupt, "recover", st.id, res.validBytes,
			"sealed segment has a torn tail")
	}
	if cerr := checkIncreasing(res.frames, st.id); cerr != nil {
		return nil, cerr
	}
	for _, f := range res.frames {
		e.installFrame(s, f)
	}
	s.size = int64(res.validBytes)
	e.logger.Logf("recover seg=%d sealed scanned records=%d size=%d",
		st.id, len(res.frames), s.size)
	return s, nil
}

func (e *Engine) recoverActive(st *segState) (*segment, error) {
	s := &segment{id: st.id, latest: make(map[string]frame)}
	data, err := e.readSegData(st.id)
	if err != nil {
		return nil, err
	}
	res := scanRecords(data, true)
	if res.outcome == scanCorrupt {
		return nil, newError(KindSegmentCorrupt, "recover", st.id, res.badOffset,
			"record verification failed")
	}
	if res.outcome == scanTornTail {
		if err := os.Truncate(e.segPath(st.id), int64(res.validBytes)); err != nil {
			return nil, err
		}
		e.tornBytes = int64(len(data) - res.validBytes)
		e.logger.Logf("recover seg=%d active torn-tail discarded=%d",
			st.id, e.tornBytes)
	}
	if cerr := checkIncreasing(res.frames, st.id); cerr != nil {
		return nil, cerr
	}
	for _, f := range res.frames {
		e.installFrame(s, f)
	}
	s.size = int64(res.validBytes)
	e.logger.Logf("recover seg=%d active records=%d size=%d",
		st.id, len(res.frames), s.size)
	return s, nil
}

func putLatest(m map[string]frame, key []byte, f frame) map[string]frame {
	k := string(key)
	if cur, ok := m[k]; !ok || f.rec.seq > cur.rec.seq {
		m[k] = f
	}
	return m
}

func (e *Engine) maxObservedSeq(s *segment, cur uint64) uint64 {
	for _, f := range s.latest {
		if f.rec.seq > cur {
			cur = f.rec.seq
		}
	}
	return cur
}
