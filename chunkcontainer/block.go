package chunkcontainer

import "encoding/binary"

type StorageForm uint8
type StoreReason uint8

const (
	FormCompressed StorageForm = iota + 1
	FormDirect
)

const (
	ReasonNone StoreReason = iota
	ReasonCompressionFailed
	ReasonInsufficientGain
)

const HeaderSize = 21

type blockRecord struct {
	form       StorageForm
	reason     StoreReason
	original   []byte
	stored     []byte
	checksum   uint32
	origLength int
}

func encodeHeader(record blockRecord) []byte {
	header := make([]byte, HeaderSize)
	header[0] = byte(record.form)
	binary.BigEndian.PutUint64(header[1:9], uint64(len(record.stored)))
	binary.BigEndian.PutUint64(header[9:17], uint64(record.origLength))
	binary.BigEndian.PutUint32(header[17:21], record.checksum)
	return header
}

func parseHeader(header []byte) blockRecord {
	return blockRecord{
		form:       StorageForm(header[0]),
		stored:     make([]byte, binary.BigEndian.Uint64(header[1:9])),
		origLength: int(binary.BigEndian.Uint64(header[9:17])),
		checksum:   binary.BigEndian.Uint32(header[17:21]),
	}
}
