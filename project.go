package projection

import "strings"

// Project applies the compiled rules to obj and returns a new object holding
// only the visible attributes.
//
// The input is never modified and the result shares no map or slice headers
// with it: every nested map and slice is an independent deep copy. A nested
// object whose children are all hidden disappears instead of producing an
// empty object.
//
// schema may be nil; when supplied, required fields and declared
// dependencies are enforced and a *ProjectionError is returned on violation.
func (rs *Ruleset) Project(obj map[string]any, schema *Schema) (map[string]any, error) {
	pr := &projector{rs: rs, schema: schema}
	out, visible, err := pr.walk(obj, nil)
	if err != nil {
		return nil, err
	}
	if !visible {
		return map[string]any{}, nil
	}
	return out.(map[string]any), nil
}

type projector struct {
	rs     *Ruleset
	schema *Schema
}

// walk projects v at the given attribute path. The bool reports whether the
// node survives (objects must retain at least one child; leaves survive when
// their own decision is allow).
func (pr *projector) walk(v any, path []string) (any, bool, error) {
	switch t := v.(type) {
	case map[string]any:
		return pr.walkMap(t, path)
	case []any:
		return pr.walkSlice(t, path)
	default:
		d := pr.rs.decision(path)
		if !d.Visible {
			return nil, false, nil
		}
		return v, true, nil
	}
}

func (pr *projector) walkMap(t map[string]any, path []string) (any, bool, error) {
	out := make(map[string]any, len(t))
	for key, child := range t {
		childPath := appendPath(path, key)
		proj, visible, err := pr.walk(child, childPath)
		if err != nil {
			return nil, false, err
		}
		if visible {
			out[key] = proj
		}
	}
	// Constraints are enforced even when every child was pruned, so a
	// required attribute hidden together with its ancestor still reports.
	if err := pr.checkConstraints(t, out, path); err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	// A container survives via its surviving children. Its own wildcard
	// decision (e.g. "addr.*" matching the intermediate object addr.geo)
	// must not remove it while deeper descendants remain visible; exact
	// denials already cascade through ancestor override at every child.
	return out, true, nil
}

func (pr *projector) walkSlice(t []any, path []string) ([]any, bool, error) {
	d := pr.rs.decision(path)
	out := make([]any, 0, len(t))
	for _, elem := range t {
		// Array elements share the field path; constraints are checked on
		// each surviving object element.
		proj, visible, err := pr.walk(elem, path)
		if err != nil {
			return nil, false, err
		}
		if visible {
			out = append(out, proj)
		}
	}
	// Walk elements even when the slice itself is hidden, so required
	// attributes inside the hidden subtree still produce an error.
	if !d.Visible {
		return nil, false, nil
	}
	return out, true, nil
}

// checkConstraints enforces required attributes and declared dependencies
// for the object at path. orig is the unprojected object (used to
// distinguish "hidden by a rule" from "absent in the source"); out is the
// projected object.
func (pr *projector) checkConstraints(orig, out map[string]any, path []string) error {
	if pr.schema == nil {
		return nil
	}
	for full, spec := range pr.schema.Fields {
		segs := strings.Split(full, ".")
		if !pathEqual(segs[:len(segs)-1], path) {
			continue
		}
		name := segs[len(segs)-1]
		if _, existed := orig[name]; !existed {
			continue
		}
		fieldPath := appendPath(path, name)
		_, present := out[name]
		if spec.Required && !present {
			d := pr.rs.decision(fieldPath)
			return &ProjectionError{
				Kind:    ErrorRequiredHidden,
				Field:   full,
				RuleRaw: d.RuleRaw,
			}
		}
		if present && spec.ComputedFrom != "" {
			srcPath := strings.Split(spec.ComputedFrom, ".")
			src := pr.rs.decision(srcPath)
			if !src.Visible {
				switch pr.schema.DependencyPolicy {
				case DependencyError:
					return &ProjectionError{
						Kind:          ErrorDependencyBroken,
						Field:         full,
						SourceField:   spec.ComputedFrom,
						SourceRuleRaw: src.RuleRaw,
					}
				default: // DependencyHideResult: hide the computed field too
					delete(out, name)
				}
			}
		}
	}
	return nil
}

func appendPath(path []string, seg string) []string {
	p := make([]string, 0, len(path)+1)
	p = append(p, path...)
	return append(p, seg)
}

func pathEqual(a, b []string) bool {
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
