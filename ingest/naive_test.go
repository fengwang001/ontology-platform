package ingest

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"ontology/coerce"
	"ontology/mapping"
)

// naiveModel is an independent, straightforward re-implementation of the
// spec used purely to cross-check Store. It intentionally shares no code
// with the implementation (it redoes coercion and traversal from scratch).
type naiveModel struct {
	mode   mapping.Mode
	fmax   int
	fields map[string]string // dotted path -> type
	mv     int
	docs   map[string]map[string]any
	log    *strings.Builder
}

type naiveOutcome struct {
	doc     map[string]any
	ignored []string
	mv      int
	errKind string
	errPath string
}

func newNaive(mode mapping.Mode, fmax int) *naiveModel {
	return &naiveModel{
		mode:   mode,
		fmax:   fmax,
		fields: map[string]string{},
		docs:   map[string]map[string]any{},
		log:    &strings.Builder{},
	}
}

func (nm *naiveModel) count() int { return len(nm.fields) }

// ---- independent validation ----

func naiveValidateID(id string) bool {
	return len(id) >= 1 && len(id) <= 512
}

func naiveValidateValue(v any, depth int) (bad bool) {
	switch x := v.(type) {
	case nil, bool, int64, float64, string:
		return false
	case map[string]any:
		if depth > 8 {
			return true
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(k) < 1 || len(k) > 64 || strings.Contains(k, ".") {
				return true
			}
			if naiveValidateValue(x[k], depth+1) {
				return true
			}
		}
		return false
	case []any:
		for _, el := range x {
			switch el.(type) {
			case nil, bool, int64, float64, string:
			default:
				return true
			}
		}
		return false
	default:
		return true
	}
}

// ---- independent coercion (returns changed indicator) ----

func naiveCoerce(typ string, v any) (any, bool) {
	switch typ {
	case "long":
		switch x := v.(type) {
		case int64:
			return x, true
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) {
				return nil, false
			}
			if x < float64(math.MinInt64) || x > float64(math.MaxInt64) {
				return nil, false
			}
			return int64(x), true
		case string:
			return naiveParseInt(x)
		}
	case "double":
		switch x := v.(type) {
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, false
			}
			return x, true
		case int64:
			if x < -(1<<53) || x > 1<<53 {
				return nil, false
			}
			return float64(x), true
		}
	case "keyword":
		switch x := v.(type) {
		case string:
			return x, true
		case int64:
			return strconv.FormatInt(x, 10), true
		case bool:
			return strconv.FormatBool(x), true
		}
	case "bool":
		switch x := v.(type) {
		case bool:
			return x, true
		case string:
			if x == "true" {
				return true, true
			}
			if x == "false" {
				return false, true
			}
		}
	}
	return nil, false
}

func naiveParseInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	d := s
	if s[0] == '-' {
		neg, d = true, s[1:]
	} else if s[0] == '+' {
		return 0, false
	}
	if d == "" || (neg && d == "0") {
		return 0, false
	}
	if len(d) > 1 && d[0] == '0' {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(d); i++ {
		c := d[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
		if n > uint64(math.MaxInt64)+1 {
			return 0, false
		}
	}
	if neg {
		if n > uint64(math.MaxInt64)+1 {
			return 0, false
		}
		return -int64(n), true
	}
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func naiveInfer(v any) (string, bool) {
	switch v.(type) {
	case bool:
		return "bool", true
	case int64:
		return "long", true
	case float64:
		return "double", true
	case string:
		return "keyword", true
	}
	return "", false
}

// ---- candidate field set + normalized document, one all-or-nothing pass ----

func (nm *naiveModel) Index(id string, doc map[string]any) naiveOutcome {
	if !naiveValidateID(id) {
		return naiveOutcome{errKind: "invalid"}
	}
	if naiveValidateValue(doc, 1) {
		return naiveOutcome{errKind: "invalid"}
	}
	cand := map[string]string{}
	ignored := map[string]bool{}
	out := map[string]any{}
	errKind, errPath := nm.walk(nil, doc, out, cand, ignored)
	if errKind != "" {
		return naiveOutcome{errKind: errKind, errPath: errPath}
	}
	if len(cand) > 0 {
		for p, t := range cand {
			nm.fields[p] = t
		}
		nm.mv++
	}
	nm.docs[id] = out
	var ig []string
	for p := range ignored {
		ig = append(ig, p)
	}
	sort.Strings(ig)
	return naiveOutcome{doc: out, ignored: ig, mv: nm.mv}
}

func (nm *naiveModel) walk(path []string, in, out map[string]any, cand map[string]string, ignored map[string]bool) (string, string) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cp := append(append([]string{}, path...), key)
		pstr := strings.Join(cp, ".")
		raw := in[key]
		typ, exists := nm.fields[pstr]
		if !exists {
			typ, exists = cand[pstr]
		}
		if !exists {
			switch nm.mode {
			case mapping.DynamicFalse:
				ignored[pstr] = true
				continue
			case mapping.DynamicStrict:
				return "strict", pstr
			}
			// infer then create
			switch v := raw.(type) {
			case nil:
				continue
			case []any:
				if len(v) == 0 {
					continue
				}
				var nt string
				for _, el := range v {
					if el == nil {
						continue
					}
					t, ok := naiveInfer(el)
					if !ok {
						return "conflict", pstr
					}
					nt = t
					break
				}
				if nt == "" {
					continue
				}
				if nm.count()+len(cand) >= nm.fmax {
					return "limit", pstr
				}
				cand[pstr] = nt
				arr := []any{}
				for _, el := range v {
					if el == nil {
						arr = append(arr, nil)
						continue
					}
					cv, ok := naiveCoerce(nt, el)
					if !ok {
						return "conflict", pstr
					}
					arr = append(arr, cv)
				}
				out[key] = arr
			case map[string]any:
				if nm.count()+len(cand) >= nm.fmax {
					return "limit", pstr
				}
				cand[pstr] = "object"
				child := map[string]any{}
				if k, pp := nm.walk(cp, v, child, cand, ignored); k != "" {
					return k, pp
				}
				out[key] = child
			default:
				nt, ok := naiveInfer(v)
				if !ok {
					return "conflict", pstr
				}
				if nm.count()+len(cand) >= nm.fmax {
					return "limit", pstr
				}
				cand[pstr] = nt
				cv, ok := naiveCoerce(nt, v)
				if !ok {
					return "conflict", pstr
				}
				out[key] = cv
			}
			continue
		}
		// existing field
		switch v := raw.(type) {
		case nil:
			continue
		case []any:
			if len(v) == 0 {
				continue
			}
			arr := []any{}
			for _, el := range v {
				if el == nil {
					arr = append(arr, nil)
					continue
				}
				if typ == "object" {
					return "conflict", pstr
				}
				cv, ok := naiveCoerce(typ, el)
				if !ok {
					return "conflict", pstr
				}
				arr = append(arr, cv)
			}
			out[key] = arr
		case map[string]any:
			if typ != "object" {
				return "conflict", pstr
			}
			child := map[string]any{}
			if k, pp := nm.walk(cp, v, child, cand, ignored); k != "" {
				return k, pp
			}
			out[key] = child
		default:
			if typ == "object" {
				return "conflict", pstr
			}
			cv, ok := naiveCoerce(typ, v)
			if !ok {
				return "conflict", pstr
			}
			out[key] = cv
		}
	}
	return "", ""
}

func (nm *naiveModel) PutMapping(path []string, typ coerce.Type) (kind, conflictPath string) {
	if len(path) == 0 || !validNaiveType(typ) {
		return "invalid", ""
	}
	for _, k := range path {
		if len(k) < 1 || len(k) > 64 || strings.Contains(k, ".") {
			return "invalid", ""
		}
	}
	t := string(typ)
	cand := map[string]string{}
	cur := []string{}
	for i, key := range path {
		cur = append(cur, key)
		pstr := strings.Join(cur, ".")
		et, exists := nm.fields[pstr]
		if !exists {
			et, exists = cand[pstr]
		}
		if exists {
			want := t
			if i < len(path)-1 {
				want = "object"
			}
			if et != want {
				return "conflict", pstr
			}
			continue
		}
		nt := t
		if i < len(path)-1 {
			nt = "object"
		}
		if nm.count()+len(cand) >= nm.fmax {
			return "limit", pstr
		}
		cand[pstr] = nt
	}
	if len(cand) > 0 {
		for p, ty := range cand {
			nm.fields[p] = ty
		}
		nm.mv++
	}
	return "", ""
}

func validNaiveType(t coerce.Type) bool {
	switch t {
	case coerce.Long, coerce.Double, coerce.Keyword, coerce.Bool, coerce.Object:
		return true
	}
	return false
}

func errKind(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrTypeConflict):
		return "conflict"
	case errors.Is(err, ErrStrict):
		return "strict"
	case errors.Is(err, ErrFieldLimit):
		return "limit"
	case err == nil:
		return ""
	default:
		return "other"
	}
}

func errPath(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrNotFound):
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return strings.TrimSpace(msg[i+2:])
	}
	return ""
}

func fmtDoc(doc map[string]any) string {
	if doc == nil {
		return "nil"
	}
	return fmt.Sprintf("%#v", doc)
}
