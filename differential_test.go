package cascade

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type refState struct {
	id         string
	owners     map[string]bool
	finalizers map[string]struct{}
	deleting   bool
	policy     Policy
	deleteAt   int64
}

type naiveModel struct {
	objects map[string]*refState
}

type refOp struct {
	name      string
	id        string
	policy    Policy
	at        int64
	finalizer string
	owners    []OwnerRef
	input     CreateObjectInput
}

func newNaiveModel() *naiveModel {
	return &naiveModel{objects: make(map[string]*refState)}
}

func (m *naiveModel) apply(op refOp) ErrorKind {
	switch op.name {
	case "create":
		return m.create(op)
	case "delete":
		return m.markDelete(op.id, op.policy, op.at)
	case "addFinalizer":
		return m.addFinalizer(op.id, op.finalizer)
	case "removeFinalizer":
		return m.removeFinalizer(op.id, op.finalizer, op.at)
	case "replace":
		return m.replace(op)
	default:
		return InvalidArgument
	}
}

func (m *naiveModel) create(op refOp) ErrorKind {
	if op.input.ID == "" {
		return InvalidArgument
	}
	ownerSet := make(map[string]bool)
	for _, owner := range op.input.Owners {
		if owner.OwnerID == "" || ownerSet[owner.OwnerID] {
			return InvalidArgument
		}
		ownerSet[owner.OwnerID] = owner.BlockDeletion
	}
	finalizers := make(map[string]struct{})
	for _, finalizer := range op.input.Finalizers {
		if finalizer == "" {
			return InvalidArgument
		}
		if _, exists := finalizers[finalizer]; exists {
			return InvalidArgument
		}
		finalizers[finalizer] = struct{}{}
	}
	if m.objects[op.input.ID] != nil {
		return InvalidArgument
	}
	if kind := m.validateTargets(op.input.ID, ownerSet); kind != 0 {
		return kind
	}
	m.objects[op.input.ID] = &refState{
		id:         op.input.ID,
		owners:     ownerSet,
		finalizers: finalizers,
	}
	return 0
}

func (m *naiveModel) markDelete(id string, policy Policy, at int64) ErrorKind {
	if id == "" {
		return InvalidArgument
	}
	if policy != Background && policy != Foreground && policy != Orphan {
		return InvalidArgument
	}
	node := m.objects[id]
	if node == nil {
		return ObjectNotFound
	}
	if !node.deleting {
		node.deleting = true
		node.policy = policy
		node.deleteAt = at
	} else if node.policy == Background && policy == Foreground {
		node.policy = Foreground
	}
	m.converge(at)
	return 0
}

func (m *naiveModel) addFinalizer(id, finalizer string) ErrorKind {
	if id == "" || finalizer == "" {
		return InvalidArgument
	}
	node := m.objects[id]
	if node == nil {
		return ObjectNotFound
	}
	if node.deleting {
		return Conflict
	}
	node.finalizers[finalizer] = struct{}{}
	return 0
}

func (m *naiveModel) removeFinalizer(id, finalizer string, at int64) ErrorKind {
	if id == "" || finalizer == "" {
		return InvalidArgument
	}
	node := m.objects[id]
	if node == nil {
		return ObjectNotFound
	}
	if _, exists := node.finalizers[finalizer]; !exists {
		return InvalidArgument
	}
	delete(node.finalizers, finalizer)
	m.converge(at)
	return 0
}

func (m *naiveModel) replace(op refOp) ErrorKind {
	if op.id == "" {
		return InvalidArgument
	}
	node := m.objects[op.id]
	if node == nil {
		return ObjectNotFound
	}
	if node.deleting {
		return Conflict
	}
	owners := make(map[string]bool)
	for _, owner := range op.owners {
		if owner.OwnerID == "" || owners[owner.OwnerID] {
			return InvalidArgument
		}
		owners[owner.OwnerID] = owner.BlockDeletion
	}
	if kind := m.validateTargets(op.id, owners); kind != 0 {
		return kind
	}
	node.owners = owners
	m.converge(op.at)
	return 0
}

func (m *naiveModel) validateTargets(selfID string, owners map[string]bool) ErrorKind {
	if owners[selfID] {
		return CycleDetected
	}
	if m.hasCycle(selfID, owners) {
		return CycleDetected
	}
	for ownerID := range owners {
		owner := m.objects[ownerID]
		if owner == nil || owner.deleting {
			return OwnerMissing
		}
	}
	return 0
}

func (m *naiveModel) hasCycle(selfID string, owners map[string]bool) bool {
	visited := make(map[string]bool)
	stack := make([]string, 0, len(owners))
	for ownerID := range owners {
		stack = append(stack, ownerID)
	}
	for len(stack) > 0 {
		currentID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[currentID] {
			continue
		}
		visited[currentID] = true
		current := m.objects[currentID]
		if current == nil {
			continue
		}
		if currentID == selfID {
			return true
		}
		for ownerID := range current.owners {
			if ownerID == selfID {
				return true
			}
			if !visited[ownerID] {
				stack = append(stack, ownerID)
			}
		}
	}
	return false
}

func (m *naiveModel) converge(at int64) {
	for {
		m.propagateForeground(at)
		removable := m.removableIDs()
		if len(removable) == 0 {
			return
		}
		for _, id := range removable {
			node := m.objects[id]
			if node == nil {
				continue
			}
			dependents := m.dependentsOf(id)
			policy := node.policy
			for _, dependentID := range dependents {
				if dependent := m.objects[dependentID]; dependent != nil {
					delete(dependent.owners, id)
				}
			}
			delete(m.objects, id)
			for _, dependentID := range dependents {
				dependent := m.objects[dependentID]
				if dependent == nil || (dependent.deleting && dependent.policy == Foreground) {
					continue
				}
				if policy != Orphan && len(dependent.owners) == 0 {
					if !dependent.deleting {
						dependent.deleting = true
						dependent.policy = Background
						dependent.deleteAt = at
					}
				}
			}
		}
	}
}

func (m *naiveModel) propagateForeground(at int64) {
	for {
		changed := false
		for _, node := range m.sortedObjects() {
			if !node.deleting || node.policy != Foreground {
				continue
			}
			for _, dependentID := range m.dependentsOf(node.id) {
				dependent := m.objects[dependentID]
				if dependent == nil {
					continue
				}
				allOthers := true
				for ownerID := range dependent.owners {
					if ownerID == node.id {
						continue
					}
					owner := m.objects[ownerID]
					if owner == nil || owner.deleting {
						continue
					}
					if !owner.deleting {
						allOthers = false
					}
				}
				if !allOthers {
					continue
				}
				if !dependent.deleting {
					dependent.deleting = true
					dependent.deleteAt = at
					changed = true
				}
				if dependent.policy != Foreground {
					changed = true
				}
				dependent.policy = Foreground
			}
		}
		if !changed {
			return
		}
	}
}

func (m *naiveModel) removableIDs() []string {
	var result []string
	for _, node := range m.sortedObjects() {
		if !node.deleting || len(node.finalizers) != 0 {
			continue
		}
		if node.policy == Foreground && m.hasBlockingDependent(node.id) {
			continue
		}
		result = append(result, node.id)
	}
	return result
}

func (m *naiveModel) hasBlockingDependent(ownerID string) bool {
	for id, node := range m.objects {
		if blocking, exists := node.owners[ownerID]; exists && blocking && m.objects[id] != nil {
			return true
		}
	}
	return false
}

func (m *naiveModel) dependentsOf(ownerID string) []string {
	var result []string
	for id, node := range m.objects {
		if _, exists := node.owners[ownerID]; exists {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (m *naiveModel) sortedObjects() []*refState {
	result := make([]*refState, 0, len(m.objects))
	for _, node := range m.objects {
		result = append(result, node)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}

func objectID(iteration, step, n int) string {
	return fmt.Sprintf("i%ds%dn%d", iteration, step, n)
}

func randomOp(rng *rand.Rand, created []string, iteration, step int) refOp {
	id := objectID(iteration, step, len(created))
	if step < 6 || rng.Intn(6) == 0 {
		input := CreateObjectInput{ID: id}
		if len(created) > 0 {
			input.Owners = randomOwners(rng, created, "")
		}
		if rng.Intn(3) == 0 {
			input.Finalizers = []string{"f" + string(rune('a'+rng.Intn(3)))}
		}
		return refOp{name: "create", input: input}
	}

	target := created[rng.Intn(len(created))]
	at := int64(iteration*10000 + step + 1)
	switch rng.Intn(5) {
	case 0:
		return refOp{name: "delete", id: target, policy: Policy(1 + rng.Intn(3)), at: at}
	case 1:
		return refOp{name: "addFinalizer", id: target, finalizer: "f" + string(rune('a'+rng.Intn(4)))}
	case 2:
		return refOp{name: "removeFinalizer", id: target, finalizer: "f" + string(rune('a'+rng.Intn(4))), at: at}
	default:
		return refOp{name: "replace", id: target, owners: randomOwners(rng, created, target), at: at}
	}
}

func randomOwners(rng *rand.Rand, created []string, selfID string) []OwnerRef {
	indices := rng.Perm(len(created))
	count := rng.Intn(3)
	if count > len(indices) {
		count = len(indices)
	}
	owners := make([]OwnerRef, 0, count)
	for _, index := range indices[:count] {
		id := created[index]
		if id == selfID {
			continue
		}
		owners = append(owners, OwnerRef{OwnerID: id, BlockDeletion: rng.Intn(2) == 0})
	}
	sortOwnerRefs(owners)
	return owners
}

func TestRandomDifferentialAgainstNaiveModel(t *testing.T) {
	for iteration := 0; iteration < 120; iteration++ {
		rng := rand.New(rand.NewSource(int64(1611000 + iteration)))
		controller := NewController()
		model := newNaiveModel()
		created := make([]string, 0)

		for step := 0; step < 90; step++ {
			op := randomOp(rng, created, iteration, step)
			t.Logf("seed=%d step=%d input=%+v basis=execute identical operation on local controller and global fixed-point oracle", iteration, step, op)

			wantKind := model.apply(op)
			var gotKind ErrorKind
			var actual any
			switch op.name {
			case "create":
				gotKind = errorKind(controller.Create(op.input))
			case "delete":
				result, err := controller.Delete(op.id, op.policy, op.at)
				gotKind, actual = errorKind(err), result
			case "addFinalizer":
				gotKind = errorKind(controller.AddFinalizer(op.id, op.finalizer))
			case "removeFinalizer":
				result, err := controller.RemoveFinalizer(op.id, op.finalizer, op.at)
				gotKind, actual = errorKind(err), result
			case "replace":
				result, err := controller.ReplaceOwners(op.id, op.owners, op.at)
				gotKind, actual = errorKind(err), result
			}

			if op.name == "create" && gotKind == 0 {
				created = append(created, op.input.ID)
			}

			gotState := controller.Snapshot()
			wantState := model.snapshot()
			t.Logf("actual_output=error_kind:%d result:%+v state_count:%d basis=compare error kind and canonical full state against oracle", gotKind, actual, len(gotState))
			if gotKind != wantKind {
				t.Fatalf("seed=%d step=%d op=%+v got_error=%d want_error=%d", iteration, step, op, gotKind, wantKind)
			}
			if !reflect.DeepEqual(gotState, wantState) {
				t.Fatalf("seed=%d step=%d op=%+v got_state=%v want_state=%v", iteration, step, op, gotState, wantState)
			}
		}
	}
}

func (m *naiveModel) snapshot() map[string]ObjectState {
	result := make(map[string]ObjectState, len(m.objects))
	for id, node := range m.objects {
		owners := make([]OwnerRef, 0, len(node.owners))
		for ownerID, blocking := range node.owners {
			owners = append(owners, OwnerRef{OwnerID: ownerID, BlockDeletion: blocking})
		}
		sortOwnerRefs(owners)
		finalizers := make([]string, 0, len(node.finalizers))
		for finalizer := range node.finalizers {
			finalizers = append(finalizers, finalizer)
		}
		sort.Strings(finalizers)
		result[id] = ObjectState{
			ID:         id,
			Owners:     owners,
			Finalizers: finalizers,
			Deleting:   node.deleting,
			Policy:     node.policy,
			DeleteAt:   node.deleteAt,
		}
	}
	return result
}

func errorKind(err error) ErrorKind {
	var controllerErr ControllerError
	if errors.As(err, &controllerErr) {
		return controllerErr.Kind
	}
	return 0
}
