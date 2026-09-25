package export

import (
	"io"
	"os"

	"hash/crc32"
)

type chunkInfo struct {
	index  int
	offset int64
	length int64
	crc    uint32
}

// scanChunks 从头解析所有完整块；遇到截断点返回对应分类错误。
func scanChunks(f *os.File, size int) (int64, []chunkInfo, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, nil, err
	}
	total := st.Size()
	var off int64
	var infos []chunkInfo
	expected := 0
	for off < total {
		if total-off < chunkHeader {
			return off, infos, chunkErr(expected, ErrChunkHeader)
		}
		hdr := make([]byte, chunkHeader)
		if _, err := f.ReadAt(hdr, off); err != nil {
			return off, infos, err
		}
		if string(hdr[0:4]) != chunkMagic ||
			crc32.Checksum(hdr[:12], crcTable) != getUint32(hdr[12:]) {
			return off, infos, chunkErr(expected, ErrChunkHeader)
		}
		idx := int(getUint32(hdr[4:]))
		bodyLen := int64(getUint32(hdr[8:]))
		if bodyLen > int64(size) {
			return off, infos, chunkErr(idx, ErrChunkHeader)
		}
		if idx != expected {
			return off, infos, chunkErr(idx, ErrChunkOrder)
		}
		end := off + chunkHeader + bodyLen
		if end > total {
			return off, infos, chunkErr(idx, ErrChunkBody)
		}
		body := make([]byte, bodyLen)
		if _, err := f.ReadAt(body, off+chunkHeader); err != nil && err != io.EOF {
			return off, infos, err
		}
		infos = append(infos, chunkInfo{index: idx, offset: off, length: bodyLen, crc: crc32.Checksum(body, crcTable)})
		off = end
		expected++
	}
	return off, infos, nil
}

// chunkBoundary 返回第 k 块开头的字节偏移；不存在返回 -1。
func chunkBoundary(f *os.File, size, k int) int64 {
	_, infos, err := scanChunks(f, size)
	if err != nil {
		return -1
	}
	for _, ci := range infos {
		if ci.index == k {
			return ci.offset
		}
	}
	return -1
}
