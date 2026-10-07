package ontology_test

import (
	"sort"
	"strconv"

	ont "ontology/ontology"
)

// naiveOutcome is the deliberately simple reference result: only the
// contract-level observable facts, no implementation structure, so
// equality with the optimized engine is a true differential oracle.
type naiveOutcome struct {
	errKind  string // "" on success
	visible  bool
	view     map[string]string // prop -> "raw:<v>" | "mask:<v>"
	redacted []string
	absent   []string
	applied  []string
	dropped  []string
	values   map[string]ont.RawValue // post-op full raw state
	version  int64
}

// naiveEngine re-implements the whole contract in the most direct way:
// it scans EVERY registered rule on every call (no indexes) and uses a
// single global mutex. The optimized Engine must agree with it exactly.
type naiveEngine struct {
	ot    *ont.ObjectType
	rows  []ont.RowRule
	props []ont.PropRule
	rmode ont.MergeMode
	pmode ont.MergeMode
	wmode ont.WriteMode

	instances map[string]map[string]ont.RawValue
	present   map[string]map[string]bool
	versions  map[string]int64
}

func newNaive(ot *ont.ObjectType, cfg ont.Config) *naiveEngine {
	return &naiveEngine{
		ot: ot, rows: cfg.Rows.Rules, props: cfg.Props.Rules,
		rmode: cfg.Rows.Mode, pmode: cfg.Props.Mode, wmode: cfg.WriteMode,
		instances: map[string]map[string]ont.RawValue{},
		present:   map[string]map[string]bool{},
		versions:  map[string]int64{},
	}
}

func (n *naiveEngine) add(instID string, vals map[string]ont.RawValue) {
	cp := map[string]ont.RawValue{}
	pr := map[string]bool{}
	for k, v := range vals {
		cp[k] = v
		pr[k] = true
	}
	n.instances[instID] = cp
	n.present[instID] = pr
	n.versions[instID] = 0
}

func naiveSel(s ont.SubjectSelector, sub ont.Subject) bool {
	if s.MatchAll {
		return true
	}
	for _, u := range s.Users {
		if u == sub.ID {
			return true
		}
	}
	for _, g := range s.Groups {
		for _, sg := range sub.Groups {
			if g == sg {
				return true
			}
		}
	}
	return false
}

func naiveCmp(dt ont.DataType, got, want ont.RawValue) int {
	switch dt {
	case ont.TypeInt:
		a, b := got.(int64), want.(int64)
		return icmp(a, b)
	case ont.TypeFloat:
		a, b := got.(float64), want.(float64)
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	case ont.TypeString:
		a, b := got.(string), want.(string)
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	default:
		a, b := got.(bool), want.(bool)
		if a == b {
			return 0
		}
		if !a {
			return -1
		}
		return 1
	}
}

func icmp(a, b int64) int {
	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

func naiveOp(c int, op ont.CmpOp) bool {
	switch op {
	case ont.CmpEq:
		return c == 0
	case ont.CmpNe:
		return c != 0
	case ont.CmpLt:
		return c < 0
	case ont.CmpLe:
		return c <= 0
	case ont.CmpGt:
		return c > 0
	default:
		return c >= 0
	}
}

func (n *naiveEngine) pred(p ont.Predicate, vals map[string]ont.RawValue, pres map[string]bool) bool {
	if !pres[p.Property] {
		return p.Op == ont.CmpNe
	}
	dt, _ := n.ot.PropertyType(p.Property)
	return naiveOp(naiveCmp(dt, vals[p.Property], p.Value), p.Op)
}

func naiveCheck(dt ont.DataType, v ont.RawValue) bool {
	switch dt {
	case ont.TypeInt:
		_, ok := v.(int64)
		return ok
	case ont.TypeFloat:
		f, ok := v.(float64)
		return ok && f == f
	case ont.TypeString:
		_, ok := v.(string)
		return ok
	case ont.TypeBool:
		_, ok := v.(bool)
		return ok
	}
	return false
}

func naiveValStr(v ont.RawValue) string {
	switch x := v.(type) {
	case string:
		return "s:" + x
	case int64:
		return "i:" + strconv.FormatInt(x, 10)
	case float64:
		return "f:" + strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "b:t"
		}
		return "b:f"
	}
	return "?"
}

func propIndex(ot *ont.ObjectType, name string) int {
	for i, p := range ot.Properties() {
		if p.Name == name {
			return i
		}
	}
	return -1
}

func sortedCopy(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	cp := append([]string(nil), xs...)
	sort.Strings(cp)
	return cp
}
