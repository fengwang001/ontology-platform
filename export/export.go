package export

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/manifest"
	"ontology/snapshot"
)

var (
	ErrBlockSize          = errors.New("invalid block size")
	ErrSnapshotExpired    = errors.New("snapshot expired")
	ErrManifestIncomplete = errors.New("manifest incomplete")
	ErrBlockHeader        = errors.New("block header incomplete or bad magic")
	ErrBlockBody          = errors.New("block body incomplete")
	ErrBlockOrder         = errors.New("block missing or out of order")
	ErrCRC                = errors.New("crc mismatch")
)

const (
	magic    = "ONTOBLK"
	hdrLen   = len(magic) + 16
	trailer  = 4
	DataName = "snapshot.blocks"
)

// Magic 返回分块魔数，供校验方识别块头。
func Magic() string { return magic }

// Checkpoint 在导出到 lastDone 块之后回调；返回 error 模拟中断。
type Checkpoint func(lastDone int) error

type Options struct {
	Dir       string
	Snapshot  *snapshot.Handle
	Order     snapshot.Order
	BlockSize int
	Stop      Checkpoint
}

// Exporter 持有导出状态与单块驻留峰值计数。
type Exporter struct {
	opts      Options
	man       manifest.Manifest
	peakBlock int
	sink      func(blockNo int) io.Writer
}

func (e *Exporter) PeakBlockBytes() int         { return e.peakBlock }
func (e *Exporter) Manifest() manifest.Manifest { return e.man }

// Run 完整导出或在 Stop 处中断（中断也会保存增量清单，供续传）。
func Run(opts Options) (*Exporter, error) {
	if opts.BlockSize <= 0 {
		return nil, ErrBlockSize
	}
	if !opts.Snapshot.Acquire() {
		return nil, ErrSnapshotExpired
	}
	defer opts.Snapshot.Release()

	keys, err := opts.Snapshot.Keys(opts.Order)
	if err != nil {
		return nil, err
	}
	man := manifest.New(opts.Snapshot.Version(), opts.BlockSize, len(keys))
	f, err := os.Create(filepath.Join(opts.Dir, DataName))
	if err != nil {
		return nil, err
	}
	e := &Exporter{opts: opts, man: man}
	e.sink = func(int) io.Writer { return f }
	err = e.stream(keys, 0, 0)
	f.Close()
	if cerr := manifest.Save(opts.Dir, e.man); cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return e, err
	}
	e.man.Finish()
	if err := manifest.Save(opts.Dir, e.man); err != nil {
		return nil, err
	}
	return e, nil
}

// stream 确定性地切分记录并写块；块号 < startBlock 的块按 sink 路由（续传时丢弃），
// 其余块从 startOffset 开始计入块表。
func (e *Exporter) stream(keys []string, startBlock, startOffset int) error {
	body := make([]byte, 0, e.opts.BlockSize)
	blockNo, offset := 0, 0
	flush := func() error {
		if len(body) == 0 {
			return nil
		}
		n, sum, err := writeBlock(e.sink(blockNo), uint64(blockNo), body)
		if err != nil {
			return err
		}
		if len(body) > e.peakBlock {
			e.peakBlock = len(body)
		}
		if blockNo >= startBlock {
			e.man.Add(blockNo, offset, n, sum)
		}
		offset += n
		if e.opts.Stop != nil {
			if err := e.opts.Stop(blockNo); err != nil {
				return err
			}
		}
		blockNo++
		body = body[:0]
		return nil
	}
	for _, key := range keys {
		value, _, err := e.opts.Snapshot.Get(key)
		if err != nil {
			return err
		}
		e.man.FoldRecord(key, value)
		rec := encodeRecord(nil, key, value)
		if len(rec) > e.opts.BlockSize && len(body) > 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		body = append(body, rec...)
		if len(body) >= e.opts.BlockSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func encodeRecord(dst []byte, key string, value []byte) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(key)))
	dst = append(dst, buf[:n]...)
	dst = append(dst, key...)
	n = binary.PutUvarint(buf[:], uint64(len(value)))
	dst = append(dst, buf[:n]...)
	return append(dst, value...)
}

func writeBlock(w io.Writer, no uint64, body []byte) (int, uint32, error) {
	hdr := make([]byte, hdrLen)
	copy(hdr, magic)
	binary.BigEndian.PutUint64(hdr[len(magic):], no)
	binary.BigEndian.PutUint64(hdr[len(magic)+8:], uint64(len(body)))
	sum := crc32.ChecksumIEEE(append(hdr[len(magic):], body...))
	tail := make([]byte, trailer)
	binary.BigEndian.PutUint32(tail, sum)
	for _, p := range [][]byte{hdr, body, tail} {
		if _, err := w.Write(p); err != nil {
			return 0, 0, err
		}
	}
	return hdrLen + len(body) + trailer, sum, nil
}
