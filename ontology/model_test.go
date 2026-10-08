package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveModel 是独立实现的朴素继承权限模型，用于随机操作序列对照。
// 解析算法刻意与 Coordinator 不同：先收集整条祖先链，再从根向下合并，
// 让更具体的声明覆盖更上层的声明。
type naiveModel struct {
	types   map[string]*naiveType
	objects map[string]*naiveObject
}

type naiveType struct {
	parent   string
	rules    map[string]Rule
	required []string
}

type naiveObject struct {
	typ     string
	version int64
	props   map[string]any
}

func newNaiveModel() *naiveModel {
	return &naiveModel{types: map[string]*naiveType{}, objects: map[string]*naiveObject{}}
}

func (m *naiveModel) chain(typeID string) []string {
	var out []string
	for id := typeID; id != ""; {
		t := m.types[id]
		if t == nil {
			break
		}
		out = append(out, id)
		id = t.parent
	}
	return out
}

// resolve 从根向下合并，最具体的显式声明最终胜出。
func (m *naiveModel) resolve(typeID, prop string) Rule {
	chain := m.chain(typeID)
	merged := map[string]Rule{}
	for i := len(chain) - 1; i >= 0; i-- {
		for p, r := range m.types[chain[i]].rules {
			merged[p] = r
		}
	}
	if r, ok := merged[prop]; ok {
		return r
	}
	return Rule{}
}

func (m *naiveModel) createType(id, parent string, rules map[string]Rule, required []string) ErrorKind {
	if parent != "" {
		if _, ok := m.types[parent]; !ok {
			return ErrTypeNotFound
		}
	}
	m.types[id] = &naiveType{parent: parent, rules: rules, required: required}
	return -1
}

func (m *naiveModel) setRule(typeID, prop string, r Rule) ErrorKind {
	t, ok := m.types[typeID]
	if !ok {
		return ErrTypeNotFound
	}
	t.rules[prop] = r
	return -1
}

func (m *naiveModel) clearRule(typeID, prop string) ErrorKind {
	t, ok := m.types[typeID]
	if !ok {
		return ErrTypeNotFound
	}
	delete(t.rules, prop)
	return -1
}

func (m *naiveModel) setRequired(typeID string, required []string) ErrorKind {
	t, ok := m.types[typeID]
	if !ok {
		return ErrTypeNotFound
	}
	t.required = required
	return -1
}

func (m *naiveModel) reparent(typeID, newParent string) ErrorKind {
	t, ok := m.types[typeID]
	if !ok {
		return ErrTypeNotFound
	}
	if newParent != "" {
		if _, ok := m.types[newParent]; !ok {
			return ErrTypeNotFound
		}
	}
	for id := newParent; id != ""; {
		if id == typeID {
			return ErrCycle
		}
		id = m.types[id].parent
	}
	t.parent = newParent
	return -1
}

func naiveMissing(required []string, props map[string]any) bool {
	for _, r := range required {
		if _, ok := props[r]; !ok {
			return true
		}
	}
	return false
}

func (m *naiveModel) createObject(id, typeID string, props map[string]any) ErrorKind {
	t, ok := m.types[typeID]
	if !ok {
		return ErrTypeNotFound
	}
	if naiveMissing(t.required, props) {
		return ErrMissingRequired
	}
	m.objects[id] = &naiveObject{typ: typeID, version: 1, props: props}
	return -1
}

func (m *naiveModel) write(id string, expected int64, props map[string]any, role string) (int64, ErrorKind) {
	o, ok := m.objects[id]
	if !ok {
		return 0, ErrObjectNotFound
	}
	if o.version != expected {
		return o.version, ErrVersionConflict
	}
	for p := range props {
		if !m.resolve(o.typ, p).Allows(role) {
			return o.version, ErrPermissionDenied
		}
	}
	merged := map[string]any{}
	for k, v := range o.props {
		merged[k] = v
	}
	for k, v := range props {
		merged[k] = v
	}
	if naiveMissing(m.types[o.typ].required, merged) {
		return o.version, ErrMissingRequired
	}
	o.props = merged
	o.version++
	return o.version, -1
}

func kindOf(err error) ErrorKind {
	if err == nil {
		return -1
	}
	return err.(*Error).Kind
}

// 随机操作序列对照：Coordinator 与朴素模型逐步执行同一序列，
// 每次操作后比较错误类别、返回值、全部实例状态与全部 (类型, 属性) 解析结果。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1676))
	c := NewCoordinator()
	m := newNaiveModel()

	props := []string{"p0", "p1", "p2"}
	roles := []string{"admin", "guest"}
	rules := []Rule{allowAll(), denyAll(), onlyAdmin()}

	randRule := func() Rule { return rules[rng.Intn(len(rules))] }
	randProps := func() map[string]any {
		out := map[string]any{}
		for _, p := range props {
			if rng.Intn(2) == 0 {
				out[p] = rng.Intn(100)
			}
		}
		return out
	}
	randRequired := func() []string {
		out := []string{}
		for _, p := range props {
			if rng.Intn(4) == 0 {
				out = append(out, p)
			}
		}
		return out
	}
	typeIDs := func() []string {
		out := []string{}
		for id := range m.types {
			out = append(out, id)
		}
		return out
	}
	objectIDs := func() []string {
		out := []string{}
		for id := range m.objects {
			out = append(out, id)
		}
		return out
	}

	const steps = 4000
	for step := 0; step < steps; step++ {
		op := rng.Intn(8)
		switch {
		case op == 0 || len(m.types) == 0:
			id := fmt.Sprintf("t%d", step)
			parent := ""
			if ids := typeIDs(); len(ids) > 0 && rng.Intn(4) > 0 {
				parent = ids[rng.Intn(len(ids))]
			}
			rules := map[string]Rule{}
			for _, p := range props {
				if rng.Intn(2) == 0 {
					rules[p] = randRule()
				}
			}
			required := randRequired()
			got := kindOf(c.CreateType(id, parent, rules, required))
			want := m.createType(id, parent, rules, required)
			if got != want {
				t.Fatalf("step %d CreateType: got %s want %s", step, got, want)
			}
		case op == 1:
			ids := typeIDs()
			id, p := ids[rng.Intn(len(ids))], props[rng.Intn(len(props))]
			r := randRule()
			if got, want := kindOf(c.SetRule(id, p, r)), m.setRule(id, p, r); got != want {
				t.Fatalf("step %d SetRule: got %s want %s", step, got, want)
			}
		case op == 2:
			ids := typeIDs()
			id, p := ids[rng.Intn(len(ids))], props[rng.Intn(len(props))]
			if got, want := kindOf(c.ClearRule(id, p)), m.clearRule(id, p); got != want {
				t.Fatalf("step %d ClearRule: got %s want %s", step, got, want)
			}
		case op == 3:
			ids := typeIDs()
			id := ids[rng.Intn(len(ids))]
			newParent := ""
			if rng.Intn(3) > 0 {
				newParent = ids[rng.Intn(len(ids))]
			}
			if got, want := kindOf(c.Reparent(id, newParent)), m.reparent(id, newParent); got != want {
				t.Fatalf("step %d Reparent(%s,%s): got %s want %s", step, id, newParent, got, want)
			}
		case op == 4:
			ids := typeIDs()
			id := ids[rng.Intn(len(ids))]
			required := randRequired()
			if got, want := kindOf(c.SetRequired(id, required)), m.setRequired(id, required); got != want {
				t.Fatalf("step %d SetRequired: got %s want %s", step, got, want)
			}
		case op == 5:
			ids := typeIDs()
			id, typ := fmt.Sprintf("o%d", step), ids[rng.Intn(len(ids))]
			p := randProps()
			got := kindOf(c.CreateObject(id, typ, p))
			want := m.createObject(id, typ, p)
			if got != want {
				t.Fatalf("step %d CreateObject: got %s want %s", step, got, want)
			}
		default:
			objs := objectIDs()
			if len(objs) == 0 {
				continue
			}
			id := objs[rng.Intn(len(objs))]
			expected := m.objects[id].version
			if rng.Intn(3) == 0 {
				expected += int64(rng.Intn(3)) - 1 // 制造过期/超前版本号
			}
			p := randProps()
			role := roles[rng.Intn(len(roles))]
			gotV, gotErr := c.Write(id, expected, p, role)
			wantV, wantKind := m.write(id, expected, p, role)
			if kindOf(gotErr) != wantKind {
				t.Fatalf("step %d Write(%s,%d): got kind %s want %s", step, id, expected, kindOf(gotErr), wantKind)
			}
			if gotV != wantV {
				t.Fatalf("step %d Write(%s,%d): got version %d want %d", step, id, expected, gotV, wantV)
			}
		}

		// 每 200 步做一次全量状态与规则解析对照。
		if step%200 == 199 {
			for id, mo := range m.objects {
				co, ok := c.Get(id)
				if !ok {
					t.Fatalf("step %d: object %s missing in coordinator", step, id)
				}
				if co.Version != mo.version || co.Type != mo.typ || !reflect.DeepEqual(co.Props, mo.props) {
					t.Fatalf("step %d: object %s diverged: %+v vs %+v", step, id, co, mo)
				}
			}
			for _, tid := range typeIDs() {
				for _, p := range props {
					c.mu.Lock()
					cr, _, _ := c.resolveRule(tid, p)
					c.mu.Unlock()
					mr := m.resolve(tid, p)
					if !reflect.DeepEqual(cr, mr) {
						t.Fatalf("step %d: rule(%s,%s) diverged: %+v vs %+v", step, tid, p, cr, mr)
					}
				}
			}
		}
	}
	t.Logf("randomized comparison done: %d types, %d objects, %d decisions",
		len(m.types), len(m.objects), len(c.Log().Entries()))
	dumpLogTail(t, c, 20)
}

func dumpLogTail(t *testing.T, c *Coordinator, n int) {
	t.Helper()
	entries := c.Log().Entries()
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	for i, d := range entries {
		t.Logf("decision[%d] op=%s input=%s output=%s basis=%s", i, d.Op, d.Input, d.Output, d.Basis)
	}
}
