package ingest

import (
	"flag"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/coerce"
	"ontology/mapping"
)

var logAllOps = flag.Bool("logops", true, "log every random operation input/output/reason")

var scalarPool = []any{
	int64(0), int64(1), int64(-7), int64(1) << 53, (int64(1) << 53) + 1,
	2.0, 2.5, 1.5, -0.0, 3.14,
	"2", "-7", "07", "-0", "0", "true", "false", "TRUE", "abc", "+1", "9223372036854775808",
	true, false,
}

var fuzzTypes = []coerce.Type{coerce.Long, coerce.Double, coerce.Keyword, coerce.Bool, coerce.Object}
var fuzzModes = []mapping.Mode{mapping.DynamicTrue, mapping.DynamicFalse, mapping.DynamicStrict}

type op struct {
	kind string // index or put
	id   string
	doc  map[string]any
	bad  bool // deliberately malformed
	path []string
	typ  coerce.Type
}

func randKey(r *rand.Rand) string {
	switch r.Intn(10) {
	case 0:
		return "a.b" // illegal dot
	case 1:
		return strings.Repeat("z", 65) // too long
	default:
		return string(rune('a'+r.Intn(6))) + string(rune('0'+r.Intn(4)))
	}
}

func randScalar(r *rand.Rand) any {
	return scalarPool[r.Intn(len(scalarPool))]
}

func randValue(r *rand.Rand, depth int, allowBad bool) any {
	n := r.Intn(100)
	switch {
	case n < 12:
		return nil
	case n < 30:
		return randScalar(r)
	case n < 45:
		// array; illegal element only when allowBad and rare
		l := r.Intn(4)
		arr := make([]any, 0, l)
		for i := 0; i < l; i++ {
			if allowBad && r.Intn(30) == 0 {
				arr = append(arr, map[string]any{"o": int64(1)})
			} else {
				arr = append(arr, randScalarOrNil(r))
			}
		}
		return arr
	case depth < 8:
		return randObject(r, depth+1, allowBad)
	default:
		return randScalar(r)
	}
}

func randScalarOrNil(r *rand.Rand) any {
	if r.Intn(5) == 0 {
		return nil
	}
	return randScalar(r)
}

func randObject(r *rand.Rand, depth int, allowBad bool) map[string]any {
	n := 1 + r.Intn(4)
	obj := map[string]any{}
	for i := 0; i < n; i++ {
		key := randKey(r)
		if !allowBad {
			for strings.Contains(key, ".") || len(key) > 64 {
				key = randKey(r)
			}
		}
		if depth > 8 {
			// guarantee depth violation at the boundary
			obj[key] = map[string]any{"x": int64(1)}
			continue
		}
		obj[key] = randValue(r, depth, allowBad)
	}
	return obj
}

func randDoc(r *rand.Rand) (map[string]any, bool) {
	allowBad := r.Intn(8) == 0
	return randObject(r, 1, allowBad), allowBad
}

func randOp(r *rand.Rand, seq int) op {
	if r.Intn(5) == 0 {
		depth := 1 + r.Intn(3)
		path := make([]string, depth)
		for i := range path {
			path[i] = fmt.Sprintf("%c%c",
				rune('a'+r.Intn(6)), rune('0'+r.Intn(4)))
		}
		if r.Intn(12) == 0 {
			path[0] = "a.b"
		}
		return op{kind: "put", path: path, typ: fuzzTypes[r.Intn(len(fuzzTypes))]}
	}
	id := fmt.Sprintf("doc%d", r.Intn(6))
	if r.Intn(20) == 0 {
		id = ""
	}
	doc, bad := randDoc(r)
	return op{kind: "index", id: id, doc: doc, bad: bad}
}

func fieldsOf(st *Store) map[string]string {
	src := st.Fields()
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = string(v)
	}
	return out
}

func TestRandomCompareNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		mode := fuzzModes[r.Intn(len(fuzzModes))]
		fmax := 1 + r.Intn(12)
		st, err := New(mode, fmax)
		if err != nil {
			t.Fatal(err)
		}
		nm := newNaive(mode, fmax)
		nOps := 2 + r.Intn(12)

		var log strings.Builder
		fmt.Fprintf(&log, "--- sequence %d mode=%v fmax=%d ---", seq, mode, fmax)

		for j := 0; j < nOps; j++ {
			o := randOp(r, seq)
			if o.kind == "index" {
				fmt.Fprintf(&log, "\nIndex(%q, %s)", o.id, fmtDoc(o.doc))
				res, ierr := st.Index(o.id, o.doc)
				no := nm.Index(o.id, o.doc)
				reason := compareOutcomes(t, st, res, ierr, no, nm, &log)
				if reason != "" {
					t.Fatalf("seq=%d op=%d mismatch:\n%s", seq, j, log.String())
				}
				if ierr == nil {
					// stored doc must equal returned normalized doc
					got, gerr := st.Get(o.id)
					if gerr != nil || !reflect.DeepEqual(got, res.Doc) {
						t.Fatalf("Get mismatch seq=%d: %v %#v vs %#v", seq, gerr, got, res.Doc)
					}
				}
			} else {
				fmt.Fprintf(&log, "\nPutMapping(%v, %s)", o.path, o.typ)
				perr := st.PutMapping(o.path, o.typ)
				nk, np := nm.PutMapping(o.path, o.typ)
				gotKind := errKind(perr)
				if gotKind != nk {
					fmt.Fprintf(&log,
						"\n  MISMATCH put kind: impl=%v(%v) naive=%v(%s)",
						gotKind, perr, nk, np)
					t.Fatalf("seq=%d put mismatch:\n%s", seq, log.String())
				}
				if nk == "conflict" && errPath(perr) != np {
					fmt.Fprintf(&log,
						"\n  MISMATCH put path: impl=%q naive=%q", errPath(perr), np)
					t.Fatalf("seq=%d put path mismatch:\n%s", seq, log.String())
				}
				fmt.Fprintf(&log, "\n  -> kind=%s path=%s mv=%d", nk, np, st.MV())
			}
		}
		if *logAllOps {
			t.Log(log.String())
		}
	}
}

func compareOutcomes(t *testing.T, st *Store, res *IndexResult, ierr error, no naiveOutcome, nm *naiveModel, log *strings.Builder) string {
	ik := errKind(ierr)
	switch {
	case ik != no.errKind:
		fmt.Fprintf(log, "\n  MISMATCH kind: impl=%q(%v) naive=%q path=%q",
			ik, ierr, no.errKind, no.errPath)
		return "kind"
	case ierr != nil && errPath(ierr) != no.errPath:
		fmt.Fprintf(log, "\n  MISMATCH path: impl=%q naive=%q",
			errPath(ierr), no.errPath)
		return "path"
	case ierr != nil:
		fmt.Fprintf(log, "\n  -> rejected kind=%s path=%s (both agree)", ik, no.errPath)
		return ""
	}
	// both accepted: compare mv, normalized doc, ignored, fields
	fmt.Fprintf(log, "\n  -> accepted mv=%d doc=%s ignored=%v",
		res.MV, fmtDoc(res.Doc), res.Ignored)
	if res.MV != no.mv {
		fmt.Fprintf(log, "\n  MISMATCH mv: impl=%d naive=%d", res.MV, no.mv)
		return "mv"
	}
	if !reflect.DeepEqual(res.Doc, no.doc) {
		fmt.Fprintf(log, "\n  MISMATCH doc:\n    impl =%s\n    naive=%s",
			fmtDoc(res.Doc), fmtDoc(no.doc))
		return "doc"
	}
	ig := res.Ignored
	if ig == nil {
		ig = []string{}
	}
	ni := no.ignored
	if ni == nil {
		ni = []string{}
	}
	if !reflect.DeepEqual(ig, ni) {
		fmt.Fprintf(log, "\n  MISMATCH ignored: impl=%v naive=%v", ig, ni)
		return "ignored"
	}
	implFields := fieldsOf(st)
	if !reflect.DeepEqual(implFields, nm.fields) {
		fmt.Fprintf(log, "\n  MISMATCH fields:\n    impl =%v\n    naive=%v",
			implFields, nm.fields)
		return "fields"
	}
	return ""
}
