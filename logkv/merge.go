package logkv

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
)

// 合并元信息文件布局（全部小端）：
//
//	magic     "LKM1" 4 字节
//	outputID  u32   输出段号（被合并集合中的最小段号）
//	count     u32
//	replaced  count 个 u32，输出段声明替代的段集合（含 outputID）
//	crc       u32   对之前全部字节的 CRC32(Castagnoli)
const mergeMetaMagic = "LKM1"

type mergeMeta struct {
	outputID uint32
	replaced []uint32
}

func encodeMergeMeta(m mergeMeta) []byte {
	buf := []byte(mergeMetaMagic)
	buf = binary.LittleEndian.AppendUint32(buf, m.outputID)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(m.replaced)))
	for _, id := range m.replaced {
		buf = binary.LittleEndian.AppendUint32(buf, id)
	}
	return binary.LittleEndian.AppendUint32(buf, crc32.Checksum(buf, crcTable))
}

func decodeMergeMeta(buf []byte) (mergeMeta, bool) {
	var m mergeMeta
	if len(buf) < 4+4+4+4 {
		return m, false
	}
	if string(buf[:4]) != mergeMetaMagic {
		return m, false
	}
	stored := binary.LittleEndian.Uint32(buf[len(buf)-4:])
	if crc32.Checksum(buf[:len(buf)-4], crcTable) != stored {
		return m, false
	}
	body := buf[4 : len(buf)-4]
	m.outputID = binary.LittleEndian.Uint32(body[0:4])
	count := binary.LittleEndian.Uint32(body[4:8])
	rest := body[8:]
	if len(rest) != int(count)*4 {
		return mergeMeta{}, false
	}
	for i := uint32(0); i < count; i++ {
		m.replaced = append(m.replaced, binary.LittleEndian.Uint32(rest[i*4:]))
	}
	return m, true
}

// cleanupMergeArtifacts 处理合并中途崩溃的残留：
//   - 未封口的输出（.mseg.tmp）整体丢弃，保留旧段；
//   - 已封口的输出（.seg + .mmeta）与被替代段并存时，清理被替代段；
//   - 孤立的 .mmeta 直接删除。
//
// 返回清理后按段号升序排列的段号列表。
func (s *Store) cleanupMergeArtifacts() ([]uint32, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	tmps := make(map[uint32]bool)
	metas := make(map[uint32]bool)
	segs := make(map[uint32]bool)
	for _, e := range entries {
		var id uint32
		if n, err := fmt.Sscanf(e.Name(), "%06d.mseg.tmp", &id); n == 1 && err == nil {
			tmps[id] = true
			continue
		}
		if n, err := fmt.Sscanf(e.Name(), "%06d.mmeta", &id); n == 1 && err == nil {
			metas[id] = true
			continue
		}
		if n, err := fmt.Sscanf(e.Name(), "%06d.seg", &id); n == 1 && err == nil {
			segs[id] = true
		}
	}
	for id := range tmps {
		// 输出段未封口：整体丢弃，保留旧段。
		os.Remove(mergeTmpPath(s.dir, id))
		os.Remove(mergeMetaPath(s.dir, id))
		delete(metas, id)
	}
	for id := range metas {
		metaPath := mergeMetaPath(s.dir, id)
		if !segs[id] {
			// 输出段不存在，元信息失去意义。
			os.Remove(metaPath)
			continue
		}
		data, err := os.ReadFile(metaPath)
		if err != nil {
			return nil, err
		}
		m, ok := decodeMergeMeta(data)
		if !ok || m.outputID != id {
			return nil, corruptError(id, -1, "merge meta checksum mismatch")
		}
		// 输出段已封口：清理其声明替代的段（输出段自身除外）。
		for _, rid := range m.replaced {
			if rid == id {
				continue
			}
			os.Remove(segmentPath(s.dir, rid))
			os.Remove(hintPath(s.dir, rid))
			delete(segs, rid)
		}
		os.Remove(metaPath)
	}
	ids := make([]uint32, 0, len(segs))
	for id := range segs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// Merge 把若干已封口段合并为一个新的已封口段。每个键只保留
// 被合并段中写序号最大的记录；删除标记仅当不在合并集合中的任何
// 段里都不存在该键写序号更小的记录时才丢弃。封口（原子改名）后
// 合并才算完成，随后被替代的段才被删除。
func (s *Store) Merge(ids []uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return newError(KindInvalidArgument, "store closed")
	}
	// 1. 参数非法：空集合、重复段号。
	if len(ids) == 0 {
		return newError(KindInvalidArgument, "empty merge set")
	}
	seen := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return newError(KindInvalidArgument, fmt.Sprintf("duplicate segment %06d", id))
		}
		seen[id] = true
	}
	// 2. 收集存在的段并扫描（段损坏优先于段不存在与活动段不可合并）。
	var existing []uint32
	var notFound uint32
	hasActive := false
	for _, id := range ids {
		if s.active != nil && id == s.active.id {
			hasActive = true
			continue
		}
		if _, ok := s.segments[id]; !ok {
			if notFound == 0 || id < notFound {
				notFound = id
			}
			continue
		}
		existing = append(existing, id)
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
	merged, err := s.collectMergedRecords(existing)
	if err != nil {
		return err
	}
	// 3. 段不存在。4. 活动段不可合并。
	if notFound != 0 {
		return &Error{Kind: KindSegmentNotFound, Segment: notFound, Offset: -1,
			Msg: "segment does not exist"}
	}
	if hasActive {
		return &Error{Kind: KindActiveSegmentNotMergeable, Segment: s.active.id, Offset: -1,
			Msg: "active segment cannot be merged"}
	}
	if len(existing) == 0 {
		return newError(KindInvalidArgument, "merge set has no sealed segment")
	}
	return s.mergeCommit(existing, merged)
}

// collectMergedRecords 扫描被合并段，返回每个键写序号最大的记录。
func (s *Store) collectMergedRecords(ids []uint32) (map[string]Record, error) {
	merged := make(map[string]Record)
	for _, id := range ids {
		seg := s.segments[id]
		f, err := os.Open(seg.path)
		if err != nil {
			return nil, err
		}
		res, err := scanSegment(id, f, seg.size, false)
		f.Close()
		if err != nil {
			return nil, err
		}
		for _, sr := range res.records {
			key := string(sr.Key)
			cur, ok := merged[key]
			if ok && cur.Seq >= sr.Seq {
				continue
			}
			merged[key] = Record{
				Seq:       sr.Seq,
				Key:       append([]byte(nil), sr.Key...),
				Value:     append([]byte(nil), sr.Value...),
				Tombstone: sr.Tombstone,
			}
		}
	}
	return merged, nil
}

// mergeCommit 写出输出段、封口、清理被替代段并切换键目录。
func (s *Store) mergeCommit(ids []uint32, merged map[string]Record) error {
	outputID := ids[0]
	// 删除标记的丢弃条件：不在合并集合中的任何段里，不存在该键
	// 写序号更小的记录。扫描集合外的全部段，记录这些键在集合外
	// 的最小写序号。
	outsideMin, err := s.scanOutsideMinSeq(ids, merged)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(merged))
	for k := range merged {
		names = append(names, k)
	}
	sort.Strings(names)
	// 1. 写输出段临时文件。
	tmp, err := os.OpenFile(mergeTmpPath(s.dir, outputID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	outEntries := make(map[string]keydirEntry)
	off := int64(0)
	for _, k := range names {
		rec := merged[k]
		if rec.Tombstone {
			minSeq, seen := outsideMin[k]
			if !seen || minSeq >= rec.Seq {
				continue // 无更小的旧值，丢弃删除标记
			}
		}
		buf := EncodeRecord(rec.Seq, rec.Key, rec.Value, rec.Tombstone)
		if _, err := tmp.Write(buf); err != nil {
			tmp.Close()
			return err
		}
		outEntries[k] = keydirEntry{
			Segment:   outputID,
			Offset:    uint64(off),
			Length:    uint32(len(buf)),
			Seq:       rec.Seq,
			Tombstone: rec.Tombstone,
		}
		off += int64(len(buf))
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	s.hook("tmp-written")
	// 2. 写合并元信息（声明替代的段集合）。
	meta := mergeMeta{outputID: outputID, replaced: ids}
	if err := os.WriteFile(mergeMetaPath(s.dir, outputID), encodeMergeMeta(meta), 0o644); err != nil {
		return err
	}
	s.hook("meta-written")
	// 3. 封口：原子改名为正式段文件。此后合并即算完成。
	if err := os.Rename(mergeTmpPath(s.dir, outputID), segmentPath(s.dir, outputID)); err != nil {
		return err
	}
	s.hook("sealed")
	// 4. 生成输出段提示信息。
	if s.cfg.WriteHints {
		if err := s.writeHint(outputID, outEntries, off); err != nil {
			return err
		}
	}
	s.hook("hint-written")
	// 5. 切换内存状态并删除被替代的段。
	replaced := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		replaced[id] = true
	}
	outSeg, err := openSegment(s.dir, outputID, true)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == outputID {
			continue
		}
		if old := s.segments[id]; old != nil {
			old.close()
			delete(s.segments, id)
		}
		os.Remove(segmentPath(s.dir, id))
		os.Remove(hintPath(s.dir, id))
	}
	if old := s.segments[outputID]; old != nil {
		old.close()
	}
	s.segments[outputID] = outSeg
	s.hook("files-deleted")
	// 6. 键目录切换：仅当条目仍指向被替代段时才改写，
	// 并发写入产生的新记录（位于活动段）不受影响。
	for k, e := range outEntries {
		if cur, ok := s.keys.m[k]; ok && replaced[cur.Segment] {
			s.keys.m[k] = e
		}
	}
	for _, id := range ids {
		if id == outputID {
			continue
		}
		s.keys.removeSegment(id)
	}
	// 输出段中被丢弃的删除标记：清除其旧条目。
	for k, rec := range merged {
		if _, kept := outEntries[k]; kept {
			continue
		}
		if rec.Tombstone {
			if cur, ok := s.keys.m[k]; ok && replaced[cur.Segment] {
				s.keys.delete(k)
			}
		}
	}
	os.Remove(mergeMetaPath(s.dir, outputID))
	s.hook("done")
	return nil
}

// scanOutsideMinSeq 扫描合并集合外的全部段，返回 merged 中删除
// 标记键在集合外的最小写序号。
func (s *Store) scanOutsideMinSeq(ids []uint32, merged map[string]Record) (map[string]uint64, error) {
	tombKeys := make(map[string]bool)
	for k, rec := range merged {
		if rec.Tombstone {
			tombKeys[k] = true
		}
	}
	result := make(map[string]uint64)
	if len(tombKeys) == 0 {
		return result, nil
	}
	inSet := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		inSet[id] = true
	}
	var outsiders []*segment
	for id, seg := range s.segments {
		if !inSet[id] {
			outsiders = append(outsiders, seg)
		}
	}
	if s.active != nil {
		outsiders = append(outsiders, s.active)
	}
	for _, seg := range outsiders {
		f, err := os.Open(seg.path)
		if err != nil {
			return nil, err
		}
		res, err := scanSegment(seg.id, f, seg.size, false)
		f.Close()
		if err != nil {
			return nil, err
		}
		for _, sr := range res.records {
			key := string(sr.Key)
			if !tombKeys[key] {
				continue
			}
			if cur, ok := result[key]; !ok || sr.Seq < cur {
				result[key] = sr.Seq
			}
		}
	}
	return result, nil
}

func (s *Store) hook(stage string) {
	if s.testHook != nil {
		s.testHook(stage)
	}
}
