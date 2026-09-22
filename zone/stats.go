package zone

// Stats is the lightweight per-row-group summary. Min/Max are computed over
// non-null values only; HasMin is false for an all-null row group, so an
// all-null group reports "no min/max" rather than zero-valued min/max.
type Stats struct {
	Rows   int
	Nulls  int
	HasMin bool
	Min    Value
	Max    Value
}

// Build computes stats for vals. NULL entries are counted in Nulls and are
// never considered for Min/Max. The returned Stats does not retain vals.
func Build(vals []Value) Stats {
	s := Stats{Rows: len(vals)}
	for _, v := range vals {
		if v.Kind == Null {
			s.Nulls++
			continue
		}
		if !s.HasMin {
			s.HasMin = true
			s.Min = v
			s.Max = v
			continue
		}
		if Compare(v, s.Min) < 0 {
			s.Min = v
		}
		if Compare(v, s.Max) > 0 {
			s.Max = v
		}
	}
	return s
}

// AllNull reports whether every row is null.
func (s Stats) AllNull() bool { return s.HasMin == false }

// Contains reports whether v lies within [Min, Max]. It must be called only
// for a non-null value whose Kind matches the stats domain.
func (s Stats) Contains(v Value) bool {
	if !s.HasMin {
		return false
	}
	return Compare(v, s.Min) >= 0 && Compare(v, s.Max) <= 0
}

// Range intersects the closed interval [lo, hi] with [Min, Max].
func (s Stats) Range(lo, hi Value) bool {
	if !s.HasMin {
		return false
	}
	return Compare(hi, s.Min) >= 0 && Compare(lo, s.Max) <= 0
}

// HasNonNull reports whether the group holds at least one non-null value.
func (s Stats) HasNonNull() bool { return s.Rows-s.Nulls > 0 }
