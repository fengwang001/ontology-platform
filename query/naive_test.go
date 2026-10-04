package query

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/dls"
	"ontology/role"
)

// 本文件用“逐步朴素模拟”独立复现角色合并、文档/字段裁剪与查询语义，
// 再把实现输出与模拟输出逐字节对照；同时构造“抹除世界”
// （删掉不可见文档、抹去不可见字段后无 DLS 的朴素求值）做双重比对。

type naiveDoc struct {
	fields map[string]role.Value
}

type naiveWorld struct {
	docs    map[string]map[string]naiveDoc
	roles   map[string][]role.Entry
	binding map[string][]string
}

func newNaiveWorld() *naiveWorld {
	return &naiveWorld{
		docs:    map[string]map[string]naiveDoc{},
		roles:   map[string][]role.Entry{},
		binding: map[string][]string{},
	}
}

func classifyError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, role.ErrInvalid):
		return "invalid"
	case errors.Is(err, role.ErrUserNotFound):
		return "user-not-found"
	case errors.Is(err, dls.ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrNotFound):
		return "not-found"
	case errors.Is(err, role.ErrRoleNotFound):
		return "role-not-found"
	case errors.Is(err, role.ErrRoleInUse):
		return "role-in-use"
	default:
		return "other:" + err.Error()
	}
}

func (w *naiveWorld) matchedEntries(user, index string) []role.Entry {
	var m []role.Entry
	for _, rn := range w.binding[user] {
		for _, en := range w.roles[rn] {
			if role.MatchPattern(en.Pattern, index) {
				m = append(m, en)
			}
		}
	}
	return m
}

func (w *naiveWorld) docVisible(m []role.Entry, fields map[string]role.Value) bool {
	for _, en := range m {
		if en.Filter == nil {
			return true
		}
	}
	for _, en := range m {
		if en.Filter != nil && role.Eval(*en.Filter, fields) {
			return true
		}
	}
	return false
}

func (w *naiveWorld) fieldVisible(m []role.Entry, field string) bool {
	for _, en := range m {
		if en.Fields.Unrestricted {
			return true
		}
	}
	for _, en := range m {
		if !en.Fields.Unrestricted && en.Fields.AllowsField(field) {
			return true
		}
	}
	return false
}

type naiveResult struct {
	errClass string
	docs     []Doc
	total    int
	agg      []AggItem
}

func (w *naiveWorld) prune(m []role.Entry, full map[string]role.Value) map[string]role.Value {
	view := map[string]role.Value{}
	for f, v := range full {
		if w.fieldVisible(m, f) {
			view[f] = v
		}
	}
	return view
}

func (w *naiveWorld) resolve(user, index string) ([]role.Entry, bool, string) {
	if _, ok := w.binding[user]; !ok {
		return nil, false, "user-not-found"
	}
	_, indexExists := w.docs[index]
	m := w.matchedEntries(user, index)
	if !indexExists || len(m) == 0 {
		return nil, false, "forbidden"
	}
	return m, true, "ok"
}

func (w *naiveWorld) runSearch(user, index string, q role.Expr, size int) naiveResult {
	if len(user) < 1 || len(user) > 64 || len(index) < 1 || len(index) > 64 ||
		size < 1 || size > 1000 {
		return naiveResult{errClass: "invalid"}
	}
	if err := role.ValidateExpr(q); err != nil {
		return naiveResult{errClass: "invalid"}
	}
	m, ok, class := w.resolve(user, index)
	if !ok {
		return naiveResult{errClass: class}
	}
	var ids []string
	for id := range w.docs[index] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	res := naiveResult{errClass: "ok", docs: []Doc{}}
	for _, id := range ids {
		full := w.docs[index][id].fields
		if !w.docVisible(m, full) {
			continue
		}
		view := w.prune(m, full)
		if !role.Eval(q, view) {
			continue
		}
		res.total++
		if len(res.docs) < size {
			res.docs = append(res.docs, Doc{ID: id, Fields: view})
		}
	}
	return res
}

func (w *naiveWorld) runGet(user, index, id string) naiveResult {
	if len(user) < 1 || len(user) > 64 || len(index) < 1 || len(index) > 64 ||
		len(id) < 1 || len(id) > 64 {
		return naiveResult{errClass: "invalid"}
	}
	m, ok, class := w.resolve(user, index)
	if !ok {
		return naiveResult{errClass: class}
	}
	d, exists := w.docs[index][id]
	if !exists || !w.docVisible(m, d.fields) {
		return naiveResult{errClass: "not-found"}
	}
	return naiveResult{errClass: "ok", docs: []Doc{{ID: id, Fields: w.prune(m, d.fields)}}}
}

func (w *naiveWorld) runAgg(user, index, field string) naiveResult {
	if len(user) < 1 || len(user) > 64 || len(index) < 1 || len(index) > 64 ||
		len(field) < 1 || len(field) > 64 {
		return naiveResult{errClass: "invalid"}
	}
	m, ok, class := w.resolve(user, index)
	if !ok {
		return naiveResult{errClass: class}
	}
	res := naiveResult{errClass: "ok", agg: []AggItem{}}
	if !w.fieldVisible(m, field) {
		return res
	}
	counts := map[role.Value]int{}
	for _, d := range w.docs[index] {
		if !w.docVisible(m, d.fields) {
			continue
		}
		if v, has := d.fields[field]; has {
			counts[v]++
		}
	}
	var ints []int64
	var strs []string
	for v := range counts {
		switch x := v.(type) {
		case int64:
			ints = append(ints, x)
		case string:
			strs = append(strs, x)
		}
	}
	sort.Slice(ints, func(i, j int) bool { return ints[i] < ints[j] })
	sort.Strings(strs)
	for _, x := range ints {
		res.agg = append(res.agg, AggItem{x, counts[x]})
	}
	for _, x := range strs {
		res.agg = append(res.agg, AggItem{x, counts[x]})
	}
	return res
}

// erasedWorld 构造抹除世界：仅保留 (user,index) 可见文档的可见字段，
// 再挂一个全开角色。其上朴素求值必须与原世界实现结果相同。
func (w *naiveWorld) erasedWorld(user, index string) (*naiveWorld, string) {
	m, ok, class := w.resolve(user, index)
	ew := newNaiveWorld()
	if !ok {
		return ew, class
	}
	ew.roles["__OPEN__"] = []role.Entry{{Pattern: index, Fields: role.FieldAuth{Unrestricted: true}}}
	ew.binding[user] = []string{"__OPEN__"}
	ew.docs[index] = map[string]naiveDoc{}
	for id, d := range w.docs[index] {
		if !w.docVisible(m, d.fields) {
			continue
		}
		ew.docs[index][id] = naiveDoc{fields: w.prune(m, d.fields)}
	}
	return ew, "ok"
}

func docsEqual(a, b []Doc) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || len(a[i].Fields) != len(b[i].Fields) {
			return false
		}
		for k, v := range a[i].Fields {
			if bv, ok := b[i].Fields[k]; !ok || bv != v {
				return false
			}
		}
	}
	return true
}

func aggEqual(a, b []AggItem) bool {
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

var (
	rIndices = []string{"logs-1", "logs-2", "logs-9", "other", "new-1"}
	rFields  = []string{"team", "level", "secret", "zone"}
	rUsers   = []string{"u1", "u2", "u3"}
	rRoles   = []string{"R1", "R2", "R3", "R4"}
)

var _ = sync.Once{}

func randPattern(rng *rand.Rand) string {
	switch rng.IntN(4) {
	case 0:
		return "*"
	case 1:
		return "logs-*"
	case 2:
		return "new-*"
	default:
		return rIndices[rng.IntN(len(rIndices))]
	}
}

func randFieldPattern(rng *rand.Rand) string {
	switch rng.IntN(4) {
	case 0:
		return "*"
	case 1:
		return "se*"
	case 2:
		return "lev*"
	default:
		return rFields[rng.IntN(len(rFields))]
	}
}

func randValue(rng *rand.Rand, field string) role.Value {
	if field == "level" || rng.IntN(2) == 0 {
		return int64(rng.IntN(11))
	}
	return []string{"a", "b", "k1", "k2"}[rng.IntN(4)]
}

func randExpr(rng *rand.Rand, depth int) role.Expr {
	field := rFields[rng.IntN(len(rFields))]
	if depth >= 2 || rng.IntN(3) == 0 {
		if field == "level" && rng.IntN(2) == 0 {
			lo := int64(rng.IntN(11))
			hi := lo + int64(rng.IntN(4))
			e, _ := role.Range(field, lo, hi)
			return e
		}
		e, _ := role.Term(field, randValue(rng, field))
		return e
	}
	n := 1 + rng.IntN(2)
	kids := make([]role.Expr, n)
	for i := range kids {
		kids[i] = randExpr(rng, depth+1)
	}
	switch rng.IntN(3) {
	case 0:
		e, _ := role.Not(kids[0])
		return e
	case 1:
		e, _ := role.And(kids...)
		return e
	default:
		e, _ := role.Or(kids...)
		return e
	}
}

func randFieldAuth(rng *rand.Rand) role.FieldAuth {
	if rng.IntN(4) == 0 {
		return role.FieldAuth{Unrestricted: true}
	}
	fa := role.FieldAuth{Grant: []string{randFieldPattern(rng)}}
	if rng.IntN(2) == 0 {
		fa.Grant = append(fa.Grant, "*")
	}
	if rng.IntN(2) == 0 {
		fa.Except = []string{"secret"}
	}
	return fa
}

func randEntries(rng *rand.Rand) []role.Entry {
	n := 1 + rng.IntN(3)
	entries := make([]role.Entry, n)
	for i := range entries {
		var filter *role.Expr
		if rng.IntN(3) != 0 {
			e := randExpr(rng, 0)
			filter = &e
		}
		entries[i] = role.Entry{Pattern: randPattern(rng), Filter: filter, Fields: randFieldAuth(rng)}
	}
	return entries
}

func cloneWorldFields(in map[string]map[string]naiveDoc, index string, id string, fields map[string]role.Value) {
	cp := make(map[string]role.Value, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	in[index][id] = naiveDoc{fields: cp}
}

func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 12
	totalChecks := 0
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewPCG(uint64(seq+1)*7919+1, uint64(seq+1)*104729+7))
		store := NewStore()
		w := newNaiveWorld()
		var logs []string
		checks := 0
		totalChecks += 0

		logf := func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		}

		for op := 0; op < opsPerSeq; op++ {
			switch rng.IntN(7) {
			case 0: // PutDoc
				idx := rIndices[rng.IntN(len(rIndices))]
				id := fmt.Sprintf("d%d", rng.IntN(5))
				fields := map[string]role.Value{}
				for _, f := range rFields {
					if rng.IntN(2) == 0 {
						fields[f] = randValue(rng, f)
					}
				}
				if len(fields) == 0 {
					fields["team"] = "a"
				}
				store.PutDoc(idx, id, fields)
				if _, ok := w.docs[idx]; !ok {
					w.docs[idx] = map[string]naiveDoc{}
				}
				cloneWorldFields(w.docs, idx, id, fields)
				logf("[%02d] PutDoc(%s,%s,%v)", op, idx, id, fields)
			case 1: // DeleteDoc
				idx := rIndices[rng.IntN(len(rIndices))]
				id := fmt.Sprintf("d%d", rng.IntN(5))
				store.DeleteDoc(idx, id)
				if ix, ok := w.docs[idx]; ok {
					delete(ix, id)
				}
				logf("[%02d] DeleteDoc(%s,%s)", op, idx, id)
			case 2: // PutRole
				name := rRoles[rng.IntN(len(rRoles))]
				entries := randEntries(rng)
				err := store.Registry().PutRole(name, entries)
				class := classifyError(err)
				if class != "invalid" && class != "ok" {
					t.Fatalf("seq=%d op=%d PutRole unexpected class %s", seq, op, class)
				}
				if class == "ok" {
					w.roles[name] = entries
				}
				logf("[%02d] PutRole(%s,%d entries) -> %s", op, name, len(entries), class)
			case 3: // DeleteRole
				name := rRoles[rng.IntN(len(rRoles))]
				err := store.Registry().DeleteRole(name)
				class := classifyError(err)
				inUse := false
				for _, bound := range w.binding {
					for _, rn := range bound {
						if rn == name {
							inUse = true
						}
					}
				}
				_, exists := w.roles[name]
				wantClass := "role-not-found"
				if exists {
					if inUse {
						wantClass = "role-in-use"
					} else {
						wantClass = "ok"
						delete(w.roles, name)
					}
				}
				if class != wantClass {
					t.Fatalf("seq=%d DeleteRole(%s): %s want %s\n%s", seq, name, class, wantClass, strings.Join(logs, "\n"))
				}
				logf("[%02d] DeleteRole(%s) -> %s", op, name, class)
			case 4: // BindUser
				user := rUsers[rng.IntN(len(rUsers))]
				n := 1 + rng.IntN(4)
				names := make([]string, n)
				for i := range names {
					names[i] = rRoles[rng.IntN(len(rRoles))]
				}
				err := store.Registry().BindUser(user, names)
				class := classifyError(err)
				dup := map[string]bool{}
				hasDup, missing := false, false
				for _, rn := range names {
					if dup[rn] {
						hasDup = true
					}
					dup[rn] = true
					if _, ok := w.roles[rn]; !ok {
						missing = true
					}
				}
				wantClass := "ok"
				switch {
				case hasDup:
					wantClass = "invalid"
				case missing:
					wantClass = "role-not-found"
				}
				if class != wantClass {
					t.Fatalf("seq=%d BindUser(%s,%v): %s want %s\n%s", seq, user, names, class, wantClass, strings.Join(logs, "\n"))
				}
				if class == "ok" {
					w.binding[user] = append([]string(nil), names...)
				}
				logf("[%02d] BindUser(%s,%v) -> %s", op, user, names, class)
			case 5: // Search
				user := rUsers[rng.IntN(len(rUsers))]
				idx := rIndices[rng.IntN(len(rIndices))]
				q := randExpr(rng, 0)
				size := 1 + rng.IntN(5)
				docs, total, err := store.Search(user, idx, q, size)
				want := w.runSearch(user, idx, q, size)
				class := classifyError(err)
				ew, ewClass := w.erasedWorld(user, idx)
				var ewRes naiveResult
				if ewClass == "ok" {
					ewRes = ew.runSearch(user, idx, q, size)
				}
				if class != want.errClass ||
					(class == "ok" && (total != want.total || !docsEqual(docs, want.docs))) ||
					(ewClass == "ok" && (total != ewRes.total || !docsEqual(docs, ewRes.docs))) {
					t.Fatalf("seq=%d Search(%s,%s,size=%d) class=%s wantClass=%s total=%d/%d\nGOT=%v\nWANT=%v\nEW=%v\n%s",
						seq, user, idx, size, class, want.errClass, total, want.total, docs, want.docs, ewRes.docs,
						strings.Join(logs, "\n"))
				}
				checks++
				logf("[%02d] Search(user=%s,index=%s,size=%d) -> class=%s total=%d n=%d", op, user, idx, size, class, total, len(docs))
			case 6: // Get / Agg 二选一
				user := rUsers[rng.IntN(len(rUsers))]
				idx := rIndices[rng.IntN(len(rIndices))]
				if rng.IntN(2) == 0 {
					id := fmt.Sprintf("d%d", rng.IntN(5))
					d, err := store.Get(user, idx, id)
					want := w.runGet(user, idx, id)
					class := classifyError(err)
					ew, ewClass := w.erasedWorld(user, idx)
					if class != want.errClass {
						t.Fatalf("seq=%d Get(%s,%s,%s): %s want %s\n%s", seq, user, idx, id, class, want.errClass, strings.Join(logs, "\n"))
					}
					if class == "ok" {
						if d.ID != want.docs[0].ID || !docsEqual([]Doc{d}, want.docs) {
							t.Fatalf("seq=%d Get mismatch %v vs %v", seq, d, want.docs)
						}
						ewRes := ew.runGet(user, idx, id)
						if ewClass != "ok" || !docsEqual([]Doc{d}, ewRes.docs) {
							t.Fatalf("seq=%d Get erased mismatch ewClass=%s", seq, ewClass)
						}
					}
					checks++
					logf("[%02d] Get(%s,%s,%s) -> %s", op, user, idx, id, class)
				} else {
					field := rFields[rng.IntN(len(rFields))]
					got, err := store.Agg(user, idx, field)
					want := w.runAgg(user, idx, field)
					class := classifyError(err)
					ew, ewClass := w.erasedWorld(user, idx)
					var ewRes naiveResult
					if ewClass == "ok" {
						ewRes = ew.runAgg(user, idx, field)
					}
					if class != want.errClass ||
						(class == "ok" && (!aggEqual(got, want.agg) || !aggEqual(got, ewRes.agg))) {
						t.Fatalf("seq=%d Agg(%s,%s,%s): class=%s want=%s\nGOT=%v\nWANT=%v\nEW=%v\n%s",
							seq, user, idx, field, class, want.errClass, got, want.agg, ewRes.agg, strings.Join(logs, "\n"))
					}
					checks++
					logf("[%02d] Agg(%s,%s,%s) -> class=%s items=%v", op, user, idx, field, class, got)
				}
			}
		}
		totalChecks += checks
		if seq < 3 {
			t.Logf("seq=%d checks=%d 判定依据与输入输出：\n%s", seq, checks, strings.Join(logs, "\n"))
		}
	}
	t.Logf("sequences=%d total permission-decision checks=%d", sequences, totalChecks)
}
