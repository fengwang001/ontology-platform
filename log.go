package ontology

import "sort"

// Record is the immutable execution record of one committed action.
// Seq is strictly increasing with no gaps; failed actions produce no
// record and consume no sequence number.
type Record struct {
	Seq        uint64
	ActionType string
	Params     map[string]any
	Objects    []string
	Relations  []string
}

// Records returns committed records with from <= Seq <= to, ordered by Seq.
// Returned records are deep copies; mutating them cannot affect the log.
func (e *Engine) Records(from, to uint64) []Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Record
	for _, r := range e.log {
		if r.Seq >= from && r.Seq <= to {
			out = append(out, cloneRecord(r))
		}
	}
	return out
}

// Inconsistencies reports every compensation step that failed, in order.
func (e *Engine) Inconsistencies() []Inconsistency {
	return e.store.Inconsistencies()
}

func cloneRecord(r Record) Record {
	out := r
	if r.Params != nil {
		out.Params = deepCopy(r.Params).(map[string]any)
	}
	out.Objects = append([]string(nil), r.Objects...)
	out.Relations = append([]string(nil), r.Relations...)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
