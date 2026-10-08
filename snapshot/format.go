// Package snapshot implements corruption detection and partial recovery
// for ontology platform snapshot files.
//
// A snapshot file is organized into sections: object-type instances, link
// instances and action execution records. The file header carries a section
// directory with absolute offsets so that locating the recoverable prefix of
// one section costs work proportional to the corruption position inside that
// section, not to the total file size.
//
// Binary layout (all integers little-endian):
//
//	File:
//	  magic        uint32        "ONTS" (0x4F4E5453)
//	  version      uint16        currently 1
//	  sectionCount uint16
//	  dirCRC       uint32        CRC32 over magic..directory entries
//	  directory    sectionCount x dirEntry
//	  sections...                at directory-declared offsets
//
//	dirEntry (12 bytes):
//	  sectionType  uint8
//	  _pad         [3]byte
//	  offset       uint32        absolute file offset of the section
//	  length       uint32        section size in bytes (header + records)
//
//	Section:
//	  declaredCount uint32       record count claimed by the writer
//	  headerCRC     uint32       CRC32 over sectionType || declaredCount
//	  records...
//
//	Record:
//	  recLen  uint32             payload length in bytes
//	  payload [recLen]byte       JSON document
//	  recCRC  uint32             CRC32 over recLen(LE) || payload
package snapshot

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
)

// SectionType identifies the kind of records a snapshot section holds.
type SectionType uint8

const (
	SectionObjects SectionType = 1
	SectionLinks   SectionType = 2
	SectionActions SectionType = 3
)

func (s SectionType) String() string {
	switch s {
	case SectionObjects:
		return "objects"
	case SectionLinks:
		return "links"
	case SectionActions:
		return "actions"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// sectionOrder is the canonical processing order of sections.
var sectionOrder = []SectionType{SectionObjects, SectionLinks, SectionActions}

const (
	magicNumber   uint32 = 0x4F4E5453 // "ONTS"
	formatVersion uint16 = 1

	fileHeaderFixedSize = 4 + 2 + 2 + 4 // magic + version + count + dirCRC
	dirEntrySize        = 12            // type(1) + pad(3) + offset(4) + length(4)
	sectionHeaderSize   = 8             // declaredCount(4) + headerCRC(4)
	recordHeaderSize    = 4             // recLen
	recordTrailerSize   = 4             // recCRC
)

// Format-level errors. These describe why a file or record cannot be parsed;
// recovery maps them onto diagnostics.
var (
	ErrBadMagic         = errors.New("snapshot: bad magic number")
	ErrBadVersion       = errors.New("snapshot: unsupported format version")
	ErrTruncated        = errors.New("snapshot: unexpected end of data")
	ErrDirectoryCRC     = errors.New("snapshot: section directory checksum mismatch")
	ErrDuplicateSection = errors.New("snapshot: duplicate section type in directory")
	ErrSectionHeaderCRC = errors.New("snapshot: section header checksum mismatch")
	ErrRecordCRC        = errors.New("snapshot: record checksum mismatch")
	ErrRecordOversized  = errors.New("snapshot: record extends past section boundary")
)

// ObjectRecord is one object-type instance record.
type ObjectRecord struct {
	RID      string            `json:"rid"`
	ObjectID string            `json:"objectId"`
	TypeID   string            `json:"typeId"`
	Props    map[string]string `json:"props,omitempty"`
}

// LinkRecord is one link instance record referencing two objects.
type LinkRecord struct {
	RID    string `json:"rid"`
	LinkID string `json:"linkId"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// ActionRecord is one action execution record. A record is only usable as a
// whole: if any of the objects or links it modifies is lost, the entire
// record must be discarded.
type ActionRecord struct {
	RID      string   `json:"rid"`
	ActionID string   `json:"actionId"`
	Objects  []string `json:"objects,omitempty"`
	Links    []string `json:"links,omitempty"`
}

func (r *ObjectRecord) valid() error {
	if r.RID == "" || r.ObjectID == "" || r.TypeID == "" {
		return errors.New("snapshot: object record missing required field")
	}
	return nil
}

func (r *LinkRecord) valid() error {
	if r.RID == "" || r.LinkID == "" || r.From == "" || r.To == "" {
		return errors.New("snapshot: link record missing required field")
	}
	return nil
}

func (r *ActionRecord) valid() error {
	if r.RID == "" || r.ActionID == "" {
		return errors.New("snapshot: action record missing required field")
	}
	return nil
}

// dirEntry is one parsed section directory entry.
type dirEntry struct {
	secType SectionType
	offset  uint32
	length  uint32
}

// parseDirectory validates the file header and returns the section directory
// keyed by section type. A directory failure is unrecoverable for the whole
// file because section boundaries can no longer be trusted.
func parseDirectory(data []byte) (map[SectionType]dirEntry, error) {
	if len(data) < fileHeaderFixedSize {
		return nil, ErrTruncated
	}
	if binary.LittleEndian.Uint32(data[0:4]) != magicNumber {
		return nil, ErrBadMagic
	}
	if binary.LittleEndian.Uint16(data[4:6]) != formatVersion {
		return nil, ErrBadVersion
	}
	count := int(binary.LittleEndian.Uint16(data[6:8]))
	dirEnd := fileHeaderFixedSize + count*dirEntrySize
	if len(data) < dirEnd {
		return nil, ErrTruncated
	}
	if crc32.ChecksumIEEE(data[8:dirEnd]) != binary.LittleEndian.Uint32(data[8:12]) {
		return nil, ErrDirectoryCRC
	}
	entries := make(map[SectionType]dirEntry, count)
	for i := 0; i < count; i++ {
		base := fileHeaderFixedSize + i*dirEntrySize
		e := dirEntry{
			secType: SectionType(data[base]),
			offset:  binary.LittleEndian.Uint32(data[base+4 : base+8]),
			length:  binary.LittleEndian.Uint32(data[base+8 : base+12]),
		}
		if _, dup := entries[e.secType]; dup {
			return nil, ErrDuplicateSection
		}
		entries[e.secType] = e
	}
	return entries, nil
}

// decodeRecordAt reads one record frame starting at off, which must end at or
// before limit. It returns the payload and the offset just past the record.
func decodeRecordAt(data []byte, off, limit int) (payload []byte, next int, err error) {
	if off+recordHeaderSize > limit {
		return nil, 0, ErrTruncated
	}
	recLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
	end := off + recordHeaderSize + recLen + recordTrailerSize
	if end > limit {
		return nil, 0, ErrRecordOversized
	}
	stored := binary.LittleEndian.Uint32(data[end-recordTrailerSize : end])
	if crc32.ChecksumIEEE(data[off:end-recordTrailerSize]) != stored {
		return nil, 0, ErrRecordCRC
	}
	return data[off+recordHeaderSize : end-recordTrailerSize], end, nil
}

// encodeRecord frames one payload with its length prefix and CRC.
func encodeRecord(payload []byte) []byte {
	buf := make([]byte, 0, recordHeaderSize+len(payload)+recordTrailerSize)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, payload...)
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(buf))
	return append(buf, crcBuf[:]...)
}

// encodeSectionHeader builds the 8-byte section header.
func encodeSectionHeader(secType SectionType, declaredCount uint32) []byte {
	buf := make([]byte, sectionHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], declaredCount)
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(buf[0:4]))
	copy(buf[4:8], crcBuf[:])
	return buf
}

// verifySectionHeader checks the section header CRC and returns the declared
// record count.
func verifySectionHeader(data []byte, e dirEntry) (declared int, err error) {
	off := int(e.offset)
	header := data[off : off+sectionHeaderSize]
	declaredCount := binary.LittleEndian.Uint32(header[0:4])
	if crc32.ChecksumIEEE(header[0:4]) != binary.LittleEndian.Uint32(header[4:8]) {
		return 0, ErrSectionHeaderCRC
	}
	return int(declaredCount), nil
}

// marshalRecord is the canonical JSON encoding of a record payload.
func marshalRecord(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("snapshot: marshal record: %v", err))
	}
	return b
}
