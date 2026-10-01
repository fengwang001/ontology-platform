package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveModel struct {
	parents map[string]string
	locks   map[string]map[int]Mode
}

type testOperation struct {
	kind        string
	transaction int
	node        string
	parent      string
	mode        Mode
}

type testResult struct {
	errText string
	mode    Mode
	held    bool
	holders []Holder
	basis   string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		parents: map[string]string{},
		locks:   map[string]map[int]Mode{},
	}
}

func (model *naiveModel) register(operation testOperation) testResult {
	if operation.node == "" {
		return testResult{errText: "invalid node id", basis: "Register rejects an empty id before existence or parent checks"}
	}
	if _, exists := model.parents[operation.node]; exists {
		return testResult{errText: "node exists", basis: "Register rejects an id already present"}
	}
	if operation.parent != "" {
		if _, exists := model.parents[operation.parent]; !exists {
			return testResult{errText: "parent not found", basis: "Register rejects a non-empty parent that is not registered"}
		}
	}
	model.parents[operation.node] = operation.parent
	model.locks[operation.node] = map[int]Mode{}
	return testResult{basis: "Register inserts one node without changing locks"}
}

func (model *naiveModel) lock(operation testOperation) testResult {
	if operation.transaction <= 0 {
		return testResult{errText: "invalid transaction", basis: "Lock validates transaction positivity first"}
	}
	if !ValidMode(operation.mode) {
		return testResult{errText: "invalid mode", basis: "Lock validates that requested mode is IS, IX, S, SIX, or X"}
	}
	holders, exists := model.locks[operation.node]
	if !exists {
		return testResult{errText: "node not found", basis: "Lock validates node registration after request arguments"}
	}

	heldMode := holders[operation.transaction]
	if heldMode != "" && ModeLE(operation.mode, heldMode) {
		return testResult{basis: fmt.Sprintf("held %s already dominates requested %s; no ancestor or conflict check", heldMode, operation.mode)}
	}

	targetMode := operation.mode
	if heldMode != "" {
		targetMode = JoinMode(heldMode, operation.mode)
	}
	required := IS
	if targetMode == IX || targetMode == SIX || targetMode == X {
		required = IX
	}

	for _, ancestor := range model.ancestors(operation.node) {
		ancestorHeld := model.locks[ancestor][operation.transaction]
		if !ModeLE(required, ancestorHeld) {
			return testResult{
				errText: fmt.Sprintf("missing intent ancestor=%s required=%s", ancestor, required),
				basis:   fmt.Sprintf("join(%s,%s)=%s, so ancestor %s must hold at least %s but holds %s", heldMode, operation.mode, targetMode, ancestor, required, ancestorHeld),
			}
		}
	}

	conflictTransaction := 0
	var conflictMode Mode
	for otherTransaction, otherMode := range holders {
		if otherTransaction != operation.transaction && !CompatibleModes(targetMode, otherMode) && (conflictTransaction == 0 || otherTransaction < conflictTransaction) {
			conflictTransaction = otherTransaction
			conflictMode = otherMode
		}
	}
	if conflictTransaction != 0 {
		return testResult{
			errText: fmt.Sprintf("conflict transaction=%d mode=%s", conflictTransaction, conflictMode),
			basis:   fmt.Sprintf("target %s is incompatible with minimum conflicting transaction %d holding %s", targetMode, conflictTransaction, conflictMode),
		}
	}

	holders[operation.transaction] = targetMode
	return testResult{basis: fmt.Sprintf("join(%s,%s)=%s; ancestors satisfy %s and other holders are compatible", heldMode, operation.mode, targetMode, required)}
}

func (model *naiveModel) ancestors(node string) []string {
	var chain []string
	current := model.parents[node]
	for current != "" {
		chain = append(chain, current)
		current = model.parents[current]
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return chain
}

func (model *naiveModel) isDescendant(node, ancestor string) bool {
	current := model.parents[node]
	for current != "" {
		if current == ancestor {
			return true
		}
		current = model.parents[current]
	}
	return false
}

func (model *naiveModel) unlock(operation testOperation) testResult {
	if operation.transaction <= 0 {
		return testResult{errText: "invalid transaction", basis: "Unlock validates transaction positivity first"}
	}
	if _, exists := model.parents[operation.node]; !exists {
		return testResult{errText: "node not found", basis: "Unlock validates node registration before ownership"}
	}
	if _, held := model.locks[operation.node][operation.transaction]; !held {
		return testResult{errText: "lock not held", basis: "Unlock requires the transaction to hold the node"}
	}
	for descendant := range model.parents {
		if descendant != operation.node && model.isDescendant(descendant, operation.node) {
			if _, held := model.locks[descendant][operation.transaction]; held {
				return testResult{
					errText: "descendant locked",
					basis:   fmt.Sprintf("descendant %s remains held, so removing the ancestor lock would break intent invariants", descendant),
				}
			}
		}
	}
	delete(model.locks[operation.node], operation.transaction)
	return testResult{basis: "Unlock removes one node only after finding no held descendant"}
}

func (model *naiveModel) releaseAll(operation testOperation) testResult {
	if operation.transaction <= 0 {
		return testResult{errText: "invalid transaction", basis: "ReleaseAll validates transaction positivity"}
	}
	for _, holders := range model.locks {
		delete(holders, operation.transaction)
	}
	return testResult{basis: "ReleaseAll deletes every lock for the transaction in one logical step"}
}

func (model *naiveModel) held(operation testOperation) testResult {
	if operation.transaction <= 0 {
		return testResult{errText: "invalid transaction", basis: "Held validates transaction positivity first"}
	}
	holders, exists := model.locks[operation.node]
	if !exists {
		return testResult{errText: "node not found", basis: "Held validates node registration"}
	}
	mode, isHeld := holders[operation.transaction]
	return testResult{mode: mode, held: isHeld, basis: "Held returns the single mode held by that transaction at that node"}
}

func (model *naiveModel) holders(operation testOperation) testResult {
	holders, exists := model.locks[operation.node]
	if !exists {
		return testResult{errText: "node not found", basis: "Holders validates node registration"}
	}
	result := testResult{holders: []Holder{}, basis: "Holders returns all holders sorted by transaction id"}
	for transaction, mode := range holders {
		result.holders = append(result.holders, Holder{Transaction: transaction, Mode: mode})
	}
	sortTestHolders(result.holders)
	return result
}

func (model *naiveModel) execute(operation testOperation) testResult {
	switch operation.kind {
	case "register":
		return model.register(operation)
	case "lock":
		return model.lock(operation)
	case "unlock":
		return model.unlock(operation)
	case "release":
		return model.releaseAll(operation)
	case "held":
		return model.held(operation)
	case "holders":
		return model.holders(operation)
	default:
		panic("unknown operation")
	}
}

func sortTestHolders(holders []Holder) {
	sort.Slice(holders, func(i, j int) bool {
		return holders[i].Transaction < holders[j].Transaction
	})
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	modes := []Mode{IS, IX, S, SIX, X}
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			manager := NewManager()
			model := newNaiveModel()
			registered := []string{}
			nextNode := 0

			for step := 0; step < 24; step++ {
				var operation testOperation
				if len(registered) == 0 || random.Intn(4) == 0 {
					id := fmt.Sprintf("n%d", nextNode)
					nextNode++
					parent := ""
					if len(registered) > 0 {
						switch random.Intn(5) {
						case 0:
							id = ""
						case 1:
							id = registered[random.Intn(len(registered))]
						case 2:
							parent = "missing-parent"
						case 3:
							parent = registered[random.Intn(len(registered))]
						}
					}
					operation = testOperation{kind: "register", node: id, parent: parent}
				} else {
					node := registered[random.Intn(len(registered))]
					if random.Intn(7) == 0 {
						node = "missing-node"
					}
					switch random.Intn(10) {
					case 0, 1, 2:
						operation = testOperation{kind: "lock", transaction: randomTransaction(random), node: node, mode: randomMode(random, modes)}
					case 3, 4:
						operation = testOperation{kind: "unlock", transaction: randomTransaction(random), node: node}
					case 5:
						operation = testOperation{kind: "release", transaction: randomTransaction(random)}
					case 6, 7:
						operation = testOperation{kind: "held", transaction: randomTransaction(random), node: node}
					default:
						operation = testOperation{kind: "holders", node: node}
					}
				}

				compareOperation(t, manager, model, operation)
				if operation.kind == "register" && operation.node != "" {
					if _, exists := model.parents[operation.node]; exists && !containsString(registered, operation.node) {
						registered = append(registered, operation.node)
					}
				}
				assertManagerInvariants(t, manager)
			}
		})
	}
}

func randomTransaction(random *rand.Rand) int {
	if random.Intn(10) == 0 {
		return 0
	}
	return 1 + random.Intn(4)
}

func randomMode(random *rand.Rand, modes []Mode) Mode {
	if random.Intn(12) == 0 {
		return "BAD"
	}
	return modes[random.Intn(len(modes))]
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func compareOperation(t *testing.T, manager *Manager, model *naiveModel, operation testOperation) {
	t.Helper()
	expected := model.execute(operation)
	actual := executeManager(manager, operation)
	if expected.holders == nil {
		expected.holders = []Holder{}
	}
	t.Logf("input=%+v output=%q expected=%q basis=%s", operation, actual.errText, expected.errText, expected.basis)
	if actual.errText != expected.errText || actual.mode != expected.mode || actual.held != expected.held || !reflect.DeepEqual(actual.holders, expected.holders) {
		t.Fatalf("mismatch for %+v: actual=%+v expected=%+v", operation, actual, expected)
	}
}

func executeManager(manager *Manager, operation testOperation) testResult {
	var err error
	result := testResult{holders: []Holder{}}
	switch operation.kind {
	case "register":
		err = manager.Register(operation.node, operation.parent)
	case "lock":
		err = manager.Lock(operation.transaction, operation.node, operation.mode)
	case "unlock":
		err = manager.Unlock(operation.transaction, operation.node)
	case "release":
		err = manager.ReleaseAll(operation.transaction)
	case "held":
		result.mode, result.held, err = manager.Held(operation.transaction, operation.node)
	case "holders":
		result.holders, err = manager.Holders(operation.node)
	}
	result.errText = classifyTestError(err)
	if result.holders == nil {
		result.holders = []Holder{}
	}
	return result
}

func classifyTestError(err error) string {
	if err == nil {
		return ""
	}
	var missing *MissingIntentError
	if errors.As(err, &missing) {
		return fmt.Sprintf("missing intent ancestor=%s required=%s", missing.Ancestor, missing.Required)
	}
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return fmt.Sprintf("conflict transaction=%d mode=%s", conflict.Transaction, conflict.Mode)
	}
	switch {
	case errors.Is(err, ErrInvalidTransaction):
		return "invalid transaction"
	case errors.Is(err, ErrInvalidMode):
		return "invalid mode"
	case errors.Is(err, ErrInvalidNodeID):
		return "invalid node id"
	case errors.Is(err, ErrNodeNotFound):
		return "node not found"
	case errors.Is(err, ErrNodeExists):
		return "node exists"
	case errors.Is(err, ErrParentNotFound):
		return "parent not found"
	case errors.Is(err, ErrLockNotHeld):
		return "lock not held"
	case errors.Is(err, ErrDescendantLocked):
		return "descendant locked"
	default:
		return err.Error()
	}
}

func assertManagerInvariants(t *testing.T, manager *Manager) {
	t.Helper()
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	for nodeID, node := range manager.nodes {
		transactions := make([]int, 0, len(node.locks))
		for transaction := range node.locks {
			transactions = append(transactions, transaction)
		}
		sort.Ints(transactions)
		for index, left := range transactions {
			leftMode := node.locks[left]
			for _, right := range transactions[index+1:] {
				if !CompatibleModes(leftMode, node.locks[right]) {
					t.Fatalf("incompatible co-locks on %q: %d=%s, %d=%s", nodeID, left, leftMode, right, node.locks[right])
				}
			}

			required := IS
			if leftMode == IX || leftMode == SIX || leftMode == X {
				required = IX
			}
			current := node.parent
			for current != "" {
				ancestorMode := manager.nodes[current].locks[left]
				if !ModeLE(required, ancestorMode) {
					t.Fatalf("transaction %d holds %s on %q but ancestor %q holds %s, require %s", left, leftMode, nodeID, current, ancestorMode, required)
				}
				current = manager.nodes[current].parent
			}
		}
	}
}
