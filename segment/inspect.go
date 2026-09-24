package segment

import (
	"encoding/binary"
	"hash/crc32"
	"os"

	"ontology/event"
)

func readHeader(f *os.File, size int64) (Header, error) {
	if size < int64(HeaderSize) {
		return Header{}, ErrHeaderIncomplete
	}
	hdr := make([]byte, HeaderSize)
	if n, err := f.ReadAt(hdr, 0); err != nil || n < HeaderSize {
		return Header{}, ErrHeaderIncomplete
	}
	if string(hdr[:4]) != magic {
		return Header{}, ErrHeaderIncomplete
	}
	return Header{
		FirstSeq: binary.LittleEndian.Uint64(hdr[8:16]),
		Count:    binary.LittleEndian.Uint64(hdr[16:24]),
	}, nil
}

// Inspect 按当前文件内容分类损坏类型；完整合法返回 nil。
func Inspect(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	if _, err := readHeader(f, size); err != nil {
		return err
	}
	off := int64(HeaderSize)
	for off < size {
		var lb [4]byte
		if n, _ := f.ReadAt(lb[:], off); n < 4 {
			return ErrLenPrefixIncomplete
		}
		bodyLen := int64(binary.LittleEndian.Uint32(lb[:]))
		if size-off < 4+bodyLen+4 {
			return ErrBodyIncomplete
		}
		buf := make([]byte, bodyLen+4)
		if _, err := f.ReadAt(buf, off+4); err != nil {
			return ErrBodyIncomplete
		}
		got := binary.LittleEndian.Uint32(buf[bodyLen:])
		chk := crc32.Checksum(append(lb[:], buf[:bodyLen]...), crcTable)
		if got != chk {
			return ErrCRC
		}
		off += 4 + bodyLen + 4
	}
	return nil
}

// ScanPrefix 返回段头与最大可恢复事件前缀及其文件末尾偏移。
func ScanPrefix(path string) (Header, []event.Event, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, nil, 0, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	size := fi.Size()
	hdr, err := readHeader(f, size)
	if err != nil {
		return Header{}, nil, 0, err
	}
	var evs []event.Event
	off := int64(HeaderSize)
	for off < size {
		var lb [4]byte
		if n, _ := f.ReadAt(lb[:], off); n < 4 {
			break
		}
		bodyLen := int64(binary.LittleEndian.Uint32(lb[:]))
		if size-off < 4+bodyLen+4 {
			break
		}
		buf := make([]byte, bodyLen+4)
		if _, err := f.ReadAt(buf, off+4); err != nil {
			break
		}
		got := binary.LittleEndian.Uint32(buf[bodyLen:])
		chk := crc32.Checksum(append(lb[:], buf[:bodyLen]...), crcTable)
		if got != chk {
			break
		}
		ev, _, derr := event.Decode(buf[:bodyLen])
		if derr != nil {
			break
		}
		evs = append(evs, ev)
		off += 4 + bodyLen + 4
	}
	return hdr, evs, off, nil
}

// Rewrite 把段头 count 改写为 n 并截断到 end 偏移。
func Rewrite(path string, n uint64, end int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	if _, err := f.WriteAt(b[:], 16); err != nil {
		return err
	}
	return f.Truncate(end)
}
