package kvlog

import (
	"hash/crc32"
	"os"
)

// Get reads a key. Result status distinguishes present / deleted / never seen.
// A keydir distortion triggers one segment rebuild and retry; if the rebuild
// still disagrees with the bytes, segment corruption is reported.
func (e *Engine) Get(key []byte) (Result, error) {
	if len(key) == 0 || len(key) > maxKeyLen {
		return Result{}, newError(KindInvalidArgument, "get", -1, -1, "bad key")
	}
	ks := string(key)

	e.mu.RLock()
	loc, ok := e.dir2.get(ks)
	if !ok {
		e.mu.RUnlock()
		e.logger.Logf("get klen=%d -> missing (no locator)", len(key))
		return Result{Status: StatusMissing}, nil
	}
	res, derr := e.readAtLocked(ks, loc)
	if derr == nil {
		e.mu.RUnlock()
		return res, nil
	}
	if derr.Kind != KindKeyDirDistorted {
		e.mu.RUnlock()
		return Result{}, derr
	}
	segID := loc.segment
	e.mu.RUnlock()

	// Self-heal: rebuild that segment's keydir entries under the write lock.
	e.mu.Lock()
	if rerr := e.rebuildSegmentLocked(segID); rerr != nil {
		e.mu.Unlock()
		return Result{}, rerr
	}
	e.selfHeals++
	loc2, ok2 := e.dir2.get(ks)
	if !ok2 {
		e.mu.Unlock()
		e.logger.Logf("get klen=%d -> missing after self-heal seg=%d", len(key), segID)
		return Result{Status: StatusMissing}, nil
	}
	res2, derr2 := e.readAtLocked(ks, loc2)
	e.mu.Unlock()
	if derr2 != nil {
		e.logger.Logf("get klen=%d self-heal seg=%d failed: %v", len(key), segID, derr2)
		return Result{}, derr2
	}
	e.logger.Logf("get klen=%d self-heal seg=%d -> status=%d", len(key), segID, res2.Status)
	return res2, nil
}

// readAtLocked verifies and decodes the record at loc. Caller holds e.mu
// (read or write). It reads only the bytes covered by the locator: read
// cost is independent of total keys and total segments.
func (e *Engine) readAtLocked(expectKey string, loc *locator) (Result, *Error) {
	f, err := os.Open(e.segPath(loc.segment))
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, newError(KindSegmentNotFound, "get", loc.segment, -1, "")
		}
		return Result{}, newError(KindSegmentCorrupt, "get", loc.segment, -1, err.Error())
	}
	defer f.Close()
	buf := make([]byte, loc.length)
	n, err := f.ReadAt(buf, loc.offset)
	e.bytesRead.Add(int64(n))
	if err != nil || n != loc.length {
		return Result{}, newError(KindSegmentCorrupt, "get", loc.segment,
			int(loc.offset), "short read at locator")
	}
	if len(buf) < recordHeaderLen {
		return Result{}, newError(KindSegmentCorrupt, "get", loc.segment,
			int(loc.offset), "frame too short")
	}
	if u32(buf[0:]) != crc32.Checksum(buf[4:], crcTable) {
		return Result{}, newError(KindSegmentCorrupt, "get", loc.segment,
			int(loc.offset), "record checksum mismatch")
	}
	klen := int(u32(buf[12:]))
	vlen := int(u32(buf[16:]))
	flags := buf[20]
	if recordHeaderLen+klen+vlen != loc.length ||
		klen == 0 || klen > maxKeyLen || vlen > maxValueLen ||
		(flags&^flagTombstone) != 0 ||
		(flags&flagTombstone != 0 && vlen != 0) {
		return Result{}, newError(KindSegmentCorrupt, "get", loc.segment,
			int(loc.offset), "frame header invalid")
	}
	seq := u64(buf[4:])
	gotKey := buf[recordHeaderLen : recordHeaderLen+klen]
	if string(gotKey) != expectKey {
		return Result{}, newError(KindKeyDirDistorted, "get", loc.segment,
			int(loc.offset), "key mismatch at locator")
	}
	if seq != loc.seq {
		return Result{}, newError(KindKeyDirDistorted, "get", loc.segment,
			int(loc.offset), "sequence mismatch at locator")
	}
	if flags&flagTombstone != 0 {
		return Result{Status: StatusDeleted}, nil
	}
	val := append([]byte(nil), buf[recordHeaderLen+klen:]...)
	return Result{Status: StatusPresent, Value: val}, nil
}

// rebuildSegmentLocked fully re-scans one segment and replaces every keydir
// entry that points at it. Any verification failure is segment corruption.
func (e *Engine) rebuildSegmentLocked(segID int) *Error {
	s := e.segByID(segID)
	if s == nil {
		return newError(KindSegmentNotFound, "rebuild", segID, -1, "")
	}
	data, err := e.readSegData(segID)
	if err != nil {
		if os.IsNotExist(err) {
			return newError(KindSegmentNotFound, "rebuild", segID, -1, "")
		}
		return newError(KindSegmentCorrupt, "rebuild", segID, -1, err.Error())
	}
	res := scanRecords(data, !s.sealed)
	if res.outcome == scanCorrupt {
		return newError(KindSegmentCorrupt, "rebuild", segID, res.badOffset,
			"record verification failed")
	}
	e.dir2.removeSegment(segID)
	latest := make(map[string]frame)
	for _, f := range res.frames {
		latest[string(f.rec.key)] = f
	}
	for _, f := range latest {
		e.dir2.put(string(f.rec.key), locator{
			segment: segID,
			offset:  int64(f.offset),
			length:  f.length,
			seq:     f.rec.seq,
			tomb:    f.rec.tomb,
		})
	}
	s.latest = latest
	s.size = int64(res.validBytes)
	s.hintAdopted = false
	return nil
}

func (e *Engine) segByID(id int) *segment {
	return e.segIndex[id]
}
