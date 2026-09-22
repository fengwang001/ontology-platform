package segment

import "ontology/zone"

// Config configures writer resource limits. Zero or negative values mean
// unlimited for the corresponding dimension.
type Config struct {
	MaxRows     int
	MaxGroups   int
	MaxDictCard int
}

// Writer accumulates immutable row groups in process memory.
type Writer struct {
	cfg    Config
	groups []*RowGroup
	rows   int
}

// NewWriter returns a writer with the given limits.
func NewWriter(cfg Config) *Writer { return &Writer{cfg: cfg} }

// AddGroup appends one row group. Encoding is chosen automatically: strings
// always use dictionary encoding; integers try dictionary encoding first and
// fall back to bit-packing when the cardinality limit is exceeded.
func (w *Writer) AddGroup(vals []zone.Value) error {
	return w.addGroup(vals, 0)
}

// AddGroupDict forces dictionary encoding. Unlike AddGroup, exceeding the
// cardinality limit is reported as ErrDictLimit and changes no state.
func (w *Writer) AddGroupDict(vals []zone.Value) error {
	if len(vals) == 0 {
		return w.addGroup(vals, EncDict)
	}
	kind := domainKind(vals)
	if kind == zone.Int {
		if _, err := buildIntDict(vals, w.cfg.MaxDictCard); err != nil {
			return ErrDictLimit
		}
	}
	return w.addGroup(vals, EncDict)
}

func (w *Writer) addGroup(vals []zone.Value, force Encoding) error {
	if w.cfg.MaxRows > 0 && w.rows+len(vals) > w.cfg.MaxRows {
		return ErrTooManyRows
	}
	if w.cfg.MaxGroups > 0 && len(w.groups)+1 > w.cfg.MaxGroups {
		return ErrTooManyGroups
	}
	kind := domainKind(vals)
	st := zone.Build(vals)
	nullSet := make([]int, 0, st.Nulls)
	for i, v := range vals {
		if v.Kind == zone.Null {
			nullSet = append(nullSet, i)
		}
	}
	g := &RowGroup{
		kind:    kind,
		rows:    len(vals),
		nulls:   st.Nulls,
		stats:   st,
		nullSet: nullSet,
	}
	nonNull := make([]zone.Value, 0, len(vals)-len(nullSet))
	for _, v := range vals {
		if v.Kind != zone.Null {
			nonNull = append(nonNull, v)
		}
	}
	if err := g.encode(nonNull, force, w.cfg.MaxDictCard); err != nil {
		return err
	}
	// Commit only after all encoding work succeeded.
	w.groups = append(w.groups, g)
	w.rows += len(vals)
	return nil
}

func domainKind(vals []zone.Value) zone.Kind {
	for _, v := range vals {
		if v.Kind != zone.Null {
			return v.Kind
		}
	}
	return zone.Int // all-null groups have no payload; the domain is arbitrary
}

// Rows returns the total number of rows written so far.
func (w *Writer) Rows() int { return w.rows }

// Groups returns the number of row groups written so far.
func (w *Writer) Groups() int { return len(w.groups) }
