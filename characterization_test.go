package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

// lengthBoundaryRecs serializes to a tiny region: each v2 body is 17 bytes
// ("a"/"b" keys, empty notes), so each frame is 25 bytes and the whole record
// area is 50 bytes. After the 4-byte length prefix only 46 bytes remain, far
// below the 64MB fixed threshold in scanFrame.
func lengthBoundaryFile(t *testing.T) []byte {
	t.Helper()
	return buildRaw(t, CurrentVersion, []Record{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
	})
}

func TestCharacterizationLengthPrefixBoundary(t *testing.T) {
	cases := []struct {
		name    string
		claimed uint32
		want    error
	}{
		{"64KB claim on a 50-byte region", 64 << 10, ErrRecordTruncated},
		{"claim just under the fixed 64MB threshold", uint32(maxBodySize - 1), ErrRecordTruncated},
		{"claim exactly equal to the fixed 64MB threshold", uint32(maxBodySize), ErrRecordTruncated},
		{"claim one byte over the fixed 64MB threshold", uint32(maxBodySize + 1), ErrLengthTooLarge},
		{"maximum uint32 claim", 0xFFFFFFFF, ErrLengthTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Clone(lengthBoundaryFile(t))
			putUint32(data[headerSize:headerSize+lenPrefixLen], tc.claimed)

			_, err := Read(bytes.NewReader(data))
			var re *RecordError
			if !errors.As(err, &re) {
				t.Fatalf("claim %d: want *RecordError, got %v", tc.claimed, err)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("claim %d: want %v, got %v", tc.claimed, tc.want, err)
			}
			other := ErrLengthTooLarge
			if tc.want == ErrLengthTooLarge {
				other = ErrRecordTruncated
			}
			if errors.Is(err, other) {
				t.Fatalf("claim %d: error %v is also classified as %v", tc.claimed, err, other)
			}
			if re.Index != 0 || re.Offset != headerSize {
				t.Fatalf("claim %d: location = (%d,%d), want (0,%d)", tc.claimed, re.Index, re.Offset, headerSize)
			}
		})
	}
}

func TestCharacterizationKeyOrderAndUniquenessRead(t *testing.T) {
	// Every file here has correct per-record CRCs and a correct region CRC;
	// only the logical key arrangement is unusual.
	cases := []struct {
		name  string
		recs  []Record
		order []string // key order Read is expected to hand back
	}{
		{
			name: "out of order keys pass through byte order",
			recs: []Record{
				{Key: "charlie", Value: 30, Note: "three"},
				{Key: "alpha", Value: 10, Note: "one"},
				{Key: "bravo", Value: 20, Note: "two"},
			},
			order: []string{"charlie", "alpha", "bravo"},
		},
		{
			name: "strictly descending keys pass through",
			recs: []Record{
				{Key: "c", Value: 3},
				{Key: "b", Value: 2},
				{Key: "a", Value: 1},
			},
			order: []string{"c", "b", "a"},
		},
		{
			name: "duplicate primary key returns both records",
			recs: []Record{
				{Key: "dup", Value: 1, Note: "first"},
				{Key: "dup", Value: 2, Note: "second"},
			},
			order: []string{"dup", "dup"},
		},
		{
			name: "duplicate key interleaved with a sorted one",
			recs: []Record{
				{Key: "dup", Value: 1},
				{Key: "dup", Value: 2},
				{Key: "mid", Value: 3},
			},
			order: []string{"dup", "dup", "mid"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildRaw(t, CurrentVersion, tc.recs)
			got, err := Read(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Read rejected the file: %v", err)
			}
			if len(got) != len(tc.recs) {
				t.Fatalf("got %d records, want %d", len(got), len(tc.recs))
			}
			for i, wantKey := range tc.order {
				if got[i].Key != wantKey {
					t.Fatalf("record %d key = %q, want %q (file byte order)", i, got[i].Key, wantKey)
				}
			}
			if !equalRecords(got, tc.recs) {
				t.Fatalf("records = %+v, want unchanged byte order %+v", got, tc.recs)
			}
		})
	}
}

func equalRecords(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCharacterizationInspectReports(t *testing.T) {
	unsortedRecs := []Record{
		{Key: "charlie", Value: 30, Note: "three"},
		{Key: "alpha", Value: 10, Note: "one"},
		{Key: "bravo", Value: 20, Note: "two"},
	}
	duplicateRecs := []Record{
		{Key: "dup", Value: 1, Note: "first"},
		{Key: "dup", Value: 2, Note: "second"},
	}

	cases := []struct {
		name      string
		build     func(t *testing.T) []byte
		wantBad   error // nil means the file has no bad point
		wantSkip  int
		wantCount bool // expect a CountMismatchError
		wantReg   bool
		wantRecs  []Record
	}{
		{
			name: "64KB length claim over a 50-byte region",
			build: func(t *testing.T) []byte {
				data := bytes.Clone(lengthBoundaryFile(t))
				putUint32(data[headerSize:headerSize+lenPrefixLen], 64<<10)
				return data
			},
			wantBad:   ErrRecordTruncated,
			wantSkip:  1,
			wantCount: false,
			wantReg:   true,
		},
		{
			name: "0xFFFFFFFF length claim",
			build: func(t *testing.T) []byte {
				data := bytes.Clone(lengthBoundaryFile(t))
				putUint32(data[headerSize:headerSize+lenPrefixLen], 0xFFFFFFFF)
				return data
			},
			wantBad:   ErrLengthTooLarge,
			wantSkip:  1,
			wantCount: false,
			wantReg:   true,
		},
		{
			name:      "CRC-valid out of order keys",
			build:     func(t *testing.T) []byte { return buildRaw(t, CurrentVersion, unsortedRecs) },
			wantBad:   nil,
			wantSkip:  0,
			wantCount: false,
			wantReg:   false,
			wantRecs:  unsortedRecs,
		},
		{
			name:      "CRC-valid duplicate primary keys",
			build:     func(t *testing.T) []byte { return buildRaw(t, CurrentVersion, duplicateRecs) },
			wantBad:   nil,
			wantSkip:  0,
			wantCount: false,
			wantReg:   false,
			wantRecs:  duplicateRecs,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build(t)
			rep := Inspect(bytes.NewReader(data))
			if rep.HeaderErr != nil {
				t.Fatalf("unexpected header error: %v", rep.HeaderErr)
			}
			if tc.wantBad == nil {
				if rep.BadIndex != -1 || rep.BadErr != nil {
					t.Fatalf("want clean report, got BadIndex=%d BadErr=%v", rep.BadIndex, rep.BadErr)
				}
			} else {
				if rep.BadIndex != 0 || rep.BadOffset != headerSize {
					t.Fatalf("bad point = (%d,%d), want (0,%d)", rep.BadIndex, rep.BadOffset, headerSize)
				}
				if !errors.Is(rep.BadErr, tc.wantBad) {
					t.Fatalf("BadErr = %v, want %v", rep.BadErr, tc.wantBad)
				}
			}
			if rep.Skipped != tc.wantSkip {
				t.Fatalf("Skipped = %d, want %d", rep.Skipped, tc.wantSkip)
			}
			if (rep.CountErr != nil) != tc.wantCount {
				t.Fatalf("CountErr = %+v, want present=%v", rep.CountErr, tc.wantCount)
			}
			if rep.RegionErr != tc.wantReg {
				t.Fatalf("RegionErr = %v, want %v", rep.RegionErr, tc.wantReg)
			}
			if tc.wantRecs != nil && !equalRecords(rep.Records, tc.wantRecs) {
				t.Fatalf("prefix records = %+v, want %+v", rep.Records, tc.wantRecs)
			}
		})
	}
}
