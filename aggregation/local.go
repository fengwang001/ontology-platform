package aggregation

import "fmt"

// AggregateBatch is the local phase: it validates and folds one batch of
// rows into per-group partial aggregates. For each group it computes the
// sum, the row count, and a value -> net-delta map. Groups whose partial
// aggregate is all-zero are filtered out and never sent to the global
// phase. Any illegal row rejects the whole batch before anything is sent.
func AggregateBatch(batch []RowOp) (map[string]PartialAgg, error) {
	partials := make(map[string]PartialAgg)
	for i, row := range batch {
		if !row.Op.Valid() {
			return nil, &Error{
				Code: ErrCodeInvalidOp,
				Msg:  fmt.Sprintf("row %d: illegal op %d", i, row.Op),
			}
		}
		if row.Group == "" {
			return nil, &Error{
				Code: ErrCodeEmptyGroup,
				Msg:  fmt.Sprintf("row %d: empty group name", i),
			}
		}
		p := partials[row.Group]
		if p.Values == nil {
			p.Values = make(map[float64]int64)
		}
		switch row.Op {
		case OpAdd:
			p.Sum += row.Value
			p.Count++
			p.Values[row.Value]++
		case OpRetract:
			p.Sum -= row.Value
			p.Count--
			p.Values[row.Value]--
			if p.Values[row.Value] == 0 {
				delete(p.Values, row.Value)
			}
		}
		partials[row.Group] = p
	}
	for group, p := range partials {
		if p.IsZero() {
			delete(partials, group)
		}
	}
	return partials, nil
}
