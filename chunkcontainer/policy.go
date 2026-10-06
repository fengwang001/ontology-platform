package chunkcontainer

import (
	"bytes"
	"hash/crc32"
)

type Compressor interface {
	Compress(data []byte) ([]byte, bool)
}

type Decompressor interface {
	Decompress(data []byte) ([]byte, bool)
}

type Config struct {
	BlockSize     int
	MinimumGain   int
	CacheCapacity int
}

type BlockDecision struct {
	Index          int
	Form           StorageForm
	Reason         StoreReason
	OriginalLength int
	StoredLength   int
	Checksum       uint32
}

type AppendResult struct {
	Blocks []BlockDecision
}

type ReadResult struct {
	Data []byte
	EOF  bool
}

func decideBlock(data []byte, minimumGain int, compressor Compressor) blockRecord {
	original := bytes.Clone(data)
	checksum := crc32.ChecksumIEEE(original)
	record := blockRecord{
		form:       FormDirect,
		reason:     ReasonNone,
		original:   original,
		stored:     original,
		checksum:   checksum,
		origLength: len(original),
	}

	compressed, ok := compressor.Compress(bytes.Clone(original))
	if !ok {
		record.reason = ReasonCompressionFailed
		return record
	}

	if len(compressed) > len(original)-minimumGain {
		record.reason = ReasonInsufficientGain
		return record
	}

	record.form = FormCompressed
	record.reason = ReasonNone
	record.stored = bytes.Clone(compressed)
	return record
}

func decisionForBlock(index int, record blockRecord) BlockDecision {
	return BlockDecision{
		Index:          index,
		Form:           record.form,
		Reason:         record.reason,
		OriginalLength: record.origLength,
		StoredLength:   len(record.stored),
		Checksum:       record.checksum,
	}
}
