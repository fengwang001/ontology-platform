package export

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/manifest"
)

// Resume 凭清单续传：自动探测第一个缺失/不完整块；快照过期则拒绝且保留已导出部分。
// wantK>=0 时强制从第 wantK 块续（测试逐 k 使用）。
func Resume(opts Options, wantK int) (*Exporter, error) {
	if opts.BlockSize <= 0 {
		return nil, ErrBlockSize
	}
	if !opts.Snapshot.Acquire() {
		return nil, ErrSnapshotExpired // 已导出部分不删除
	}
	defer opts.Snapshot.Release()

	man, err := manifest.Load(opts.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrManifestIncomplete
	}
	if err != nil {
		return nil, err
	}
	path := filepath.Join(opts.Dir, DataName)
	k, err := validatePrefix(path, man.Blocks)
	if err != nil {
		return nil, err
	}
	if wantK >= 0 {
		k = wantK
	}

	keys, err := opts.Snapshot.Keys(opts.Order)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cut := int64(0)
	if k > 0 {
		prev, ok := man.Blocks[k-1]
		if !ok {
			return nil, fmt.Errorf("%w: block %d", ErrBlockOrder, k-1)
		}
		cut = prev.Offset + int64(prev.Length)
	}
	if _, err := f.Seek(cut, 0); err != nil {
		return nil, err
	}
	if err := f.Truncate(cut); err != nil {
		return nil, err
	}
	// 单次重走确定性分块：块 0..k-1 丢弃（仅重建块表/校验和/峰值），
	// 块 k.. 追加到截断后的数据文件，保证与一次性导出逐字节相同。
	e := &Exporter{opts: opts, man: manifest.New(man.Version, man.BlockSize, man.Records)}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	e.sink = func(no int) io.Writer {
		if no < k {
			return null
		}
		return f
	}
	if err := e.stream(keys, k, int(cut)); err != nil {
		_ = manifest.Save(opts.Dir, e.man)
		return e, err
	}
	e.man.Finish()
	return e, manifest.Save(opts.Dir, e.man)
}

// validatePrefix 顺序校验已落盘块，返回第一个缺失/不完整块的块号。
// 乱序或删除块返回包装 ErrBlockOrder 并指名块号。
func validatePrefix(path string, blocks map[int]manifest.BlockInfo) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	off := 0
	for no := 0; ; no++ {
		info, listed := blocks[no]
		if off == len(data) {
			if listed {
				return no, nil // 该块在清单中但数据缺失
			}
			return no, nil
		}
		if !listed {
			return no, fmt.Errorf("%w: unexpected block %d", ErrBlockOrder, no)
		}
		if err := checkOne(data, off, uint64(no), info); err != nil {
			return no, err
		}
		off += info.Length
	}
}

func checkOne(data []byte, off int, wantNo uint64, info manifest.BlockInfo) error {
	if len(data)-off < hdrLen {
		return ErrBlockHeader
	}
	if string(data[off:off+len(magic)]) != magic {
		return ErrBlockHeader
	}
	no := binary.BigEndian.Uint64(data[off+len(magic):])
	if no != wantNo {
		return fmt.Errorf("%w: block %d at slot expecting %d", ErrBlockOrder, no, wantNo)
	}
	bodyLen := int(binary.BigEndian.Uint64(data[off+len(magic)+8:]))
	end := off + hdrLen + bodyLen
	if len(data) < end {
		return ErrBlockBody
	}
	if len(data) < end+trailer {
		return ErrCRC // CRC 尾被截断：无法通过校验
	}
	got := binary.BigEndian.Uint32(data[end : end+trailer])
	want := crc32.ChecksumIEEE(data[off+len(magic) : end])
	if got != want {
		return ErrCRC
	}
	return nil
}

// Classify 对“分块文件在 length 处被截断 + 清单状态”给出四类可判定错误。
func Classify(data []byte, blocks map[int]manifest.BlockInfo, manifestOK bool) error {
	if !manifestOK {
		return ErrManifestIncomplete
	}
	if _, err := validatePrefixBytes(data, blocks); err != nil {
		return err
	}
	return nil
}

func validatePrefixBytes(data []byte, blocks map[int]manifest.BlockInfo) (int, error) {
	off := 0
	for no := 0; ; no++ {
		info, listed := blocks[no]
		if off == len(data) {
			if listed {
				return no, fmt.Errorf("%w: block %d", ErrBlockOrder, no)
			}
			return no, nil
		}
		if !listed {
			return no, fmt.Errorf("%w: unexpected block %d", ErrBlockOrder, no)
		}
		if err := checkOne(data, off, uint64(no), info); err != nil {
			return no, err
		}
		off += info.Length
	}
}
