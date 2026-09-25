package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

// Characterization tests: these pin the behavior the code *actually* has
// today, which in places diverges from the package docs and sentinel
// doc comments. See FINDINGS.md for the divergence analysis.

// TestLengthPrefixClassificationUsesFixedThreshold pins that scanFrame splits
// "length prefix too big" by the fixed 64MB maxBodySize constant, not by
// whether the claim fits the remaining bytes: any claim <= 64MB that runs
// past the end is reported as ErrRecordTruncated, exactly like a file cut
// mid-record, while only claims > 64MB become ErrLengthTooLarge.
func TestLengthPrefixClassificationUsesFixedThreshold(t *testing.T) {
	base := goodFile(t, sampleRecords())
	bodyLen := len(base) - headerSize // remaining bytes at record 0

	cases := []struct {
		name  string
		claim uint32
		want  error // sentinel class Read currently reports
	}{
		{"claim barely exceeds remaining", uint32(bodyLen), ErrRecordTruncated},
		{"64KB claim on ~100B file", 0x00010000, ErrRecordTruncated},
		{"claim exactly 64MB threshold", maxBodySize, ErrRecordTruncated},
		{"claim just over 64MB threshold", maxBodySize + 1, ErrLengthTooLarge},
		{"claim 0xFFFFFFFF", 0xFFFFFFFF, ErrLengthTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Clone(base)
			putUint32(data[headerSize:headerSize+lenPrefixLen], tc.claim)

			_, err := Read(bytes.NewReader(data))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Read error = %v, want class %v", err, tc.want)
			}
			var re *RecordError
			if !errors.As(err, &re) || re.Index != 0 || re.Offset != headerSize {
				t.Fatalf("location = %+v, want index 0 offset %d", re, headerSize)
			}

			rep := Inspect(bytes.NewReader(data))
			if rep.BadIndex != 0 || rep.BadOffset != headerSize || !errors.Is(rep.BadErr, tc.want) {
				t.Fatalf("Inspect bad point = (%d, %d, %v), want (0, %d, %v)",
					rep.BadIndex, rep.BadOffset, rep.BadErr, headerSize, tc.want)
			}
			// The two intact trailing frames are found by resync.
			if rep.Skipped != 2 {
				t.Fatalf("Inspect skipped = %d, want 2", rep.Skipped)
			}
			if rep.CountErr != nil {
				t.Fatalf("Inspect CountErr = %+v, want nil", rep.CountErr)
			}
			if !rep.RegionErr {
				t.Fatal("Inspect RegionErr = false, want true (body bytes changed)")
			}
		})
	}
}

// TestReadIgnoresKeyOrderAndUniqueness pins that Read performs no key-order
// or key-uniqueness validation: a file whose per-record CRCs, count, and
// region CRC are all correct is accepted verbatim, in file byte order, even
// when keys are unsorted or duplicated.
func TestReadIgnoresKeyOrderAndUniqueness(t *testing.T) {
	cases := []struct {
		name string
		recs []Record // stored in exactly this (file) order
	}{
		{"out-of-order keys", []Record{
			{Key: "bravo", Value: 2, Note: "b"},
			{Key: "alpha", Value: 1, Note: "a"},
			{Key: "charlie", Value: 3, Note: "c"},
		}},
		{"duplicate primary keys", []Record{
			{Key: "alpha", Value: 1, Note: "first"},
			{Key: "alpha", Value: 2, Note: "second"},
			{Key: "bravo", Value: 3, Note: "b"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildRaw(t, CurrentVersion, tc.recs)

			got, err := Read(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Read rejected file: %v", err)
			}
			if len(got) != len(tc.recs) {
				t.Fatalf("Read returned %d records, want %d", len(got), len(tc.recs))
			}
			for i := range tc.recs {
				if got[i] != tc.recs[i] {
					t.Fatalf("record %d = %+v, want %+v (file order preserved)", i, got[i], tc.recs[i])
				}
			}

			rep := Inspect(bytes.NewReader(data))
			if rep.BadIndex != -1 || rep.BadErr != nil || rep.CountErr != nil || rep.RegionErr {
				t.Fatalf("Inspect flagged a fully-checksummed file: %+v", rep)
			}
			if rep.Skipped != 0 {
				t.Fatalf("Inspect skipped = %d, want 0", rep.Skipped)
			}
			if len(rep.Records) != len(tc.recs) {
				t.Fatalf("Inspect recovered %d records, want %d", len(rep.Records), len(tc.recs))
			}
			for i := range tc.recs {
				if rep.Records[i] != tc.recs[i] {
					t.Fatalf("Inspect record %d = %+v, want %+v", i, rep.Records[i], tc.recs[i])
				}
			}
		})
	}
}

// TestWriteAcceptsDuplicateKeys pins that Write does not reject duplicate
// primary keys either; it sorts stably and emits both records.
func TestWriteAcceptsDuplicateKeys(t *testing.T) {
	dups := []Record{
		{Key: "alpha", Value: 2, Note: "second"},
		{Key: "alpha", Value: 1, Note: "first"},
	}
	var buf bytes.Buffer
	if err := Write(&buf, dups); err != nil {
		t.Fatalf("Write rejected duplicate keys: %v", err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Read returned %d records, want 2 (both duplicates kept)", len(got))
	}
}
