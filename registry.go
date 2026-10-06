package ontology

import (
	"io"
	"sync"
	"time"
)

const DefaultMaxDepth = 32

type Config struct {
	MaxTotal  int
	MaxPerDef map[string]int
	MaxDepth  int
	Logger    io.Writer
}

type Registry struct {
	mu        sync.Mutex
	defs      map[string]Definition
	instances map[uint64]*instanceNode
	active    map[string]*instanceNode
	byDef     map[string]map[uint64]*instanceNode
	nextID    uint64
	revision  uint64
	hits      uint64
	creates   uint64
	maxTotal  int
	maxPerDef map[string]int
	maxDepth  int
	logger    io.Writer
	logMu     sync.Mutex
}

type instanceNode struct {
	id         uint64
	def        string
	args       []Type
	result     Type
	deps       map[uint64]*instanceNode
	dependents map[uint64]*instanceNode
	stale      bool
	reason     *StaleReason
	createdAt  time.Time
}

type instantiationTxn struct {
	registry *Registry
	created  map[uint64]*instanceNode
	reserved map[string]*instanceNode
	running  map[string]bool
	depth    int
	parent   *instanceNode
}

func NewRegistry(config Config) *Registry {
	maxDepth := config.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxTotal := config.MaxTotal
	if maxTotal <= 0 {
		maxTotal = 1_000_000
	}
	perDef := make(map[string]int, len(config.MaxPerDef))
	for def, limit := range config.MaxPerDef {
		perDef[def] = limit
	}
	return &Registry{
		defs:      make(map[string]Definition),
		instances: make(map[uint64]*instanceNode),
		active:    make(map[string]*instanceNode),
		byDef:     make(map[string]map[uint64]*instanceNode),
		maxTotal:  maxTotal,
		maxPerDef: perDef,
		maxDepth:  maxDepth,
		logger:    config.Logger,
	}
}

func (r *Registry) RegisterDefinition(def Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateDefinition(def); err != nil {
		return err
	}
	if _, exists := r.defs[def.Name]; exists {
		return errorf(ErrDefinition, "definition %q is already registered", def.Name)
	}
	stored := cloneDefinition(def)
	r.defs[def.Name] = stored
	r.byDef[def.Name] = make(map[uint64]*instanceNode)
	return nil
}

func validateDefinition(def Definition) error {
	if def.Name == "" {
		return errorf(ErrDefinition, "definition name is empty")
	}
	if def.Body == nil {
		return errorf(ErrDefinition, "definition %q has no body", def.Name)
	}
	for i, param := range def.Params {
		if param.Name == "" {
			return errorf(ErrDefinition, "definition %q parameter %d has no name", def.Name, i)
		}
		for _, constraint := range param.Constraints {
			if constraint.Kind == ConstraintSameAs {
				if constraint.OtherParam < 0 || constraint.OtherParam >= len(def.Params) || constraint.OtherParam == i {
					return errorf(ErrDefinition, "definition %q parameter %d has invalid same-as constraint", def.Name, i)
				}
			}
			if constraint.Kind == ConstraintOneOf && len(constraint.Allowed) == 0 {
				return errorf(ErrDefinition, "definition %q parameter %d has empty one-of constraint", def.Name, i)
			}
		}
	}
	return nil
}

func cloneDefinition(def Definition) Definition {
	stored := def
	stored.Params = append([]Param(nil), def.Params...)
	for i := range stored.Params {
		if def.Params[i].Default != nil {
			defaultValue := *def.Params[i].Default
			stored.Params[i].Default = &defaultValue
		}
		stored.Params[i].Constraints = append([]Constraint(nil), def.Params[i].Constraints...)
		for j := range stored.Params[i].Constraints {
			stored.Params[i].Constraints[j].Allowed = copyArgs(def.Params[i].Constraints[j].Allowed)
		}
	}
	return stored
}

func (r *Registry) Instantiate(def string, requested ...Type) (Result, error) {
	r.mu.Lock()
	txn := &instantiationTxn{
		registry: r,
		created:  make(map[uint64]*instanceNode),
		reserved: make(map[string]*instanceNode),
		running:  make(map[string]bool),
		depth:    0,
	}
	node, hit, err := txn.instantiate(def, requested)
	if err != nil {
		txn.rollback()
		r.mu.Unlock()
		r.logInstantiate(def, requested, Result{}, err)
		return Result{}, err
	}
	if hit {
		r.hits++
	} else {
		r.creates++
	}
	result := Result{Instance: r.snapshotInstance(node), Hit: hit}
	r.mu.Unlock()
	r.logInstantiate(def, requested, result, nil)
	return result, nil
}

func (txn *instantiationTxn) instantiate(def string, requested []Type) (*instanceNode, bool, error) {
	definition, ok := txn.registry.defs[def]
	if !ok {
		return nil, false, errorf(ErrUndefined, "definition %q is not registered", def)
	}
	args, err := txn.prepareArgs(definition, requested)
	if err != nil {
		return nil, false, err
	}
	if txn.depth >= txn.registry.maxDepth {
		return nil, false, errorf(ErrDepth, "instantiation depth %d exceeds limit %d", txn.depth, txn.registry.maxDepth)
	}
	key := instanceKey(def, args)
	if existing := txn.registry.active[key]; existing != nil {
		return existing, true, nil
	}
	if txn.running[key] {
		return nil, false, errorf(ErrDepth, "infinite re-entrant instantiation of %s", key)
	}
	if err := txn.registry.checkQuota(def); err != nil {
		return nil, false, err
	}
	now := time.Now()
	node := &instanceNode{
		id:         txn.registry.allocateIDLocked(),
		def:        def,
		args:       args,
		deps:       make(map[uint64]*instanceNode),
		dependents: make(map[uint64]*instanceNode),
		createdAt:  now,
	}
	txn.registry.instances[node.id] = node
	txn.registry.active[key] = node
	txn.registry.byDef[def][node.id] = node
	txn.created[node.id] = node
	txn.reserved[key] = node
	txn.running[key] = true
	childTxn := &instantiationTxn{
		registry: txn.registry,
		created:  txn.created,
		reserved: txn.reserved,
		running:  txn.running,
		depth:    txn.depth + 1,
		parent:   node,
	}
	result, bodyErr := definition.Body(childTxn, args)
	if bodyErr != nil {
		return nil, false, bodyErr
	}
	canonicalResult, err := canonicalType(result)
	if err != nil {
		return nil, false, errorf(ErrDefinition, "definition %q returned an invalid result: %v", def, err)
	}
	node.result = canonicalResult
	txn.running[key] = false
	return node, false, nil
}

func (txn *instantiationTxn) Instantiate(def string, requested ...Type) (Type, error) {
	node, _, err := txn.instantiate(def, requested)
	if err != nil {
		return Type{}, err
	}
	if txn.parent != nil {
		txn.parent.deps[node.id] = node
		node.dependents[txn.parent.id] = txn.parent
	}
	return node.result, nil
}

func (txn *instantiationTxn) prepareArgs(def Definition, requested []Type) ([]Type, error) {
	if len(requested) > len(def.Params) {
		return nil, errorf(ErrParameters, "definition %q expects %d arguments but got %d", def.Name, len(def.Params), len(requested))
	}
	args := make([]Type, len(def.Params))
	copy(args, requested)
	for i := len(requested); i < len(def.Params); i++ {
		if def.Params[i].Default == nil {
			return nil, errorf(ErrParameters, "definition %q parameter %q has no default", def.Name, def.Params[i].Name)
		}
		args[i] = *def.Params[i].Default
	}
	canonicalArgs := make([]Type, len(args))
	for i, arg := range args {
		canonicalArg, err := canonicalType(arg)
		if err != nil {
			return nil, errorf(ErrParameters, "definition %q parameter %q: %v", def.Name, def.Params[i].Name, err)
		}
		canonicalArgs[i] = canonicalArg
	}
	for i, param := range def.Params {
		for _, constraint := range param.Constraints {
			switch constraint.Kind {
			case ConstraintOneOf:
				allowed, err := canonicalizeArgs(constraint.Allowed)
				if err != nil {
					return nil, errorf(ErrDefinition, "definition %q has an invalid constraint: %v", def.Name, err)
				}
				if !typeIn(canonicalArgs[i], allowed) {
					return nil, errorf(ErrConstraint, "definition %q parameter %q violates one-of constraint", def.Name, param.Name)
				}
			case ConstraintSameAs:
				if !sameType(canonicalArgs[i], canonicalArgs[constraint.OtherParam]) {
					return nil, errorf(ErrConstraint, "definition %q parameter %q must equal parameter %q", def.Name, param.Name, def.Params[constraint.OtherParam].Name)
				}
			}
		}
	}
	return canonicalArgs, nil
}

func typeIn(value Type, values []Type) bool {
	for _, candidate := range values {
		if sameType(value, candidate) {
			return true
		}
	}
	return false
}

func (r *Registry) allocateIDLocked() uint64 {
	r.nextID++
	return r.nextID
}

func (r *Registry) checkQuota(def string) error {
	if len(r.instances)+1 > r.maxTotal {
		return errorf(ErrQuota, "total instance quota %d reached", r.maxTotal)
	}
	limit := r.maxPerDef[def]
	if limit > 0 && len(r.byDef[def])+1 > limit {
		return errorf(ErrQuota, "definition %q instance quota %d reached", def, limit)
	}
	return nil
}

func (r *Registry) rollback(txn *instantiationTxn) {}

func (txn *instantiationTxn) rollback() {
	for _, node := range txn.created {
		delete(txn.running, instanceKey(node.def, node.args))
		for _, dependency := range node.deps {
			if _, createdHere := txn.created[dependency.id]; !createdHere {
				delete(dependency.dependents, node.id)
			}
		}
		key := instanceKey(node.def, node.args)
		if txn.registry.active[key] == node {
			delete(txn.registry.active, key)
		}
		delete(txn.registry.byDef[node.def], node.id)
		delete(txn.registry.instances, node.id)
	}
}
