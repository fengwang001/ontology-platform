package kvlog

import (
	"os"
	"path/filepath"
)

func validateKV(key []byte, value []byte) error {
	if len(key) == 0 {
		return newError(KindInvalidArgument, "write", -1, -1, "empty key")
	}
	if len(key) > maxKeyLen {
		return newError(KindInvalidArgument, "write", -1, -1, "key too large")
	}
	if len(value) > maxValueLen {
		return newError(KindInvalidArgument, "write", -1, -1, "value too large")
	}
	return nil
}

// Put appends key=value.
func (e *Engine) Put(key, value []byte) (seq uint64, err error) {
	if verr := validateKV(key, value); verr != nil {
		return 0, verr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.appendLocked(key, value, false)
}

// Delete appends a tombstone for key.
func (e *Engine) Delete(key []byte) (seq uint64, err error) {
	if len(key) == 0 || len(key) > maxKeyLen {
		return 0, newError(KindInvalidArgument, "delete", -1, -1, "bad key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.appendLocked(key, nil, true)
}

func (e *Engine) appendLocked(key, value []byte, tomb bool) (uint64, error) {
	r := record{seq: e.nextSeq, key: key, value: value, tomb: tomb}
	encoded := r.appendTo(nil)

	if e.activeFile == nil {
		if err := e.createActiveLocked(); err != nil {
			return 0, err
		}
	}
	active := e.activeSegment()
	if active.size > 0 && active.size+int64(len(encoded)) > int64(e.cfg.MaxSegmentBytes) {
		if err := e.sealActiveLocked("rollover"); err != nil {
			return 0, err
		}
		if err := e.createActiveLocked(); err != nil {
			return 0, err
		}
		active = e.activeSegment()
	}

	off := active.size
	n, err := e.activeFile.Write(encoded)
	if err != nil {
		return 0, err
	}
	if err := e.activeFile.Sync(); err != nil {
		return 0, err
	}
	active.size += int64(n)
	f := frame{offset: int(off), length: n, rec: r}
	if active.latest == nil {
		active.latest = make(map[string]frame)
	}
	active.latest[string(key)] = f

	e.nextSeq++
	e.dir2.put(string(key), locator{
		segment: active.id,
		offset:  off,
		length:  n,
		seq:     r.seq,
		tomb:    tomb,
	})
	e.logger.Logf("append seq=%d seg=%d off=%d klen=%d tomb=%v -> ok",
		r.seq, active.id, off, len(key), tomb)
	return r.seq, nil
}

func (e *Engine) activeSegment() *segment {
	for i := len(e.segs) - 1; i >= 0; i-- {
		if !e.segs[i].sealed {
			return e.segs[i]
		}
	}
	return nil
}

func (e *Engine) createActiveLocked() error {
	id := e.nextSegID
	e.nextSegID++
	path := e.segPath(id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	s := &segment{id: id, latest: make(map[string]frame)}
	e.segs = append(e.segs, s)
	e.segs = sortSegs(e.segs)
	if e.segIndex == nil {
		e.segIndex = make(map[int]*segment)
	}
	e.segIndex[id] = s
	e.activeFile = f
	e.logger.Logf("create-active seg=%d", id)
	return nil
}

// sealActiveLocked seals the active segment, optionally writing its hint.
// point is the crash-hook label ("rollover" or "merge").
func (e *Engine) sealActiveLocked(point string) error {
	active := e.activeSegment()
	if active == nil {
		return nil
	}
	if err := e.activeFile.Sync(); err != nil {
		return err
	}
	if e.cfg.WriteHints {
		if err := e.writeHintLocked(active); err != nil {
			return err
		}
		if e.crashHook != nil && e.crashHook("seal-hint:"+point) {
			crashExit()
		}
	}
	marker := filepath.Join(e.dir, segSealedName(active.id))
	if err := writeFileAtomic(marker, nil); err != nil {
		return err
	}
	active.sealed = true
	if err := e.activeFile.Close(); err != nil {
		return err
	}
	e.activeFile = nil
	e.logger.Logf("sealed seg=%d size=%d", active.id, active.size)
	return nil
}

func (e *Engine) writeHintLocked(s *segment) error {
	h := &hintFile{segmentID: s.id, validBytes: s.size}
	keys := make([]string, 0, len(s.latest))
	for k := range s.latest {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		f := s.latest[k]
		h.entries = append(h.entries, hintEntry{
			seq:    f.rec.seq,
			offset: int64(f.offset),
			length: f.length,
			tomb:   f.rec.tomb,
			key:    f.rec.key,
		})
	}
	return writeFileAtomic(filepath.Join(e.dir, segHintName(s.id)), encodeHint(h))
}

func sortSegs(in []*segment) []*segment {
	// insertion order is nearly sorted; avoid pulling sort into hot path
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1].id > in[j].id; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
	return in
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// readFileData reads an entire segment file and counts it for tests.
func (e *Engine) readSegData(id int) ([]byte, error) {
	data, err := os.ReadFile(e.segPath(id))
	if err != nil {
		return nil, err
	}
	e.bytesRead.Add(int64(len(data)))
	return data, nil
}
