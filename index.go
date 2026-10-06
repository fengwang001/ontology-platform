package store

import "sort"

type SecondaryIndex struct {
	entries   map[string]IndexEntry
	watermark int64
}

func NewSecondaryIndex() *SecondaryIndex {
	return &SecondaryIndex{entries: make(map[string]IndexEntry)}
}

func (i *SecondaryIndex) Lookup(secondary string) (IndexEntry, bool) {
	entry, ok := i.entries[secondary]
	return entry, ok
}

func (i *SecondaryIndex) Owner(secondary string) (string, bool) {
	entry, ok := i.entries[secondary]
	if !ok {
		return "", false
	}
	return entry.Primary, true
}

func (i *SecondaryIndex) Watermark() int64 {
	return i.watermark
}

func (i *SecondaryIndex) SetWatermark(lsn int64) {
	i.watermark = lsn
}

func (i *SecondaryIndex) Apply(record Record) error {
	if record.LSN <= 0 || record.Primary == "" {
		return ErrInvalidArgument
	}
	switch record.Operation {
	case OpPut:
		i.applyPut(record)
	case OpDelete:
		i.applyDelete(record)
	default:
		return ErrInvalidArgument
	}
	return nil
}

func (i *SecondaryIndex) applyPut(record Record) {
	i.removeOwned(record.Primary, record.OldSecondary, record.LSN)
	if record.Secondary == nil {
		return
	}
	secondary := *record.Secondary
	entry, exists := i.entries[secondary]
	if !exists || entry.LSN < record.LSN {
		i.entries[secondary] = IndexEntry{Primary: record.Primary, LSN: record.LSN}
	}
}

func (i *SecondaryIndex) applyDelete(record Record) {
	i.removeOwned(record.Primary, record.OldSecondary, record.LSN)
}

func (i *SecondaryIndex) removeOwned(primary string, secondary *string, lsn int64) {
	if secondary == nil {
		return
	}
	entry, exists := i.entries[*secondary]
	if exists && entry.Primary == primary && entry.LSN < lsn {
		delete(i.entries, *secondary)
	}
}

func (i *SecondaryIndex) Entries() map[string]IndexEntry {
	result := make(map[string]IndexEntry, len(i.entries))
	for secondary, entry := range i.entries {
		result[secondary] = entry
	}
	return result
}

func (i *SecondaryIndex) Rebuild(table *Table) {
	i.entries = BuildIndex(table)
	i.watermark = 0
}

func BuildIndex(table *Table) map[string]IndexEntry {
	snapshot := table.Snapshot()
	entries := make(map[string]IndexEntry, len(snapshot))
	for primary, row := range snapshot {
		if row.Secondary == nil {
			continue
		}
		entries[*row.Secondary] = IndexEntry{Primary: primary}
	}
	return entries
}

func (i *SecondaryIndex) Check(table *Table) CheckReport {
	expected := BuildIndex(table)
	mismatches := make([]Mismatch, 0)
	for secondary, expectedEntry := range expected {
		actual, exists := i.entries[secondary]
		if !exists {
			mismatches = append(mismatches, Mismatch{
				Secondary: secondary,
				Type:      MismatchMissing,
				Expected:  expectedEntry.Primary,
			})
			continue
		}
		if actual.Primary != expectedEntry.Primary {
			mismatches = append(mismatches, Mismatch{
				Secondary: secondary,
				Type:      MismatchWrongPrimary,
				Expected:  expectedEntry.Primary,
				Actual:    actual.Primary,
			})
		}
	}
	for secondary, actual := range i.entries {
		if _, exists := expected[secondary]; exists {
			continue
		}
		mismatch := Mismatch{
			Secondary: secondary,
			Type:      MismatchExtra,
			Actual:    actual.Primary,
		}
		mismatches = append(mismatches, mismatch)
	}
	sort.Slice(mismatches, func(left, right int) bool {
		return mismatches[left].Secondary < mismatches[right].Secondary
	})
	return CheckReport{Mismatches: mismatches}
}
