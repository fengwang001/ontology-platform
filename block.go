package blockstore

import (
	"encoding/binary"
	"hash/fnv"
)

const headerSize = 25

type blockRecord struct {
	physical []byte
	reason   FallbackReason
}

func checksum(data []byte) uint64 {
	sum := fnv.New64a()
	_, _ = sum.Write(data)
	return sum.Sum64()
}

func encodeBlock(raw []byte, minGain int, compressor Compressor) *blockRecord {
	originalLength := len(raw)
	sum := checksum(raw)
	compressed, ok := compressor.Compress(raw)

	form := StoredCompressed
	payload := compressed
	reason := NoFallback
	if !ok {
		form = StoredRaw
		payload = raw
		reason = CompressionFailed
	} else if int64(minGain) > int64(originalLength)-int64(len(compressed)) {
		form = StoredRaw
		payload = raw
		reason = InsufficientGain
	}

	physical := make([]byte, headerSize+len(payload))
	putHeader(physical[:headerSize], form, len(payload), originalLength, sum)
	copy(physical[headerSize:], payload)
	return &blockRecord{physical: physical, reason: reason}
}

func decodeBlock(rec *blockRecord, index int, decompressor Decompressor) ([]byte, StorageForm, FallbackReason, error) {
	if len(rec.physical) < headerSize {
		return nil, 0, rec.reason, blockError("read", index, LengthMismatch)
	}

	form := headerForm(rec)
	storedLength := headerStoredLength(rec)
	originalLength := headerOriginalLength(rec)
	expectedChecksum := headerChecksum(rec)
	if storedLength != len(rec.physical)-headerSize {
		return nil, form, rec.reason, blockError("read", index, LengthMismatch)
	}

	payload := rec.physical[headerSize:]
	var decoded []byte
	switch form {
	case StoredRaw:
		decoded = append([]byte(nil), payload...)
	case StoredCompressed:
		data, ok := decompressor.Decompress(payload)
		if !ok {
			return nil, form, rec.reason, blockError("read", index, DecompressFailed)
		}
		decoded = append([]byte(nil), data...)
	default:
		return nil, form, rec.reason, blockError("read", index, DecompressFailed)
	}

	if len(decoded) != originalLength {
		return nil, form, rec.reason, blockError("read", index, LengthMismatch)
	}
	if checksum(decoded) != expectedChecksum {
		return nil, form, rec.reason, blockError("read", index, ChecksumMismatch)
	}
	return decoded, form, rec.reason, nil
}

func headerForm(rec *blockRecord) StorageForm { return StorageForm(rec.physical[0]) }

func headerStoredLength(rec *blockRecord) int {
	return int(binary.BigEndian.Uint64(rec.physical[1:9]))
}

func headerOriginalLength(rec *blockRecord) int {
	return int(binary.BigEndian.Uint64(rec.physical[9:17]))
}

func headerChecksum(rec *blockRecord) uint64 {
	return binary.BigEndian.Uint64(rec.physical[17:25])
}

func putHeader(dst []byte, form StorageForm, storedLength, originalLength int, sum uint64) {
	dst[0] = byte(form)
	binary.BigEndian.PutUint64(dst[1:9], uint64(storedLength))
	binary.BigEndian.PutUint64(dst[9:17], uint64(originalLength))
	binary.BigEndian.PutUint64(dst[17:25], sum)
}
