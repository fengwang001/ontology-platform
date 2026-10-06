package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type naiveInstance struct {
	id         uint64
	def        string
	args       []Type
	result     Type
	deps       map[uint64]bool
	dependents map[uint64]bool
	stale      bool
}

type naiveModel struct {
	defs     map[string]Definition
	all      map[uint64]*naiveInstance
	active   []*naiveInstance
	nextID   uint64
	hits     uint64
	creates  uint64
	maxDepth int
}

type naiveTxn struct {
	model   *naiveModel
	created map[uint64]*naiveInstance
	depth   int
	parent  *naiveInstance
}

func newNaiveModel(maxDepth int) *naiveModel {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	return &naiveModel{
		defs:     make(map[string]Definition),
		all:      make(map[uint64]*naiveInstance),
		maxDepth: maxDepth,
	}
}

func (m *naiveModel) register(def Definition) {
	m.defs[def.Name] = def
}

func (m *naiveModel) instantiate(def string, requested []Type) (uint64, bool, error) {
	created := make(map[uint64]*naiveInstance)
	txn := &naiveTxn{model: m, created: created, depth: 0}
	node, hit, err := txn.instantiate(def, requested)
	if err != nil {
		for id := range created {
			delete(m.all, id)
			m.active = removeActive(m.active, id)
		}
		return 0, false, err
	}
	if hit {
		m.hits++
	} else {
		m.creates++
	}
	return node.id, hit, nil
}

func (txn *naiveTxn) Instantiate(def string, requested ...Type) (Type, error) {
	node, _, err := txn.instantiate(def, requested)
	if err != nil {
		return Type{}, err
	}
	if txn.parent != nil {
		txn.parent.deps[node.id] = true
		node.dependents[txn.parent.id] = true
	}
	return node.result, nil
}

func (txn *naiveTxn) instantiate(def string, requested []Type) (*naiveInstance, bool, error) {
	definition, ok := txn.model.defs[def]
	if !ok {
		return nil, false, errorf(ErrUndefined, "missing %s", def)
	}
	if len(requested) != len(definition.Params) {
		return nil, false, errorf(ErrParameters, "arity")
	}
	args := make([]Type, len(requested))
	for i, arg := range requested {
		canonicalArg, err := canonicalType(arg)
		if err != nil {
			return nil, false, err
		}
		args[i] = canonicalArg
	}
	if txn.depth >= txn.model.maxDepth {
		return nil, false, errorf(ErrDepth, "limit")
	}
	for _, node := range txn.model.active {
		if node.def == def && equalArgs(node.args, args) {
			return node, true, nil
		}
	}
	node := &naiveInstance{
		id:         txn.model.nextID + 1,
		def:        def,
		args:       args,
		deps:       make(map[uint64]bool),
		dependents: make(map[uint64]bool),
	}
	for id, existing := range txn.model.all {
		if id >= node.id && existing != nil {
			return nil, false, errorf(ErrQuota, "temporary id collision")
		}
	}
	txn.model.nextID++
	txn.model.all[node.id] = node
	txn.model.active = append(txn.model.active, node)
	txn.created[node.id] = node
	child := &naiveTxn{model: txn.model, created: txn.created, depth: txn.depth + 1, parent: node}
	result, err := definition.Body(child, args)
	if err != nil {
		return nil, false, err
	}
	canonicalResult, err := canonicalType(result)
	if err != nil {
		return nil, false, err
	}
	node.result = canonicalResult
	return node, false, nil
}

func equalArgs(left, right []Type) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameType(left[i], right[i]) {
			return false
		}
	}
	return true
}

func removeActive(nodes []*naiveInstance, id uint64) []*naiveInstance {
	for i, node := range nodes {
		if node.id == id {
			return append(nodes[:i], nodes[i+1:]...)
		}
	}
	return nodes
}

func (m *naiveModel) keySet() map[string]bool {
	result := make(map[string]bool)
	for _, node := range m.active {
		result[instanceKey(node.def, node.args)] = true
	}
	return result
}

func registryKeySet(r *Registry) map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]bool, len(r.active))
	for key := range r.active {
		result[key] = true
	}
	return result
}

func TestRandomRequestsMatchNaiveModel(t *testing.T) {
	const operations = 400
	registry := NewRegistry(Config{MaxDepth: 4})
	model := newNaiveModel(4)
	leaf := Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody}
	box := Definition{Name: "Box", Params: []Param{{Name: "t"}}, Body: boxBody}
	stack := Definition{
		Name:   "Stack",
		Params: []Param{{Name: "t"}},
		Body: func(nested Nested, args []Type) (Type, error) {
			first, err := nested.Instantiate("Box", args...)
			if err != nil {
				return Type{}, err
			}
			second, err := nested.Instantiate("Leaf", args...)
			if err != nil {
				return Type{}, err
			}
			return Struct(Field{Name: "a", Type: first}, Field{Name: "b", Type: second}), nil
		},
	}
	for _, def := range []Definition{leaf, box, stack} {
		if err := registry.RegisterDefinition(def); err != nil {
			t.Fatal(err)
		}
		model.register(def)
	}
	random := rand.New(rand.NewSource(1531))
	var log strings.Builder
	defNames := []string{"Leaf", "Box", "Stack", "Missing"}
	for i := 0; i < operations; i++ {
		def := defNames[random.Intn(len(defNames))]
		arg := randomType(random)
		requested := []Type{arg}
		if random.Intn(8) == 0 {
			requested = append(requested, Named("extra"))
		}
		modelID, modelHit, modelErr := model.instantiate(def, requested)
		result, registryErr := registry.Instantiate(def, requested...)
		decision := fmt.Sprintf("model_hit=%t model_id=%d", modelHit, modelID)
		if modelErr != nil {
			decision += " model_error=" + string(codeOf(modelErr))
		}
		if registryErr != nil {
			decision += " registry_error=" + string(codeOf(registryErr))
		} else {
			decision += fmt.Sprintf(" registry_hit=%t registry_id=%d", result.Hit, result.Instance.ID)
		}
		fmt.Fprintf(&log, "op=%d input=Instantiate(%s,%v) output=%s basis=canonical-linear-scan-vs-hash-index\n", i, def, requested, decision)
		if (modelErr == nil) != (registryErr == nil) {
			t.Fatalf("op %d error mismatch: model=%v registry=%v\nlog:\n%s", i, modelErr, registryErr, log.String())
		}
		if modelErr == nil {
			if modelHit != result.Hit {
				t.Fatalf("op %d hit mismatch: model=%t registry=%t\nlog:\n%s", i, modelHit, result.Hit, log.String())
			}
			registryKey := instanceKey(def, normalizeForTest(t, requested))
			if !registryKeySetContains(registry, registryKey) {
				t.Fatalf("op %d registry missing %s\nlog:\n%s", i, registryKey, log.String())
			}
			if !model.keySet()[registryKey] {
				t.Fatalf("op %d model missing %s\nlog:\n%s", i, registryKey, log.String())
			}
		}
	}
	modelKeys := model.keySet()
	registryKeys := registryKeySet(registry)
	if len(modelKeys) != len(registryKeys) {
		t.Fatalf("active set size mismatch: naive=%d indexed=%d\nlog:\n%s", len(modelKeys), len(registryKeys), log.String())
	}
	for key := range modelKeys {
		if !registryKeys[key] {
			t.Fatalf("registry lacks %s\nlog:\n%s", key, log.String())
		}
	}
	summary := registry.Summary()
	if summary.CumulativeHits != model.hits || summary.CumulativeCreates != model.creates {
		t.Fatalf("hit/create mismatch: registry=%+v naive hits=%d creates=%d", summary, model.hits, model.creates)
	}
	if !strings.Contains(log.String(), "basis=canonical-linear-scan-vs-hash-index") {
		t.Fatal("random test must log decision basis")
	}
	t.Logf("random model log:\n%s", log.String())
}

func randomType(random *rand.Rand) Type {
	baseNames := []string{"int", "string", "bool"}
	base := Named(baseNames[random.Intn(len(baseNames))])
	switch random.Intn(6) {
	case 0:
		return Alias("alias-"+base.Name, base)
	case 1:
		return Struct(Field{Name: "x", Type: Alias("a", base)})
	case 2:
		return Struct(Field{Name: "x", Type: base}, Field{Name: "y", Type: Named(baseNames[random.Intn(len(baseNames))])})
	case 3:
		return InstanceOf("Leaf", Alias("nested-alias", base))
	default:
		return base
	}
}

func normalizeForTest(t *testing.T, args []Type) []Type {
	t.Helper()
	result, err := canonicalArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func registryKeySetContains(r *Registry, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.active[key]
	return ok
}
