package doublewrite

import (
	"encoding/binary"
	"hash/crc32"
)

const (
	headerSize   = 16
	magicPage    = 0x50474531
	checksumSize = 4
)

// EncodePage builds a page image: magic(4) + pageNo(4) + version(8) + payload + checksum(4).
// The last four bytes hold a CRC32 over every preceding byte. Bytes of payload
// beyond pageSize-headerSize-checksumSize are rejected; shorter payload is
// zero-padded so every page image has exactly pageSize bytes.
func EncodePage(pageNo uint32, version uint64, payload []byte, pageSize int) ([]byte, error) {
	if pageSize < headerSize+checksumSize {
		return nil, ErrInvalidPage
	}
	if len(payload) > pageSize-headerSize-checksumSize {
		return nil, ErrInvalidPage
	}
	buf := make([]byte, pageSize)
	putUint32(buf[0:4], magicPage)
	putUint32(buf[4:8], pageNo)
	putUint64(buf[8:16], version)
	copy(buf[headerSize:pageSize-checksumSize], payload)
	putUint32(buf[pageSize-checksumSize:pageSize], checksum(buf[:pageSize-checksumSize]))
	return buf, nil
}

// PageInfo holds the parsed fields of one page image.
type PageInfo struct {
	PageNo  uint32
	Version uint64
}

// ParsePage validates checksum and header and returns the parsed fields.
func ParsePage(buf []byte, pageSize int) (PageInfo, []byte, error) {
	if len(buf) != pageSize || pageSize < headerSize+checksumSize {
		return PageInfo{}, nil, ErrInvalidPage
	}
	if getUint32(buf[0:4]) != magicPage {
		return PageInfo{}, nil, ErrPageCorrupt
	}
	want := getUint32(buf[pageSize-checksumSize : pageSize])
	if checksum(buf[:pageSize-checksumSize]) != want {
		return PageInfo{}, nil, ErrPageCorrupt
	}
	info := PageInfo{
		PageNo:  getUint32(buf[4:8]),
		Version: getUint64(buf[8:16]),
	}
	payload := make([]byte, pageSize-headerSize-checksumSize)
	copy(payload, buf[headerSize:pageSize-checksumSize])
	return info, payload, nil
}

func checksum(buf []byte) uint32 { return crc32.ChecksumIEEE(buf) }

func putUint64(b []byte, v uint64) { binary.BigEndian.PutUint64(b, v) }
func getUint64(b []byte) uint64    { return binary.BigEndian.Uint64(b) }
func putUint32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func getUint32(b []byte) uint32    { return binary.BigEndian.Uint32(b) }
