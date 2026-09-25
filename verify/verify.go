package verify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/export"
	"ontology/manifest"
)

var errBody = export.ErrBlockBody

// Result 是一次完整性校验的结果。
type Result struct {
	Blocks   int
	Checksum []byte
}

// File 校验导出目录：清单存在、块连续有序且 CRC 正确、总校验和与清单一致。
func File(dir string) (Result, error) {
	man, err := manifest.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, export.ErrManifestIncomplete
	}
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, export.DataName))
	if err != nil {
		return Result{}, err
	}
	n, err := scan(data, man.Blocks)
	if err != nil {
		return Result{}, err
	}
	if n != len(man.Blocks) {
		return Result{}, fmt.Errorf("%w: block %d", export.ErrBlockOrder, n)
	}
	got, err := Checksum(data, man.Blocks)
	if err != nil {
		return Result{}, err
	}
	if string(got) != string(man.Checksum) {
		return Result{}, fmt.Errorf("%w: total checksum", export.ErrCRC)
	}
	return Result{Blocks: n, Checksum: man.Checksum}, nil
}

// Checksum 独立扫描分块文件，解码记录并重算 XOR 折叠总校验和。
func Checksum(data []byte, blocks map[int]manifest.BlockInfo) ([]byte, error) {
	sum := make([]byte, 0)
	off := 0
	for no := 0; no < len(blocks); no++ {
		info, ok := blocks[no]
		if !ok {
			return nil, fmt.Errorf("%w: missing %d", export.ErrBlockOrder, no)
		}
		body, err := bodyAt(data, off, uint64(no), info)
		if err != nil {
			return nil, err
		}
		recs, err := decodeRecords(body)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			sum = xorDigest(sum, r)
		}
		off += info.Length
	}
	return sum, nil
}

// scan 顺序检查魔数/块号/体完整性/CRC，并要求块号严格连续。
func scan(data []byte, blocks map[int]manifest.BlockInfo) (int, error) {
	off := 0
	for no := 0; ; no++ {
		info, listed := blocks[no]
		if off == len(data) {
			if listed {
				return no, fmt.Errorf("%w: missing block %d", export.ErrBlockOrder, no)
			}
			return no, nil
		}
		if !listed {
			return no, fmt.Errorf("%w: unexpected block %d", export.ErrBlockOrder, no)
		}
		if _, err := bodyAt(data, off, uint64(no), info); err != nil {
			return no, err
		}
		off += info.Length
	}
}

func bodyAt(data []byte, off int, wantNo uint64, info manifest.BlockInfo) ([]byte, error) {
	if len(data)-off < 24 || string(data[off:off+8]) != export.Magic() {
		return nil, export.ErrBlockHeader
	}
	if binary.BigEndian.Uint64(data[off+8:]) != wantNo {
		return nil, fmt.Errorf("%w: slot %d wrong number", export.ErrBlockOrder, wantNo)
	}
	bodyLen := int(binary.BigEndian.Uint64(data[off+16:]))
	end := off + 24 + bodyLen
	if len(data) < end {
		return nil, export.ErrBlockBody
	}
	if len(data) < end+4 {
		return nil, export.ErrCRC
	}
	got := binary.BigEndian.Uint32(data[end : end+4])
	if crc32.ChecksumIEEE(data[off+8:end]) != got {
		return nil, export.ErrCRC
	}
	return data[off+24 : end], nil
}
