package store

import "testing"

func TestCheckDistinguishesMismatchTypes(t *testing.T) {
	table := NewTable()
	table.Put(Row{Primary: "p1", Secondary: strPtr("bad")})
	table.Put(Row{Primary: "p2", Secondary: strPtr("missing")})

	index := NewSecondaryIndex()
	index.entries["extra"] = IndexEntry{Primary: "ghost", LSN: 1}
	index.entries["bad"] = IndexEntry{Primary: "ghost", LSN: 1}
	index.watermark = 1
	s := newStore(table, NewWriteLog(), index, nil)
	s.log.last = 1

	report, err := s.Check()
	requireNoError(t, err)
	byType := make(map[MismatchType]Mismatch)
	for _, mismatch := range report.Mismatches {
		byType[mismatch.Type] = mismatch
	}
	extra := byType[MismatchExtra]
	if extra.Secondary != "extra" || extra.Actual != "ghost" {
		t.Fatalf("extra mismatch = %#v", extra)
	}
	missing := byType[MismatchMissing]
	if missing.Secondary != "missing" || missing.Expected != "p2" {
		t.Fatalf("missing mismatch = %#v", missing)
	}
	wrong := byType[MismatchWrongPrimary]
	if wrong.Secondary != "bad" || wrong.Expected != "p1" || wrong.Actual != "ghost" {
		t.Fatalf("wrong mismatch = %#v", wrong)
	}
}
