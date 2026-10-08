package snapshot

import "encoding/json"

// SectionStats records how much work locating a section's recoverable prefix
// actually cost. It exists so the complexity claim — work proportional to
// the corruption position, not to the section or file size — can be
// verified by callers and tests.
type SectionStats struct {
	// RecordsScanned is prefix length plus one when corruption was found
	// (the corrupt record itself is examined before the scan stops).
	RecordsScanned int
	// BytesScanned is the number of section bytes inspected.
	BytesScanned int
}

// sectionScan is the outcome of scanning one section.
type sectionScan struct {
	// payloads holds the recoverable prefix: the maximal run of records
	// that each parse cleanly and pass their integrity check.
	payloads [][]byte
	declared int
	// corruption is non-nil when the scan stopped at a structurally
	// corrupt record or at a corrupt section header.
	corruption *Diagnostic
	stats      SectionStats
}

// recordValidator checks that a payload decodes into a well-formed record of
// the expected kind. The CRC frame check has already happened at this point;
// the validator covers structural/semantic well-formedness of the payload.
type recordValidator func(payload []byte) error

func validateObjectPayload(payload []byte) error {
	var r ObjectRecord
	if err := json.Unmarshal(payload, &r); err != nil {
		return err
	}
	return r.valid()
}

func validateLinkPayload(payload []byte) error {
	var r LinkRecord
	if err := json.Unmarshal(payload, &r); err != nil {
		return err
	}
	return r.valid()
}

func validateActionPayload(payload []byte) error {
	var r ActionRecord
	if err := json.Unmarshal(payload, &r); err != nil {
		return err
	}
	return r.valid()
}

func validatorFor(t SectionType) recordValidator {
	switch t {
	case SectionObjects:
		return validateObjectPayload
	case SectionLinks:
		return validateLinkPayload
	default:
		return validateActionPayload
	}
}

// scanSection locates the maximal recoverable prefix of one section.
//
// The scan is strictly sequential and stops at the first structurally
// corrupt record: everything from that record on is excluded from the
// prefix, even if some later record would parse fine in isolation. Work is
// therefore proportional to the corruption position inside the section; the
// section's remaining bytes and the rest of the file are never touched.
func scanSection(data []byte, e dirEntry, validate recordValidator) sectionScan {
	res := sectionScan{}
	start := int(e.offset)
	end := start + int(e.length)

	// A truncated file leaves directory entries pointing past the end of
	// the data. The directory itself is CRC-protected and therefore
	// trustworthy, so clamp the scan limit to what actually exists: records
	// before the cut are still framed intact and remain recoverable.
	if end > len(data) {
		end = len(data)
	}
	if start+sectionHeaderSize > end {
		res.corruption = &Diagnostic{
			Category:    CatStructural,
			Section:     e.secType,
			RecordIndex: -1,
			Detail:      ErrTruncated.Error(),
		}
		return res
	}

	declared, err := verifySectionHeader(data, e)
	if err != nil {
		res.corruption = &Diagnostic{
			Category:    CatStructural,
			Section:     e.secType,
			RecordIndex: -1,
			Detail:      err.Error(),
		}
		res.stats.BytesScanned = sectionHeaderSize
		return res
	}
	res.declared = declared
	res.stats.BytesScanned = sectionHeaderSize

	off := start + sectionHeaderSize
	for off < end {
		payload, next, derr := decodeRecordAt(data, off, end)
		if derr == nil {
			derr = validate(payload)
		}
		if derr != nil {
			res.corruption = &Diagnostic{
				Category:    CatStructural,
				Section:     e.secType,
				RecordIndex: len(res.payloads),
				Detail:      derr.Error(),
			}
			res.stats.RecordsScanned = len(res.payloads) + 1
			res.stats.BytesScanned += recordHeaderSize
			return res
		}
		res.payloads = append(res.payloads, payload)
		res.stats.BytesScanned += next - off
		off = next
	}
	res.stats.RecordsScanned = len(res.payloads)
	return res
}
