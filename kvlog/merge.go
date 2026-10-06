package kvlog

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// resolveCommittedMerges performs recovery-time cleanup:
//
//   - A sealed output segment coexisting with the segments it declares it
//     replaces is a committed merge: the replaced segments are deleted.
//   - An unsealed output segment (crash before/during sealing) is wholly
//     discarded (log + marker/temp files + merge manifest), old segments are
//     retained.
//
// Resolution is iterated in ascending output id so merge chains are handled.
func (e *Engine) resolveCommittedMerges(states []*segState) error {
	outputs := make([]*segState, 0)
	for _, st := range states {
		if len(st.replaces) > 0 {
			outputs = append(outputs, st)
		}
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].id < outputs[j].id })
	for _, out := range outputs {
		if !out.sealed {
			if err := e.discardSegment(out.id); err != nil {
				return err
			}
			e.logger.Logf("recover merge-output seg=%d unsealed: discarded", out.id)
			continue
		}
		data, err := e.readSegData(out.id)
		if err != nil {
			return err
		}
		res := scanRecords(data, false)
		if res.outcome != scanOK || res.validBytes != int(out.size) {
			if err := e.discardSegment(out.id); err != nil {
				return err
			}
			e.logger.Logf("recover merge-output seg=%d torn/corrupt: discarded", out.id)
			continue
		}
		for _, old := range out.replaces {
			if old == out.id {
				return newError(KindSegmentCorrupt, "recover", out.id, -1,
					"merge manifest lists the output itself")
			}
			if err := e.removeSegmentFiles(old); err != nil {
				return err
			}
			e.logger.Logf("recover committed merge seg=%d replaces-seg=%d: removed old",
				out.id, old)
		}
	}
	return nil
}

func (e *Engine) removeSegmentFiles(id int) error {
	for _, suffix := range []string{".log", ".hint", ".sealed", ".merge", ".tmp", ".merge.tmp"} {
		if err := os.Remove(filepath.Join(e.dir, fmtID(id)+suffix)); err != nil &&
			!os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (e *Engine) discardSegment(id int) error {
	return e.removeSegmentFiles(id)
}

func fmtID(id int) string {
	const digits = "00000000"
	s := strconv.Itoa(id)
	if len(s) >= len(digits) {
		return s
	}
	return digits[:len(digits)-len(s)] + s
}

// Merge merges the named sealed segments into one new sealed segment.
// ids need not be contiguous and must not include the active segment.
func (e *Engine) Merge(ids []int) (outputID int, err error) {
	if len(ids) == 0 {
		return 0, newError(KindInvalidArgument, "merge", -1, -1, "no segments")
	}

	e.mu.RLock()
	type planSeg struct {
		id     int
		frames []frame
	}
	sortedIDs := append([]int(nil), ids...)
	sort.Ints(sortedIDs)
	for i := 1; i < len(sortedIDs); i++ {
		if sortedIDs[i] == sortedIDs[i-1] {
			e.mu.RUnlock()
			return 0, newError(KindInvalidArgument, "merge", sortedIDs[i], -1,
				"duplicate segment")
		}
	}
	segByID := make(map[int]*segment, len(e.segs))
	for _, s := range e.segs {
		segByID[s.id] = s
	}
	plan := make([]planSeg, 0, len(sortedIDs))
	merging := make(map[int]bool)
	newest := make(map[string]frame)
	for _, id := range sortedIDs {
		s, ok := segByID[id]
		if !ok {
			e.mu.RUnlock()
			return 0, newError(KindSegmentNotFound, "merge", id, -1, "")
		}
		if !s.sealed {
			e.mu.RUnlock()
			return 0, newError(KindActiveSegmentNotMergeable, "merge", id, -1, "")
		}
		data, rerr := e.readSegData(id)
		if rerr != nil {
			e.mu.RUnlock()
			return 0, rerr
		}
		res := scanRecords(data, false)
		if res.outcome != scanOK {
			e.mu.RUnlock()
			return 0, newError(KindSegmentCorrupt, "merge", id, res.badOffset,
				"record verification failed")
		}
		plan = append(plan, planSeg{id: id, frames: res.frames})
		merging[id] = true
		for _, f := range res.frames {
			k := string(f.rec.key)
			if cur, ok := newest[k]; !ok || f.rec.seq > cur.rec.seq {
				newest[k] = f
			}
		}
	}

	// Tombstone-drop condition: a tombstone may be dropped only if no
	// surviving (non-merged) segment contains an older record for that key.
	outsideHasOlder := make(map[string]bool)
	for _, s := range e.segs {
		if merging[s.id] {
			continue
		}
		data, rerr := e.readSegData(s.id)
		if rerr != nil {
			e.mu.RUnlock()
			return 0, rerr
		}
		res := scanRecords(data, false)
		if res.outcome != scanOK {
			e.mu.RUnlock()
			return 0, newError(KindSegmentCorrupt, "merge", s.id, res.badOffset,
				"record verification failed")
		}
		for _, f := range res.frames {
			k := string(f.rec.key)
			win, merged := newest[k]
			if merged && f.rec.seq < win.rec.seq {
				outsideHasOlder[k] = true
			}
		}
	}

	type outEntry struct {
		f frame
	}
	outList := make([]outEntry, 0, len(newest))
	for k, f := range newest {
		if f.rec.tomb && !outsideHasOlder[k] {
			continue
		}
		outList = append(outList, outEntry{f: f})
	}
	sort.Slice(outList, func(i, j int) bool {
		return outList[i].f.rec.seq < outList[j].f.rec.seq
	})
	payload := make([]byte, 0)
	type keptFrame struct {
		f frame
	}
	kept := make([]keptFrame, 0, len(outList))
	for _, o := range outList {
		off := len(payload)
		payload = o.f.rec.appendTo(payload)
		f2 := o.f
		f2.offset = off
		f2.length = len(payload) - off
		kept = append(kept, keptFrame{f: f2})
	}

	manifest := buildManifest(sortedIDs)
	outID := e.nextSegID
	e.mu.RUnlock()

	// Writing phase is lock-free: readers and writers proceed unaffected.
	logPath := filepath.Join(e.dir, segLogName(outID))
	tmpPath := logPath + ".merge.tmp"
	_ = os.Remove(tmpPath)
	wf, werr := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if werr != nil {
		return 0, werr
	}
	if _, werr = wf.Write(payload); werr != nil {
		wf.Close()
		os.Remove(tmpPath)
		return 0, werr
	}
	if werr = wf.Sync(); werr != nil {
		wf.Close()
		os.Remove(tmpPath)
		return 0, werr
	}
	if werr = wf.Close(); werr != nil {
		os.Remove(tmpPath)
		return 0, werr
	}
	if e.hookCrash("merge-data-written") {
		crashExit()
	}
	if werr = os.Rename(tmpPath, logPath); werr != nil {
		os.Remove(tmpPath)
		return 0, werr
	}
	if e.hookCrash("merge-data-renamed") {
		crashExit()
	}
	// Manifest first, then sealed marker: until the marker exists recovery
	// treats the output as an unfinished merge and discards it.
	if werr = writeFileAtomic(filepath.Join(e.dir, segMergeName(outID)),
		[]byte(manifest)); werr != nil {
		os.Remove(logPath)
		return 0, werr
	}
	if e.hookCrash("merge-manifest") {
		crashExit()
	}
	hint := &hintFile{segmentID: outID, validBytes: int64(len(payload))}
	for _, kk := range kept {
		hint.entries = append(hint.entries, hintEntry{
			seq:    kk.f.rec.seq,
			offset: int64(kk.f.offset),
			length: kk.f.length,
			tomb:   kk.f.rec.tomb,
			key:    kk.f.rec.key,
		})
	}
	if werr = writeFileAtomic(filepath.Join(e.dir, segHintName(outID)),
		encodeHint(hint)); werr != nil {
		os.Remove(logPath)
		os.Remove(filepath.Join(e.dir, segMergeName(outID)))
		return 0, werr
	}
	if e.hookCrash("merge-hint") {
		crashExit()
	}
	if werr = writeFileAtomic(filepath.Join(e.dir, segSealedName(outID)), nil); werr != nil {
		return 0, werr
	}
	if e.hookCrash("merge-sealed") {
		crashExit()
	}
	if dErr := syncDir(e.dir); dErr != nil {
		return 0, dErr
	}

	// Commit phase under the write lock.
	e.mu.Lock()
	defer e.mu.Unlock()
	liveByID := make(map[int]*segment, len(e.segs))
	for _, s := range e.segs {
		liveByID[s.id] = s
	}
	for _, id := range sortedIDs {
		if _, ok := liveByID[id]; !ok {
			_ = e.removeSegmentFiles(outID)
			return 0, newError(KindSegmentNotFound, "merge", id, -1,
				"segment vanished during merge")
		}
	}
	if _, exists := liveByID[outID]; exists {
		_ = e.removeSegmentFiles(outID)
		return 0, newError(KindInvalidArgument, "merge", outID, -1,
			"output id collision")
	}

	outSeg := &segment{
		id:     outID,
		sealed: true,
		size:   int64(len(payload)),
		latest: make(map[string]frame),
	}
	for _, kk := range kept {
		outSeg.latest[string(kk.f.rec.key)] = kk.f
	}
	e.segs = append(e.segs, outSeg)
	sortSegs(e.segs)
	e.segIndex[outID] = outSeg
	if outID >= e.nextSegID {
		e.nextSegID = outID + 1
	}

	for _, id := range sortedIDs {
		e.dir2.removeSegment(id)
	}
	for _, kk := range kept {
		k := string(kk.f.rec.key)
		cur, ok := e.dir2.get(k)
		if !ok || kk.f.rec.seq > cur.seq {
			e.dir2.put(k, locator{
				segment: outID,
				offset:  int64(kk.f.offset),
				length:  kk.f.length,
				seq:     kk.f.rec.seq,
				tomb:    kk.f.rec.tomb,
			})
		}
	}

	for _, id := range sortedIDs {
		if err := e.removeSegmentFiles(id); err != nil {
			return 0, err
		}
	}
	e.segs = filterSegs(e.segs, sortedIDs)
	for _, id := range sortedIDs {
		delete(e.segIndex, id)
	}
	e.logger.Logf("merge ids=%v -> seg=%d kept=%d committed",
		sortedIDs, outID, len(kept))
	return outID, nil
}

func (e *Engine) hookCrash(point string) bool {
	e.mu.RLock()
	h := e.crashHook
	e.mu.RUnlock()
	return h != nil && h(point)
}

func buildManifest(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, " ") + "\n"
}

func filterSegs(in []*segment, removed []int) []*segment {
	drop := make(map[int]bool)
	for _, id := range removed {
		drop[id] = true
	}
	out := in[:0]
	for _, s := range in {
		if !drop[s.id] {
			out = append(out, s)
		}
	}
	return out
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
