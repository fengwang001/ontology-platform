package query

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"ontology/role"
)

// 本测试用独立编写的朴素逐步模拟（不调用 dls 包），对随机操作序列与引擎做对照。
// 朴素侧严格按规格执行：先在完整文档上判可见文档集合，构造"抹除后的世界"
//（删掉不可见文档、抹去不可见字段），再在该世界上朴素地重做请求。

const naiveRuns = 1500

var (
	nIndexes = []string{"ix-a", "ix-b", "ix-z"}
	nUsers   = []string{"u1", "u2"}
	nFields  = []string{"team", "level", "secret", "note"}
	nPat     = []string{"ix-a", "ix-b", "ix-*", "*"}
	nGrant   = []string{"team", "level", "secret", "note", "*"}
	nExcept  = []string{"secret", "note"}
)

type nDoc struct{ fields map[string]role.Value }

type nState struct {
	docs    map[string]map[string]*nDoc
	roles   map[string][]role.Entry
	binding map[string][]string
}

func newNState() *nState {
	return &nState{
		docs:    map[string]map[string]*nDoc{},
		roles:   map[string][]role.Entry{},
		binding: map[string][]string{},
	}
}

type nView struct {
	docIDs map[string]bool
	fields map[string]bool
	reason string
}

func (n *nState) matchedEntries(user string) ([][]role.Entry, bool) {
	names, bound := n.binding[user]
	if !bound {
		return nil, false
	}
	var groups [][]role.Entry
	for _, name := range names {
		if entries, ok := n.roles[name]; ok {
			groups = append(groups, entries)
		}
	}
	return groups, true
}

// resolve 在完整文档上判文档侧，在字段授权上判字段侧，两侧独立合并。
func (n *nState) resolve(user, index string) (*nView, error) {
	groups, bound := n.matchedEntries(user)
	if !bound {
		return nil, role.ErrNoUser
	}
	if _, ok := n.docs[index]; !ok {
		return nil, role.ErrNoPerm
	}
	matchedCount := 0
	docOpen := false
	fieldOpen := false
	var filters []*role.Expr
	var auths []role.FieldAuth
	for _, entries := range groups {
		for i := range entries {
			entry := &entries[i]
			if !role.MatchPattern(entry.IndexPattern, index) {
				continue
			}
			matchedCount++
			if entry.Filter == nil {
				docOpen = true
			} else {
				filters = append(filters, entry.Filter)
			}
			if entry.Fields.Unrestricted {
				fieldOpen = true
			} else {
				auths = append(auths, entry.Fields)
			}
		}
	}
	if matchedCount == 0 {
		return nil, role.ErrNoPerm
	}

	view := &nView{docIDs: map[string]bool{}, fields: map[string]bool{}}
	for id, doc := range n.docs[index] {
		visible := docOpen
		if !visible {
			for _, filter := range filters {
				if role.Eval(*filter, doc.fields) {
					visible = true
					break
				}
			}
		}
		view.docIDs[id] = visible
	}
	for _, doc := range n.docs[index] {
		for field := range doc.fields {
			vis := fieldOpen
			if !vis {
				for _, auth := range auths {
					if auth.FieldVisible(field) {
						vis = true
						break
					}
				}
			}
			view.fields[field] = vis
		}
	}
	view.reason = fmt.Sprintf("M=%d docOpen=%v fieldOpen=%v filters=%d auths=%d",
		matchedCount, docOpen, fieldOpen, len(filters), len(auths))
	return view, nil
}

// erasedWorld 构造抹除后的世界：只保留可见文档，只保留可见字段。
func (n *nState) erasedWorld(index string, view *nView) map[string]map[string]role.Value {
	world := map[string]map[string]role.Value{}
	for id, doc := range n.docs[index] {
		if !view.docIDs[id] {
			continue
		}
		stripped := map[string]role.Value{}
		for field, value := range doc.fields {
			if view.fields[field] {
				stripped[field] = value
			}
		}
		world[id] = stripped
	}
	return world
}

type nSearchOut struct {
	docs  []Doc
	total int
}

func (n *nState) search(user, index string, q role.Expr, size int) (*nSearchOut, error) {
	view, err := n.resolve(user, index)
	if err != nil {
		return nil, err
	}
	world := n.erasedWorld(index, view)
	var ids []string
	for id, fields := range world {
		if role.Eval(q, fields) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := &nSearchOut{total: len(ids)}
	if len(ids) > size {
		ids = ids[:size]
	}
	for _, id := range ids {
		out.docs = append(out.docs, Doc{ID: id, Fields: world[id]})
	}
	return out, nil
}

func (n *nState) get(user, index, id string) (*Doc, error) {
	view, err := n.resolve(user, index)
	if err != nil {
		return nil, err
	}
	world := n.erasedWorld(index, view)
	fields, ok := world[id]
	if !ok {
		return nil, role.ErrDocNotFound
	}
	return &Doc{ID: id, Fields: fields}, nil
}

func (n *nState) agg(user, index, field string) ([]Bucket, error) {
	view, err := n.resolve(user, index)
	if err != nil {
		return nil, err
	}
	if !view.fields[field] {
		return []Bucket{}, nil
	}
	world := n.erasedWorld(index, view)
	intCounts, strCounts := map[int64]int{}, map[string]int{}
	for _, fields := range world {
		switch v := fields[field].(type) {
		case int64:
			intCounts[v]++
		case string:
			strCounts[v]++
		}
	}
	var out []Bucket
	var intKeys []int64
	for key := range intCounts {
		intKeys = append(intKeys, key)
	}
	sort.Slice(intKeys, func(i, j int) bool { return intKeys[i] < intKeys[j] })
	for _, key := range intKeys {
		out = append(out, Bucket{Value: key, Count: intCounts[key]})
	}
	var strKeys []string
	for key := range strCounts {
		strKeys = append(strKeys, key)
	}
	sort.Strings(strKeys)
	for _, key := range strKeys {
		out = append(out, Bucket{Value: key, Count: strCounts[key]})
	}
	return out, nil
}

func randDoc(rng *rand.Rand) map[string]role.Value {
	fields := map[string]role.Value{}
	for _, field := range nFields {
		if rng.Intn(100) < 25 {
			continue
		}
		switch field {
		case "level":
			fields[field] = int64(rng.Intn(11))
		case "team", "secret":
			fields[field] = []string{"a", "b", "c"}[rng.Intn(3)]
		default:
			if rng.Intn(2) == 0 {
				fields[field] = int64(rng.Intn(11))
			} else {
				fields[field] = []string{"a", "b", "k1", "k2"}[rng.Intn(4)]
			}
		}
	}
	return fields
}

func randExpr(rng *rand.Rand, depth int) role.Expr {
	if depth >= 4 || rng.Intn(100) < 45 {
		field := nFields[rng.Intn(len(nFields))]
		if rng.Intn(2) == 0 {
			if rng.Intn(2) == 0 {
				return role.Term(field, int64(rng.Intn(11)))
			}
			return role.Term(field, []string{"a", "b", "c", "k1", "k2", "x"}[rng.Intn(6)])
		}
		lo := int64(rng.Intn(10))
		return role.Range(field, lo, lo+int64(rng.Intn(6)))
	}
	switch rng.Intn(3) {
	case 0:
		return role.Not(randExpr(rng, depth+1))
	case 1:
		return role.And(randExpr(rng, depth+1), randExpr(rng, depth+1))
	default:
		return role.Or(randExpr(rng, depth+1), randExpr(rng, depth+1))
	}
}

func randEntries(rng *rand.Rand) []role.Entry {
	count := 1 + rng.Intn(3)
	entries := make([]role.Entry, 0, count)
	for i := 0; i < count; i++ {
		entry := role.Entry{IndexPattern: nPat[rng.Intn(len(nPat))]}
		if rng.Intn(100) < 40 {
			filter := randExpr(rng, 1)
			entry.Filter = &filter
		}
		if rng.Intn(100) < 30 {
			entry.Fields = role.FieldAuth{Unrestricted: true}
		} else {
			auth := role.FieldAuth{Grant: []string{}}
			for j, n := 0, 1+rng.Intn(3); j < n; j++ {
				auth.Grant = append(auth.Grant, nGrant[rng.Intn(len(nGrant))])
			}
			if rng.Intn(2) == 0 {
				auth.Except = append(auth.Except, nExcept[rng.Intn(len(nExcept))])
			}
			entry.Fields = auth
		}
		entries = append(entries, entry)
	}
	return entries
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, role.ErrInvalid):
		return "invalid"
	case errors.Is(err, role.ErrNoUser):
		return "no_user"
	case errors.Is(err, role.ErrNoRole):
		return "no_role"
	case errors.Is(err, role.ErrRoleInUse):
		return "role_in_use"
	case errors.Is(err, role.ErrNoPerm):
		return "no_perm"
	case errors.Is(err, role.ErrDocNotFound):
		return "doc_not_found"
	default:
		return "unknown:" + err.Error()
	}
}

func valuesString(vs []Bucket) string {
	out := ""
	for i, b := range vs {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%v:%d", b.Value, b.Count)
	}
	return out
}

func docsString(docs []Doc) string {
	out := ""
	for i, d := range docs {
		if i > 0 {
			out += ";"
		}
		keys := make([]string, 0, len(d.Fields))
		for k := range d.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out += d.ID + "{"
		for j, k := range keys {
			if j > 0 {
				out += ","
			}
			out += fmt.Sprintf("%s=%v", k, d.Fields[k])
		}
		out += "}"
	}
	return out
}

func viewReason(n *nState, user, index string) string {
	view, err := n.resolve(user, index)
	if err != nil {
		return errClass(err)
	}
	return view.reason
}

func TestRandomNaiveEquivalence(t *testing.T) {
	logPath := filepath.Join(os.TempDir(), "dls_equiv.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	logf := func(format string, args ...any) { fmt.Fprintf(logFile, format+"\n", args...) }

	for run := 0; run < naiveRuns; run++ {
		seed := int64(1000 + run)
		rng := rand.New(rand.NewSource(seed))
		logf("==== run %d seed %d ====", run, seed)

		store := role.NewStore()
		engine := NewEngine(store)
		n := newNState()

		docIDs := []string{"d1", "d2", "d3", "d4"}
		for _, index := range []string{"ix-a", "ix-b"} {
			n.docs[index] = map[string]*nDoc{}
			for _, id := range docIDs {
				fields := randDoc(rng)
				if err := engine.PutDoc(index, id, fields); err != nil {
					t.Fatalf("seed %d PutDoc: %v", seed, err)
				}
				n.docs[index][id] = &nDoc{fields: fields}
			}
		}

		// 预置若干角色并绑定两个用户，保证 Search/Get/Agg 的成功路径高频被对照；
		// 后续 PutRole/BindUser 仍会整体替换，制造多样化的合并形态。
		seedRoles := map[string][]role.Entry{
			"R1": {{IndexPattern: "ix-*", Filter: ptr(role.Term("team", "a")),
				Fields: role.FieldAuth{Grant: []string{"team", "level"}}}},
			"R2": {{IndexPattern: "ix-a", Filter: ptr(role.Range("level", 5, 9)),
				Fields: role.FieldAuth{Grant: []string{"*"}, Except: []string{"secret"}}}},
			"R3": {{IndexPattern: "*", Fields: role.FieldAuth{Grant: []string{"note"}}}},
		}
		for name, entries := range seedRoles {
			if err := store.PutRole(name, entries); err != nil {
				t.Fatalf("seed %d seed PutRole: %v", seed, err)
			}
			n.roles[name] = entries
		}
		if err := store.BindUser("u1", []string{"R1"}); err != nil {
			t.Fatalf("seed %d seed BindUser: %v", seed, err)
		}
		n.binding["u1"] = []string{"R1"}
		if err := store.BindUser("u2", []string{"R1", "R2", "R3"}); err != nil {
			t.Fatalf("seed %d seed BindUser: %v", seed, err)
		}
		n.binding["u2"] = []string{"R1", "R2", "R3"}
		logf("seed roles/bindings installed: u1={R1} u2={R1,R2,R3}")

		for op := 0; op < 8+rng.Intn(10); op++ {
			switch rng.Intn(10) {
			case 0:
				index := []string{"ix-a", "ix-b"}[rng.Intn(2)]
				id := docIDs[rng.Intn(len(docIDs))]
				fields := randDoc(rng)
				logf("op %d PutDoc %s/%s fields=%v", op, index, id, fields)
				if err := engine.PutDoc(index, id, fields); err != nil {
					t.Fatalf("seed %d PutDoc: %v", seed, err)
				}
				n.docs[index][id] = &nDoc{fields: fields}
			case 1:
				index := []string{"ix-a", "ix-b"}[rng.Intn(2)]
				id := append(append([]string{}, docIDs...), "d9")[rng.Intn(len(docIDs)+1)]
				logf("op %d DeleteDoc %s/%s", op, index, id)
				if err := engine.DeleteDoc(index, id); err != nil {
					t.Fatalf("seed %d DeleteDoc: %v", seed, err)
				}
				delete(n.docs[index], id)
			case 2:
				name := "R" + string(rune('1'+rng.Intn(4)))
				entries := randEntries(rng)
				logf("op %d PutRole %s entries=%d", op, name, len(entries))
				if err := store.PutRole(name, entries); err != nil {
					t.Fatalf("seed %d PutRole invalid: %v entries=%+v", seed, err, entries)
				}
				n.roles[name] = entries
			case 3:
				name := "R" + string(rune('1'+rng.Intn(5)))
				engineErr := store.DeleteRole(name)
				inUse := false
				for _, roles := range n.binding {
					for _, r := range roles {
						if r == name {
							inUse = true
						}
					}
				}
				_, exists := n.roles[name]
				want := "nil"
				switch {
				case inUse:
					want = "role_in_use"
				case !exists:
					want = "no_role"
				default:
					delete(n.roles, name)
				}
				logf("op %d DeleteRole %s -> %s (want %s)", op, name, errClass(engineErr), want)
				if errClass(engineErr) != want {
					t.Fatalf("seed %d DeleteRole %s: got %s want %s", seed, name, errClass(engineErr), want)
				}
			case 4:
				user := nUsers[rng.Intn(len(nUsers))]
				allRoles := []string{"R1", "R2", "R3", "R4"}
				k := 1 + rng.Intn(3)
				rng.Shuffle(len(allRoles), func(i, j int) { allRoles[i], allRoles[j] = allRoles[j], allRoles[i] })
				roles := allRoles[:k]
				engineErr := store.BindUser(user, roles)
				allExist := true
				for _, r := range roles {
					if _, ok := n.roles[r]; !ok {
						allExist = false
					}
				}
				want := "nil"
				if !allExist {
					want = "no_role"
				} else {
					n.binding[user] = append([]string(nil), roles...)
				}
				logf("op %d BindUser %s roles=%v -> %s (want %s)", op, user, roles, errClass(engineErr), want)
				if errClass(engineErr) != want {
					t.Fatalf("seed %d BindUser: got %s want %s", seed, errClass(engineErr), want)
				}
			case 5:
				user, index := nUsers[rng.Intn(len(nUsers))], nIndexes[rng.Intn(len(nIndexes))]
				q := randExpr(rng, 1)
				size := 1 + rng.Intn(5)
				got, gErr := engine.Search(user, index, q, size)
				naive, nErr := n.search(user, index, q, size)
				logf("op %d Search user=%s index=%s size=%d -> eng=%s naive=%s reason=%s",
					op, user, index, size, errClass(gErr), errClass(nErr), viewReason(n, user, index))
				if errClass(gErr) != errClass(nErr) {
					t.Fatalf("seed %d Search err: eng=%s naive=%s", seed, errClass(gErr), errClass(nErr))
				}
				if gErr == nil {
					if got.Total != naive.total || docsString(got.Docs) != docsString(naive.docs) {
						t.Fatalf("seed %d Search mismatch: eng(total=%d,%s) naive(total=%d,%s) reason=%s",
							seed, got.Total, docsString(got.Docs), naive.total, docsString(naive.docs),
							viewReason(n, user, index))
					}
					logf("    MATCH Total=%d docs=%s", got.Total, docsString(got.Docs))
				}
			case 6:
				user, index := nUsers[rng.Intn(len(nUsers))], nIndexes[rng.Intn(len(nIndexes))]
				id := append(append([]string{}, docIDs...), "d9")[rng.Intn(len(docIDs)+1)]
				got, gErr := engine.Get(user, index, id)
				naive, nErr := n.get(user, index, id)
				logf("op %d Get %s/%s/%s -> eng=%s naive=%s", op, user, index, id, errClass(gErr), errClass(nErr))
				if errClass(gErr) != errClass(nErr) {
					t.Fatalf("seed %d Get err: eng=%s naive=%s", seed, errClass(gErr), errClass(nErr))
				}
				if gErr == nil && docsString([]Doc{*got}) != docsString([]Doc{*naive}) {
					t.Fatalf("seed %d Get mismatch: eng=%v naive=%v", seed, got.Fields, naive.Fields)
				}
			case 7:
				user, index := nUsers[rng.Intn(len(nUsers))], nIndexes[rng.Intn(len(nIndexes))]
				fieldPool := append(append([]string{}, nFields...), "ghostfield")
				field := fieldPool[rng.Intn(len(fieldPool))]
				got, gErr := engine.Agg(user, index, field)
				naive, nErr := n.agg(user, index, field)
				logf("op %d Agg %s/%s/%s -> eng=%s naive=%s", op, user, index, field, errClass(gErr), errClass(nErr))
				if errClass(gErr) != errClass(nErr) {
					t.Fatalf("seed %d Agg err: eng=%s naive=%s", seed, errClass(gErr), errClass(nErr))
				}
				if gErr == nil {
					if valuesString(got) != valuesString(naive) {
						t.Fatalf("seed %d Agg mismatch: eng=%s naive=%s reason=%s",
							seed, valuesString(got), valuesString(naive), viewReason(n, user, index))
					}
					logf("    MATCH agg=%s", valuesString(got))
				}
			case 8, 9:
				// 非法参数注入：size 越界 / 字段名越界，invalid 必须先于用户与权限判定。
				user, index := "ghost", nIndexes[rng.Intn(len(nIndexes))]
				if rng.Intn(2) == 0 {
					badSize := []int{0, 1001}[rng.Intn(2)]
					_, gErr := engine.Search(user, index, role.Term("f", int64(1)), badSize)
					logf("op %d InvalidSearch user=%s badSize=%d -> %s", op, user, badSize, errClass(gErr))
					if errClass(gErr) != "invalid" {
						t.Fatalf("seed %d bad size: %s", seed, errClass(gErr))
					}
				} else {
					_, gErr := engine.Agg(user, index, "")
					logf("op %d InvalidAgg user=%s emptyField -> %s", op, user, errClass(gErr))
					if errClass(gErr) != "invalid" {
						t.Fatalf("seed %d bad field: %s", seed, errClass(gErr))
					}
				}
			}
		}
	}
	t.Logf("naive equivalence log written to %s", logPath)
}
