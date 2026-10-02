package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randomSchema builds a random valid-ish schema tree.
func randomSchema(rng *rand.Rand) []Field {
	leafBudget := 16
	var gen func(depth int) []Field
	gen = func(depth int) []Field {
		n := 1 + rng.Intn(3)
		var fs []Field
		for i := 0; i < n; i++ {
			name := string(rune('a' + i))
			makeLeaf := depth >= 5 || leafBudget <= 1 || rng.Intn(2) == 0
			if makeLeaf {
				fs = append(fs, Field{Name: name, Rep: randomRep(rng)})
				leafBudget--
			} else {
				ch := gen(depth + 1)
				if len(ch) == 0 {
					fs = append(fs, Field{Name: name, Rep: randomRep(rng)})
					leafBudget--
				} else {
					fs = append(fs, Field{Name: name, Rep: randomRep(rng), Children: ch})
				}
			}
		}
		return fs
	}
	for {
		leafBudget = 16
		sc := gen(0)
		paths := collectLeaves(sc)
		if len(paths) >= 1 && len(paths) <= 16 {
			return sc
		}
	}
}

func randomRep(rng *rand.Rand) Rep {
	return Rep(rng.Intn(3))
}

type testLeaf struct {
	fields []*Field
	names  []string
	path   string
}

func collectLeaves(fields []Field) []testLeaf {
	var out []testLeaf
	var walk func(fs []Field, pf []*Field, pn []string)
	walk = func(fs []Field, pf []*Field, pn []string) {
		for i := range fs {
			f := &fs[i]
			cf := append(append([]*Field{}, pf...), f)
			cn := append(append([]string{}, pn...), f.Name)
			if len(f.Children) == 0 {
				path := ""
				for j, nm := range cn {
					if j > 0 {
						path += "."
					}
					path += nm
				}
				out = append(out, testLeaf{fields: cf, names: cn, path: path})
			} else {
				walk(f.Children, cf, cn)
			}
		}
	}
	walk(fields, nil, nil)
	return out
}

func genValidRecord(fields []Field, rng *rand.Rand) map[string]any {
	m := map[string]any{}
	for i := range fields {
		f := &fields[i]
		switch f.Rep {
		case Required:
			m[f.Name] = genValue(f, rng)
		case Optional:
			switch rng.Intn(3) {
			case 0:
				// omitted
			case 1:
				m[f.Name] = nil
			case 2:
				m[f.Name] = genValue(f, rng)
			}
		case Repeated:
			switch rng.Intn(4) {
			case 0:
				// omitted
			case 1:
				m[f.Name] = nil
			case 2:
				m[f.Name] = []any{}
			case 3:
				n := rng.Intn(3)
				arr := make([]any, n)
				for j := range arr {
					arr[j] = genElement(f, rng)
				}
				m[f.Name] = arr
			}
		}
	}
	return m
}

func genValue(f *Field, rng *rand.Rand) any {
	if len(f.Children) == 0 {
		return int64(rng.Intn(21) - 10)
	}
	return genValidRecord(f.Children, rng)
}

func genElement(f *Field, rng *rand.Rand) any {
	if len(f.Children) == 0 {
		return int64(rng.Intn(21) - 10)
	}
	return genValidRecord(f.Children, rng)
}

// normalizeRecord returns the canonical form: missing optionals and
// empty repeats dropped, present optional groups kept (possibly empty).
func normalizeRecord(fields []Field, m map[string]any) map[string]any {
	out := map[string]any{}
	for i := range fields {
		f := &fields[i]
		v, present := m[f.Name]
		if !present || v == nil {
			if f.Rep == Required {
				panic("missing required in supposedly-valid record")
			}
			continue
		}
		if f.Rep == Repeated {
			arr := v.([]any)
			if len(arr) == 0 {
				continue
			}
			if len(f.Children) == 0 {
				cp := make([]any, len(arr))
				copy(cp, arr)
				out[f.Name] = cp
			} else {
				elems := make([]any, len(arr))
				for j, e := range arr {
					elems[j] = normalizeRecord(f.Children, e.(map[string]any))
				}
				out[f.Name] = elems
			}
			continue
		}
		if len(f.Children) == 0 {
			out[f.Name] = v
		} else {
			out[f.Name] = normalizeRecord(f.Children, v.(map[string]any))
		}
	}
	return out
}

// naiveEmit is an independent implementation of the spec's entry rules.
func naiveEmit(tl testLeaf, rec map[string]any) []Entry {
	var out []Entry
	var walk func(seg int, node any, def, repIn int)
	walk = func(seg int, node any, def, repIn int) {
		f := tl.fields[seg]
		last := seg == len(tl.fields)-1
		get := func() (any, bool) {
			m := node.(map[string]any)
			v, ok := m[f.Name]
			return v, ok
		}
		if f.Rep == Repeated {
			var arr []any
			if v, ok := get(); ok && v != nil {
				arr = v.([]any)
			}
			if len(arr) == 0 {
				out = append(out, Entry{Rep: repIn, Def: def, Null: true})
				return
			}
			level := 0
			for k := 0; k <= seg; k++ {
				if tl.fields[k].Rep == Repeated {
					level++
				}
			}
			for j, elem := range arr {
				rep := repIn
				if j > 0 {
					rep = level
				}
				if last {
					out = append(out, Entry{Rep: rep, Def: def + 1, Value: elem.(int64)})
				} else {
					walk(seg+1, elem, def+1, rep)
				}
			}
			return
		}
		v, present := get()
		if f.Rep == Optional && (!present || v == nil) {
			out = append(out, Entry{Rep: repIn, Def: def, Null: true})
			return
		}
		if f.Rep == Optional {
			def++
		}
		if last {
			out = append(out, Entry{Rep: repIn, Def: def, Value: v.(int64)})
			return
		}
		walk(seg+1, v, def, repIn)
	}
	walk(0, rec, 0, 0)
	return out
}

func naivePages(counts []int, pageEntries int) []PageInfo {
	var pages []PageInfo
	cur := PageInfo{}
	started := false
	for recIdx, n := range counts {
		if !started {
			cur = PageInfo{StartRecord: recIdx}
			started = true
		} else if cur.EntryCount >= pageEntries {
			pages = append(pages, cur)
			cur = PageInfo{StartRecord: recIdx}
		}
		cur.RecordCount++
		cur.EntryCount += n
	}
	if started {
		pages = append(pages, cur)
	}
	return pages
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	const cases = 2000
	for iter := 0; iter < cases; iter++ {
		schema := randomSchema(rng)
		leaves := collectLeaves(schema)
		pageEntries := 1 + rng.Intn(4)
		s, err := New(schema, pageEntries, 10000)
		if err != nil {
			t.Fatalf("iter %d: valid schema rejected: %v\nschema=%s", iter, err, fmtSchema(schema))
		}

		recordN := 1 + rng.Intn(5)
		var recs []map[string]any
		var norms []map[string]any
		naiveEntries := map[string][]Entry{}
		naiveCounts := map[string][]int{}
		for _, tl := range leaves {
			naiveEntries[tl.path] = nil
			naiveCounts[tl.path] = nil
		}

		for r := 0; r < recordN; r++ {
			rec := genValidRecord(schema, rng)
			recs = append(recs, rec)
			norms = append(norms, normalizeRecord(schema, rec))
			if err := s.Shred(rec); err != nil {
				t.Fatalf("iter %d rec %d: %v\nrec=%s", iter, r, err, fmtMap(rec))
			}
			for _, tl := range leaves {
				es := naiveEmit(tl, rec)
				naiveEntries[tl.path] = append(naiveEntries[tl.path], es...)
				naiveCounts[tl.path] = append(naiveCounts[tl.path], len(es))
			}
		}

		// Entries per column.
		for _, tl := range leaves {
			got := s.Entries(tl.path)
			if !reflect.DeepEqual(got, naiveEntries[tl.path]) {
				t.Fatalf("iter %d col %s entries mismatch\ngot  %+v\nwant %+v",
					iter, tl.path, got, naiveEntries[tl.path])
			}
			wantPages := naivePages(naiveCounts[tl.path], pageEntries)
			if gotPages := s.Pages(tl.path); !reflect.DeepEqual(gotPages, wantPages) {
				t.Fatalf("iter %d col %s pages mismatch\ngot  %+v\nwant %+v",
					iter, tl.path, gotPages, wantPages)
			}
			if got := s.RecordCount(); got != recordN {
				t.Fatalf("iter %d record count %d", iter, got)
			}
		}

		gotRecs := s.Assemble()
		if len(gotRecs) != len(norms) {
			t.Fatalf("iter %d assembled count %d want %d", iter, len(gotRecs), len(norms))
		}
		for r := range norms {
			if !reflect.DeepEqual(gotRecs[r], norms[r]) {
				t.Fatalf("iter %d rec %d mismatch\ngot  %s\nwant %s",
					iter, r, fmtMap(gotRecs[r]), fmtMap(norms[r]))
			}
		}

		var totalEntries int
		for _, es := range naiveEntries {
			totalEntries += len(es)
		}
		if got := s.EntriesRead(); got != int64(totalEntries) {
			t.Fatalf("iter %d entriesRead=%d want %d", iter, got, totalEntries)
		}

		t.Logf("case %d PASS: pageEntries=%d records=%d leaves=%d entries=%d; input schema=%s; inputs=%s; output=%s; verdict: entries/pages/stats/assemble equal to naive model and normalized inputs",
			iter, pageEntries, recordN, len(leaves), totalEntries,
			fmtSchema(schema), fmtMaps(recs), fmtMaps(gotRecs))
	}
}

func fmtSchema(fs []Field) string {
	var parts []string
	var walk func(fields []Field, indent string)
	walk = func(fields []Field, indent string) {
		for i := range fields {
			f := &fields[i]
			kind := []string{"req", "opt", "rep"}[f.Rep]
			if len(f.Children) == 0 {
				parts = append(parts, fmt.Sprintf("%s%s:%s:int64", indent, f.Name, kind))
			} else {
				parts = append(parts, fmt.Sprintf("%s%s:%s{", indent, f.Name, kind))
				walk(f.Children, indent+"  ")
				parts = append(parts, indent+"}")
			}
		}
	}
	walk(fs, "")
	return joinParts(parts)
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

func fmtMap(m map[string]any) string {
	return fmt.Sprintf("%#v", m)
}

func fmtMaps(ms []map[string]any) string {
	var out []string
	for _, m := range ms {
		out = append(out, fmtMap(m))
	}
	return "[" + joinStrings(out, " | ") + "]"
}

func joinStrings(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
