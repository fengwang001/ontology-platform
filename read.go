package snapshot

import (
	"hash/crc32"
	"io"
)

type headerInfo struct {
	version   uint16
	count     uint32
	regionCRC uint32
	body      []byte // raw record area
}

func parseHeader(data []byte) (headerInfo, error) {
	if len(data) < headerSize {
		return headerInfo{}, ErrHeaderTruncated
	}
	if string(data[0:magicLen]) != Magic {
		return headerInfo{}, ErrBadMagic
	}
	version := uint16At(data[magicLen : magicLen+versionLen])
	if version > CurrentVersion {
		return headerInfo{}, &VersionError{Got: version, Current: CurrentVersion, MinCompat: MinCompatibleVersion}
	}
	if version < MinCompatibleVersion {
		return headerInfo{}, &VersionError{Got: version, Current: CurrentVersion, MinCompat: MinCompatibleVersion}
	}
	count := uint32At(data[magicLen+versionLen : magicLen+versionLen+countLen])
	regionCRC := uint32At(data[magicLen+versionLen+countLen : headerSize])
	return headerInfo{version: version, count: count, regionCRC: regionCRC, body: data[headerSize:]}, nil
}

// scanFrame reads one record frame at pos. It returns the decoded record and
// the next frame position, or a RecordError describing why it failed.
func scanFrame(body []byte, pos int, index int, version uint16) (Record, int, *RecordError) {
	fail := func(err error) (Record, int, *RecordError) {
		return Record{}, pos, &RecordError{Index: index, Offset: int64(headerSize + pos), Err: err}
	}
	if len(body)-pos < lenPrefixLen {
		return fail(ErrRecordTruncated)
	}
	claimed := int(uint32At(body[pos : pos+lenPrefixLen]))
	total := lenPrefixLen + claimed + recCRCLen
	if total > len(body)-pos {
		// An implausibly large claim is a corrupt length prefix; a believable
		// claim that simply runs past the end is a mid-record truncation.
		if claimed > maxBodySize {
			return fail(ErrLengthTooLarge)
		}
		return fail(ErrRecordTruncated)
	}
	payload := body[pos+lenPrefixLen : pos+lenPrefixLen+claimed]
	gotCRC := uint32At(body[pos+lenPrefixLen+claimed : pos+total])
	if crc32.ChecksumIEEE(payload) != gotCRC {
		return fail(ErrRecordChecksum)
	}
	rec, err := decodeBody(payload, version)
	if err != nil {
		return fail(err)
	}
	return rec, pos + total, nil
}

// Read strictly decodes a snapshot. Any corruption fails the whole call and no
// partial record set is returned.
func Read(r io.Reader) ([]Record, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	hdr, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	body := hdr.body

	records := make([]Record, 0, hdr.count)
	pos := 0
	var lastErr error
	for i := 0; i < int(hdr.count); i++ {
		rec, next, rerr := scanFrame(body, pos, i, hdr.version)
		if rerr != nil {
			lastErr = rerr
			break
		}
		records = append(records, rec)
		pos = next
	}
	if lastErr != nil {
		// If the body ended exactly after the last good frame, the header
		// simply promised more records than exist. Any bytes remaining mean a
		// genuinely damaged record and the record error wins.
		if pos == len(body) {
			return nil, &CountMismatchError{Claimed: int(hdr.count), Actual: len(records)}
		}
		return nil, lastErr
	}

	// If one more complete, valid frame exists the header undercounted.
	if pos < len(body) {
		if _, _, rerr := scanFrame(body, pos, len(records), hdr.version); rerr == nil {
			return nil, &CountMismatchError{Claimed: int(hdr.count), Actual: len(records) + 1}
		}
	}

	if crc32.ChecksumIEEE(body) != hdr.regionCRC {
		return nil, ErrRegionChecksum
	}
	if pos < len(body) {
		return nil, ErrTrailingBytes
	}
	return records, nil
}
