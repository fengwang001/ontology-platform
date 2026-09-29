package evolution

import "strconv"

// Event is an old-version event: columns aligned by name.
type Event struct {
	Version int
	// Columns are matched by name, never by position.
	Columns map[string]any
}

// ProjectedRow is the deterministic projection result.
type ProjectedRow struct {
	SchemaVersion int
	Columns       map[string]any
}

// ProjectionReport explains the per-column decisions.
type ProjectionReport struct {
	EventVersion  int
	TargetVersion int
	// Results covers every target column plus every dropped event column,
	// in a deterministic order (target schema order, then dropped names).
	Results []ColumnResult
}

// Snapshot is an immutable view of one schema version.
// A projection always runs against one complete Snapshot, so concurrent
// registry evolution can never mix columns from two versions.
type Snapshot struct {
	schema Schema
	idx    map[string]int
}

func newSnapshot(version int, cols []ColumnDef) *Snapshot {
	sc := Schema{Version: version, Columns: cloneColumns(cols)}
	return &Snapshot{schema: sc, idx: sc.columnIndex()}
}

// Schema returns a defensive copy of the snapshot's schema.
func (s *Snapshot) Schema() Schema {
	return Schema{Version: s.schema.Version, Columns: cloneColumns(s.schema.Columns)}
}

func (s *Snapshot) column(name string) (ColumnDef, bool) {
	i, ok := s.idx[name]
	if !ok {
		return ColumnDef{}, false
	}
	return s.schema.Columns[i], true
}

// Project maps one event onto this snapshot's schema by column name.
// On success the returned row is complete and fully typed. On failure the
// whole event is rejected (no partial row is returned) and the returned
// report still explains every column examined up to and past the failure.
func (s *Snapshot) Project(ev Event) (*ProjectedRow, *ProjectionReport, error) {
	report := &ProjectionReport{EventVersion: ev.Version, TargetVersion: s.schema.Version}

	row := make(map[string]any, len(s.schema.Columns))

	// Target columns are visited in schema order: deterministic report order.
	for _, target := range s.schema.Columns {
		raw, present := ev.Columns[target.Name]
		if !present || raw == nil {
			if target.Required {
				report.Results = append(report.Results, ColumnResult{
					Name:       target.Name,
					Decision:   DecisionRejected,
					TargetType: target.Type,
					Basis:      "column missing in event and declared required -> reject whole event",
				})
				return nil, report, reject(ReasonRequiredMissing,
					"required column %q is missing from event version %d", target.Name, ev.Version)
			}
			zv := target.Type.zeroValue()
			row[target.Name] = zv
			report.Results = append(report.Results, ColumnResult{
				Name:       target.Name,
				Decision:   DecisionDefaulted,
				TargetType: target.Type,
				OutValue:   zv,
				Basis:      "column missing in event and nullable -> fill type zero value",
			})
			continue
		}

		normalized, sourceType, err := normalizeValue(raw)
		if err != nil {
			report.Results = append(report.Results, ColumnResult{
				Name:        target.Name,
				Decision:    DecisionRejected,
				TargetType:  target.Type,
				SourceValue: raw,
				Basis:       "event value has no representation in any supported type -> reject",
			})
			return nil, report, err
		}

		out, decision, err := convertValue(normalized, sourceType, target)
		if err != nil {
			report.Results = append(report.Results, ColumnResult{
				Name:        target.Name,
				Decision:    DecisionRejected,
				SourceType:  sourceType,
				TargetType:  target.Type,
				SourceValue: raw,
				Basis:       "string -> int succeeds only for a canonical legal integer -> reject",
			})
			return nil, report, err
		}
		row[target.Name] = out
		basis := "same type, aligned by name, copied unchanged"
		if decision == DecisionConverted {
			switch {
			case sourceType == TypeInt && target.Type == TypeString:
				basis = "int -> string always succeeds (strconv.FormatInt)"
			case sourceType == TypeString && target.Type == TypeInt:
				basis = "string -> int: content is a legal base-10 integer (strconv.ParseInt)"
			}
		}
		report.Results = append(report.Results, ColumnResult{
			Name:        target.Name,
			Decision:    decision,
			SourceType:  sourceType,
			TargetType:  target.Type,
			SourceValue: raw,
			OutValue:    out,
			Basis:       basis,
		})
	}

	// Columns that no longer exist are dropped silently (sorted for determinism).
	for _, name := range sortedKeys(ev.Columns) {
		if _, ok := s.idx[name]; ok {
			continue
		}
		report.Results = append(report.Results, ColumnResult{
			Name:        name,
			Decision:    DecisionDropped,
			SourceValue: ev.Columns[name],
			Basis:       "column absent from target schema -> silently dropped",
		})
	}

	return &ProjectedRow{SchemaVersion: s.schema.Version, Columns: row}, report, nil
}

// normalizeValue maps Go values onto the supported representations.
// Integers accept every Go integer kind and become int64; only string and
// integer kinds are legal event values.
func normalizeValue(raw any) (any, ColumnType, error) {
	switch v := raw.(type) {
	case string:
		return v, TypeString, nil
	case int:
		return int64(v), TypeInt, nil
	case int8:
		return int64(v), TypeInt, nil
	case int16:
		return int64(v), TypeInt, nil
	case int32:
		return int64(v), TypeInt, nil
	case int64:
		return v, TypeInt, nil
	case uint:
		return int64(v), TypeInt, nil
	case uint8:
		return int64(v), TypeInt, nil
	case uint16:
		return int64(v), TypeInt, nil
	case uint32:
		return int64(v), TypeInt, nil
	case uint64:
		return int64(v), TypeInt, nil
	default:
		return nil, "", reject(ReasonInvalidValue,
			"event value %v (%T) is neither int nor string", raw, raw)
	}
}

// convertValue applies the decidable int<->string rules.
// int -> string always succeeds; string -> int succeeds only for content
// that parses as a base-10 integer with no leftover characters.
func convertValue(v any, from ColumnType, target ColumnDef) (any, ColumnDecision, error) {
	if from == target.Type {
		return v, DecisionAligned, nil
	}
	switch {
	case from == TypeInt && target.Type == TypeString:
		return strconv.FormatInt(v.(int64), 10), DecisionConverted, nil
	case from == TypeString && target.Type == TypeInt:
		text := v.(string)
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, DecisionConverted, reject(ReasonConversionFailed,
				"column %q: %q is not a legal integer", target.Name, text)
		}
		return n, DecisionConverted, nil
	default:
		return nil, "", reject(ReasonInvalidType,
			"column %q: cannot convert %s to %s", target.Name, from, target.Type)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// renderValue keeps logs readable.
