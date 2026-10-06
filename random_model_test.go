package store

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

type naiveModel struct {
	rows    map[string]*string
	records []Record
}

func newNaiveModel() *naiveModel {
	return &naiveModel{rows: make(map[string]*string)}
}

func (m *naiveModel) Put(row Row) (int64, error, string) {
	existing, exists := m.rows[row.Primary]
	if row.Secondary != nil {
		for primary, secondary := range m.rows {
			if secondary != nil && *secondary == *row.Secondary && primary != row.Primary {
				return 0, ErrUniqueConflict, fmt.Sprintf("secondary %q owned by %q", *row.Secondary, primary)
			}
		}
	}
	lsn := int64(len(m.records) + 1)
	record := Record{
		LSN:       lsn,
		Operation: OpPut,
		Primary:   row.Primary,
		Secondary: cloneStringPtr(row.Secondary),
	}
	if exists {
		record.OldSecondary = cloneStringPtr(existing)
	}
	m.records = append(m.records, record)
	m.rows[row.Primary] = cloneStringPtr(row.Secondary)
	owner := "nil"
	if row.Secondary != nil {
		owner = *row.Secondary
	}
	return lsn, nil, "accepted; owner-before=" + owner
}

func (m *naiveModel) Delete(primary string) (int64, error, string) {
	secondary, exists := m.rows[primary]
	if !exists {
		return 0, ErrPrimaryNotFound, "primary absent in model"
	}
	lsn := int64(len(m.records) + 1)
	m.records = append(m.records, Record{
		LSN:          lsn,
		Operation:    OpDelete,
		Primary:      primary,
		OldSecondary: cloneStringPtr(secondary),
	})
	delete(m.rows, primary)
	return lsn, nil, "accepted delete"
}

func (m *naiveModel) Rebuild() map[string]string {
	result := make(map[string]string)
	for primary, secondary := range m.rows {
		if secondary != nil {
			result[*secondary] = primary
		}
	}
	return result
}

func randomSecondary(r *rand.Rand) *string {
	switch r.IntN(5) {
	case 0:
		return nil
	case 1:
		return strPtr("a")
	case 2:
		return strPtr("b")
	case 3:
		return strPtr("c")
	default:
		return strPtr("d")
	}
}

func secondaryLog(value *string) string {
	if value == nil {
		return "nil"
	}
	return fmt.Sprintf("%q", *value)
}

func TestRandomWriteAndCrashSequencesAgainstNaiveModel(t *testing.T) {
	for seed := uint64(1); seed <= 120; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, seed*7+1))
			model := newNaiveModel()

			for i := 0; i < 8; i++ {
				primary := fmt.Sprintf("p%d", r.IntN(4))
				var lsn int64
				var err error
				var basis string
				input := Row{Primary: primary, Secondary: randomSecondary(r)}
				if r.IntN(5) == 0 {
					lsn, err, basis = model.Delete(primary)
					t.Logf("input delete(%q) -> lsn=%d err=%v basis=%s", primary, lsn, err, basis)
				} else {
					lsn, err, basis = model.Put(input)
					t.Logf("input put(%q,%s) -> lsn=%d err=%v basis=%s", primary, secondaryLog(input.Secondary), lsn, err, basis)
				}
			}

			watermark := r.Int64N(int64(len(model.records)) + 1)
			appliedPrefix := watermark + r.Int64N(int64(len(model.records))-watermark+1)
			s := recoveredStoreFromRecords(t, model.records, appliedPrefix, watermark)
			t.Logf("crash recovery: records=%d appliedPrefix=%d watermark=%d", len(model.records), appliedPrefix, watermark)

			performModelOperation := func() {
				primary := fmt.Sprintf("p%d", r.IntN(5))
				if r.IntN(5) == 0 {
					lsn, modelErr, basis := model.Delete(primary)
					gotLSN, gotErr := s.Delete(primary)
					t.Logf("during-catchup delete(%q) -> model=(%d,%v) store=(%d,%v) basis=%s", primary, lsn, modelErr, gotLSN, gotErr, basis)
					if (modelErr == nil) != (gotErr == nil) {
						t.Fatalf("delete error mismatch model=%v store=%v", modelErr, gotErr)
					}
				} else {
					input := Row{Primary: primary, Secondary: randomSecondary(r)}
					lsn, modelErr, basis := model.Put(input)
					gotLSN, gotErr := s.Put(input)
					t.Logf("during-catchup put(%q,%s) -> model=(%d,%v) store=(%d,%v) basis=%s", primary, secondaryLog(input.Secondary), lsn, modelErr, gotLSN, gotErr, basis)
					if (modelErr == nil) != (gotErr == nil) {
						t.Fatalf("put error mismatch model=%v store=%v", modelErr, gotErr)
					}
				}
			}

			if r.IntN(2) == 0 {
				performModelOperation()
			}
			for s.Watermark() < s.LastLSN() {
				batch := 1 + r.IntN(4)
				advanced, err := s.CatchUp(batch)
				t.Logf("catchup batch=%d -> advanced=%d err=%v target=%d", batch, advanced, err, s.LastLSN())
				if err != nil {
					t.Fatalf("unexpected catch-up error: %v", err)
				}
				if s.Watermark() < s.LastLSN() && r.IntN(3) == 0 {
					performModelOperation()
				}
			}

			state := s.PersistedSnapshot()
			if state.Watermark != s.LastLSN() || state.Watermark != int64(len(model.records)) {
				t.Fatalf("watermark=%d last=%d records=%d", state.Watermark, s.LastLSN(), len(model.records))
			}
			expected := model.Rebuild()
			requireIndexEntries(t, s, expected)

			for primary, secondary := range model.rows {
				row, ok := state.Rows[primary]
				if !ok {
					t.Fatalf("missing primary %q", primary)
				}
				if (secondary == nil) != (row.Secondary == nil) || secondary != nil && *row.Secondary != *secondary {
					t.Fatalf("primary %q = %v, want %v", primary, row.Secondary, secondary)
				}
			}
			if len(state.Rows) != len(model.rows) || len(state.Records) != len(model.records) {
				t.Fatalf("table/log size mismatch rows=%d/%d records=%d/%d", len(state.Rows), len(model.rows), len(state.Records), len(model.records))
			}
		})
	}
}
