package ontology

import "fmt"

// Validator is the consistency-check and error-normalization module.
type Validator struct {
	types      map[string]ObjectType
	views      map[string]ViewSpec
	viewsBySrc map[string][]ViewSpec
}

// NewValidator builds the normalization/verification module.
func NewValidator(types []ObjectType, views []ViewSpec) *Validator {
	v := &Validator{
		types:      make(map[string]ObjectType),
		views:      make(map[string]ViewSpec),
		viewsBySrc: make(map[string][]ViewSpec),
	}
	for _, t := range types {
		v.types[t.Name] = t
	}
	for _, w := range views {
		v.views[w.Name] = w
		for _, src := range w.Sources {
			v.viewsBySrc[src.Type] = append(v.viewsBySrc[src.Type], w)
		}
	}
	return v
}

// GroupResult is the externally visible aggregate value of one group.
type GroupResult struct {
	View   string
	Group  string
	Value  float64
	Count  int64
	Exists bool
}

// ValidateWrite checks parameters in the strict priority order:
// empty primary key / unknown type -> attribute shape -> mandatory group key.
func (v *Validator) ValidateWrite(req WriteRequest) *OpError {
	if req.Type == "" {
		return invalid("write", req.Type, req.Key, "object type is empty")
	}
	if req.Key == "" {
		return invalid("write", req.Type, req.Key, "primary key is empty")
	}
	t, ok := v.types[req.Type]
	if !ok {
		return invalid("write", req.Type, req.Key, "unknown object type")
	}
	for name, spec := range t.Attrs {
		val, present := req.Attrs[name]
		if !present || val == nil {
			if spec.Required {
				return invalid("write", req.Type, req.Key, "missing required attribute "+name)
			}
			continue
		}
		if !typeMatches(spec, val) {
			return invalid("write", req.Type, req.Key,
				"attribute "+name+" expects "+string(spec.Type)+" got "+typeName(val))
		}
	}
	for name := range req.Attrs {
		if _, ok := t.Attrs[name]; !ok {
			return invalid("write", req.Type, req.Key, "undeclared attribute "+name)
		}
	}
	for _, w := range v.viewsBySrc[req.Type] {
		for _, src := range w.Sources {
			if src.Type != req.Type || !src.GroupValid {
				continue
			}
			if keyEmpty(req.Attrs[src.GroupAttr]) {
				return invalid("write", req.Type, req.Key,
					"group key attribute "+src.GroupAttr+" for view "+w.Name+" is empty")
			}
		}
	}
	return nil
}

// ValidateDelete checks only parameter legality: non-empty type and key.
func (v *Validator) ValidateDelete(req DeleteRequest) *OpError {
	if req.Type == "" {
		return invalid("delete", req.Type, req.Key, "object type is empty")
	}
	if req.Key == "" {
		return invalid("delete", req.Type, req.Key, "primary key is empty")
	}
	if _, ok := v.types[req.Type]; !ok {
		return invalid("delete", req.Type, req.Key, "unknown object type")
	}
	return nil
}

// Verify recomputes every aggregate purely from live source instances and
// compares it against the maintained derived indexes. It returns the first
// divergence found, or nil if the maintained view is a faithful derivative.
func (v *Validator) Verify(src SourceReader, m *Maintainer) *OpError {
	type agg struct {
		count int64
		sum   float64
	}
	want := make(map[string]map[string]agg)
	for _, inst := range src.Snapshot() {
		if inst.Deleted {
			continue
		}
		for _, w := range v.viewsBySrc[inst.TypeName] {
			for _, s := range w.Sources {
				if s.Type != inst.TypeName {
					continue
				}
				g, ok := groupKey(inst.Attrs[s.GroupAttr])
				if !ok {
					continue
				}
				if want[w.Name] == nil {
					want[w.Name] = make(map[string]agg)
				}
				a := want[w.Name][g]
				a.count++
				if w.Kind == AggSum {
					if nv, ok := numberValue(inst.Attrs[s.ValueAttr]); ok {
						a.sum += nv
					}
				}
				want[w.Name][g] = a
			}
		}
	}
	for name := range v.views {
		vw := v.views[name]
		groups, err := m.allGroups(name)
		if err != nil {
			return invalid("verify", name, "", err.Error())
		}
		for g, got := range groups {
			exp := want[name][g]
			if vw.Kind == AggCount {
				exp.sum = float64(exp.count)
			}
			if got.Count != exp.count || got.Value != exp.sum {
				return &OpError{Code: ErrInvalidArgument, Op: "verify", TypeName: name, Key: g,
					Detail: fmtGroupErr(name, g, got.Count, got.Value, exp.count, exp.sum)}
			}
		}
		for g, exp := range want[name] {
			if _, ok := groups[g]; !ok {
				return &OpError{Code: ErrInvalidArgument, Op: "verify", TypeName: name, Key: g,
					Detail: fmtGroupErr(name, g, 0, 0, exp.count, exp.sum)}
			}
		}
	}
	return nil
}

func fmtGroupErr(view, group string, gc int64, gv float64, ec int64, ev float64) string {
	return fmt.Sprintf("view %s group %q maintained=(count=%d,sum=%v) expected=(count=%d,sum=%v)",
		view, group, gc, gv, ec, ev)
}
