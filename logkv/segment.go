package logkv

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// segment 是一个日志段文件的运行时表示。
type segment struct {
	id     uint32
	path   string
	file   *os.File
	size   int64
	sealed bool
}

func segmentPath(dir string, id uint32) string {
	return fmt.Sprintf("%s/%06d.seg", dir, id)
}

func hintPath(dir string, id uint32) string {
	return fmt.Sprintf("%s/%06d.hint", dir, id)
}

func mergeTmpPath(dir string, id uint32) string {
	return fmt.Sprintf("%s/%06d.mseg.tmp", dir, id)
}

func mergeMetaPath(dir string, id uint32) string {
	return fmt.Sprintf("%s/%06d.mmeta", dir, id)
}

func openSegment(dir string, id uint32, sealed bool) (*segment, error) {
	path := segmentPath(dir, id)
	flag := os.O_RDONLY
	if !sealed {
		flag = os.O_CREATE | os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &segment{id: id, path: path, file: f, size: st.Size(), sealed: sealed}, nil
}

func (s *segment) close() {
	if s.file != nil {
		s.file.Close()
	}
}

// scannedRecord 是扫描得到的一条记录及其位置。
type scannedRecord struct {
	Record
	Offset int64
	Length uint32 // 整条记录的字节数
}

// scanResult 是一次段扫描的结果。
type scanResult struct {
	records    []scannedRecord
	validBytes int64 // 段内有效字节数（最后一条完整记录之后）
	tornBytes  int64 // 被判定为撕裂尾部而丢弃的字节数
}

// scanSegment 流式扫描段内容。allowTorn 仅对活动段为真：
// 此时尾部头不完整、声明长度越过段末、或最后一条记录校验失败，
// 都视为撕裂尾部并截断；其余任何位置的记录无效都报段损坏。
// 已封口段（allowTorn=false）中任何无效记录都是段损坏。
func scanSegment(segID uint32, r io.Reader, fileSize int64, allowTorn bool) (scanResult, error) {
	var res scanResult
	br := bufio.NewReaderSize(r, 64*1024)
	off := int64(0)
	for off < fileSize {
		remaining := fileSize - off
		if remaining < recordHeaderSize {
			// 头不完整。
			if allowTorn {
				res.validBytes = off
				res.tornBytes = remaining
				return res, nil
			}
			return res, corruptError(segID, off, "trailing bytes smaller than record header")
		}
		headBuf := make([]byte, recordHeaderSize)
		_, err := io.ReadFull(br, headBuf)
		if err != nil {
			return res, corruptError(segID, off, "short read on record header")
		}
		h := parseRecordHeader(headBuf)
		if !h.consistent() {
			// 头部字段不自洽，等价于校验失败；只有它恰为
			// 最后一条记录（延伸到段末）时才算撕裂尾部。
			if allowTorn && h.total() >= remaining {
				res.validBytes = off
				res.tornBytes = remaining
				return res, nil
			}
			return res, corruptError(segID, off, "inconsistent record header")
		}
		total := h.total()
		if total > remaining {
			// 声明长度越过段末。
			if allowTorn {
				res.validBytes = off
				res.tornBytes = remaining
				return res, nil
			}
			return res, corruptError(segID, off, "record length past end of segment")
		}
		rest := make([]byte, int(total)-recordHeaderSize)
		_, err = io.ReadFull(br, rest)
		if err != nil {
			return res, corruptError(segID, off, "short read on record body")
		}
		rec, ok := decodeRecord(append(headBuf, rest...))
		if !ok {
			// 校验失败：恰为最后一条才算撕裂尾部。
			if allowTorn && total == remaining {
				res.validBytes = off
				res.tornBytes = remaining
				return res, nil
			}
			return res, corruptError(segID, off, "record checksum mismatch")
		}
		res.records = append(res.records, scannedRecord{
			Record: rec,
			Offset: off,
			Length: uint32(total),
		})
		off += total
	}
	res.validBytes = off
	return res, nil
}

// readRecordAt 从已打开的文件读取指定位置、指定长度的一条记录。
func readRecordAt(f *os.File, offset int64, length uint32) (Record, error) {
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return Record{}, err
	}
	if len(buf) < recordHeaderSize {
		return Record{}, errShortRecord
	}
	h := parseRecordHeader(buf)
	if !h.consistent() || h.total() != int64(length) {
		return Record{}, errShortRecord
	}
	rec, ok := decodeRecord(buf)
	if !ok {
		return Record{}, errChecksum
	}
	return rec, nil
}

var (
	errShortRecord = newError(KindSegmentCorruption, "record shorter than declared")
	errChecksum    = newError(KindSegmentCorruption, "record checksum mismatch")
)
