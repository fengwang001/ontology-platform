package snapshot

import (
	"hash/crc32"
	"io"
)

// Report is the lenient diagnosis produced by Inspect.
type Report struct {
	// HeaderErr is set when the header itself cannot be trusted.
	HeaderErr error
	Version   uint16

	// Records is the maximal continuously-verified prefix from the start.
	Records []Record

	// BadIndex is the zero-based index of the first damaged record, or -1.
	BadIndex int
	// BadOffset is its byte offset within the file.
	BadOffset int64
	// BadErr is the classified reason (see Err* sentinels).
	BadErr error

	// Skipped counts parseable records found after the bad point.
	Skipped int

	// CountErr is set when header count disagrees with found records.
	CountErr *CountMismatchError
	// RegionErr is set when the whole record-area checksum is wrong.
	RegionErr bool
}

// resyncFrom finds the longest chain of consecutively valid frames starting at
// any offset strictly after start. CRC makes accidental alignment unlikely.
func resyncFrom(body []byte, start, startIndex int, version uint16) (bestCount int) {
	for off := start + 1; off < len(body); off++ {
		pos := off
		count := 0
		for {
			_, next, rerr := scanFrame(body, pos, startIndex+count, version)
			if rerr != nil {
				break
			}
			count++
			pos = next
			if pos >= len(body) {
				break
			}
		}
		if count > bestCount {
			bestCount = count
		}
	}
	return bestCount
}

// Inspect leniently examines a potentially corrupt snapshot. It always returns
// the maximum continuously-verified record prefix, locates the first bad
// record, and counts parseable records stranded after it. Inspect never
// returns partial data in place of Read; the two are independent.
func Inspect(r io.Reader) *Report {
	rep := &Report{BadIndex: -1}
	data, err := io.ReadAll(r)
	if err != nil {
		rep.HeaderErr = err
		return rep
	}
	hdr, err := parseHeader(data)
	if err != nil {
		rep.HeaderErr = err
		return rep
	}
	rep.Version = hdr.version
	body := hdr.body

	pos := 0
	for i := 0; i < int(hdr.count); i++ {
		rec, next, rerr := scanFrame(body, pos, i, hdr.version)
		if rerr == nil {
			rep.Records = append(rep.Records, rec)
			pos = next
			continue
		}
		if pos == len(body) {
			// Body ended cleanly but header promised more.
			rep.CountErr = &CountMismatchError{Claimed: int(hdr.count), Actual: i}
			break
		}
		rep.BadIndex = i
		rep.BadOffset = rerr.Offset
		rep.BadErr = rerr.Err
		rep.Skipped = resyncFrom(body, pos, i+1, hdr.version)
		break
	}

	// Undercount: a complete extra frame exists past the declared count.
	if rep.BadIndex == -1 && rep.CountErr == nil && pos < len(body) {
		if _, _, rerr := scanFrame(body, pos, len(rep.Records), hdr.version); rerr == nil {
			rep.CountErr = &CountMismatchError{Claimed: int(hdr.count), Actual: len(rep.Records) + 1}
		}
	}

	if crc32.ChecksumIEEE(body) != hdr.regionCRC {
		rep.RegionErr = true
	}
	return rep
}
