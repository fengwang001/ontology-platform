package export

import (
	"errors"
	"os"
	"path/filepath"

	"ontology/manifest"
	"ontology/snapshot"
)

// Result 报告一次导出结果。
type Result struct {
	Chunks    int
	TotalSum  uint64
	PeakChunk int
}

// Export 把快照一次性导出到 opts.Dir。
func Export(snap *snapshot.Snapshot, opts Options) (*Result, error) {
	if opts.ChunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	if err := snap.ExportBegin(); err != nil {
		return nil, ErrSnapshotExpired
	}
	defer snap.ExportEnd()
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	keys := orderedKeys(snap, opts.Reverse)
	m := &manifest.Manifest{SnapVersion: snap.Version(), EntryCount: uint64(len(keys)), ChunkSize: uint64(opts.ChunkSize)}
	if err := writePending(opts.Dir, m); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(opts.Dir, DataName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := newChunker(f, opts.ChunkSize)
	sum, err := feed(snap, keys, c, 0)
	if err != nil {
		return nil, err
	}
	if c.off > 0 || c.index == 0 {
		if err := c.flush(); err != nil { // 空存储也产生一个空体单块
			return nil, err
		}
	}
	m.ChunkCount = uint32(len(c.crcs))
	m.ChunkCRC = c.crcs
	m.TotalSum = sum
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := writeFinal(opts.Dir, m); err != nil {
		return nil, err
	}
	return &Result{Chunks: c.index, TotalSum: sum, PeakChunk: c.peak}, nil
}

func feed(snap *snapshot.Snapshot, keys []string, c *chunker, skip int) (uint64, error) {
	var sum uint64
	skipped := 0
	for _, key := range keys {
		val, err := snap.Get(key)
		if err != nil {
			if errors.Is(err, snapshot.ErrClosed) {
				return 0, ErrSnapshotExpired
			}
			return 0, err
		}
		sum = addSum(sum, entryHash(key, val))
		frm := frame(key, val)
		if skipped < skip {
			take := len(frm)
			if skipped+take > skip {
				take = skip - skipped
				if err := c.write(frm[take:]); err != nil {
					return 0, err
				}
			}
			skipped += len(frm)
			continue
		}
		if err := c.write(frm); err != nil {
			return 0, err
		}
	}
	return sum, nil
}

// Resume 从第 k 块（0 基）续传；快照过期时返回 ErrSnapshotExpired 且不动已导出文件。
func Resume(snap *snapshot.Snapshot, opts Options, k int) (*Result, error) {
	if opts.ChunkSize <= 0 {
		return nil, ErrInvalidChunkSize
	}
	raw, err := os.ReadFile(filepath.Join(opts.Dir, ManifestName))
	if err != nil {
		return nil, ErrManifestIncomplete
	}
	m, err := manifest.Unmarshal(raw)
	if err != nil || m.Done {
		return nil, ErrManifestIncomplete
	}
	if snap.Closed() || snap.Version() != m.SnapVersion {
		return nil, ErrSnapshotExpired // 先判定，绝不截断已有数据
	}
	if err := snap.ExportBegin(); err != nil {
		return nil, ErrSnapshotExpired
	}
	defer snap.ExportEnd()

	f, err := os.OpenFile(filepath.Join(opts.Dir, DataName), os.O_RDWR, 0o644)
	if err != nil {
		return nil, ErrChunkBody
	}
	defer f.Close()
	end, infos, err := scanChunks(f, int(m.ChunkSize))
	if err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, ErrChunkMissing
	}
	cut := int64(0)
	switch {
	case k == len(infos):
		cut = end
	case k > 0:
		cut = chunkBoundary(f, int(m.ChunkSize), k)
	}
	if cut < 0 || k > len(infos) {
		return nil, chunkErr(k, ErrChunkMissing)
	}
	prefixCRC := make([]uint32, 0, k)
	for i := 0; i < k; i++ {
		if i >= len(infos) || infos[i].index != i {
			return nil, chunkErr(i, ErrChunkMissing)
		}
		prefixCRC = append(prefixCRC, infos[i].crc)
	}
	if err := f.Truncate(cut); err != nil {
		return nil, err
	}
	if _, err := f.Seek(cut, 0); err != nil {
		return nil, err
	}
	keys := orderedKeys(snap, opts.Reverse)
	c := newChunkerAt(f, int(m.ChunkSize), k)
	c.crcs = prefixCRC
	sum, err := feed(snap, keys, c, k*int(m.ChunkSize))
	if err != nil {
		return nil, err
	}
	if c.off > 0 {
		if err := c.flush(); err != nil {
			return nil, err
		}
	}
	m.ChunkCount = uint32(c.index)
	m.ChunkCRC = c.crcs
	m.TotalSum = sum
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := writeFinal(opts.Dir, m); err != nil {
		return nil, err
	}
	return &Result{Chunks: c.index, TotalSum: sum, PeakChunk: c.peak}, nil
}

func newChunkerAt(f *os.File, size, startIndex int) *chunker {
	c := newChunker(f, size)
	c.index = startIndex
	return c
}
