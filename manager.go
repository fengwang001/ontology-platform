package ontology

import "sync"

type nodeInfo struct {
	parent string
	locks  map[int]Mode
}

type Holder struct {
	Transaction int
	Mode        Mode
}

type Manager struct {
	mu    sync.RWMutex
	nodes map[string]*nodeInfo
}

func NewManager() *Manager {
	return &Manager{nodes: make(map[string]*nodeInfo)}
}

func (manager *Manager) Register(id, parent string) error {
	if id == "" {
		return ErrInvalidNodeID
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	if _, exists := manager.nodes[id]; exists {
		return ErrNodeExists
	}
	if parent != "" {
		if _, exists := manager.nodes[parent]; !exists {
			return ErrParentNotFound
		}
	}

	manager.nodes[id] = &nodeInfo{
		parent: parent,
		locks:  make(map[int]Mode),
	}
	return nil
}

func (manager *Manager) Lock(transaction int, node string, mode Mode) error {
	if transaction <= 0 {
		return ErrInvalidTransaction
	}
	if !ValidMode(mode) {
		return ErrInvalidMode
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	targetNode, exists := manager.nodes[node]
	if !exists {
		return ErrNodeNotFound
	}

	held := targetNode.locks[transaction]
	if held != "" && ModeLE(mode, held) {
		return nil
	}

	targetMode := mode
	if held != "" {
		targetMode = JoinMode(held, mode)
	}

	required := IS
	if targetMode == IX || targetMode == SIX || targetMode == X {
		required = IX
	}

	for _, ancestorID := range manager.ancestorChainLocked(node) {
		ancestorHeld := manager.nodes[ancestorID].locks[transaction]
		if !ModeLE(required, ancestorHeld) {
			return &MissingIntentError{Ancestor: ancestorID, Required: required}
		}
	}

	var conflictTransaction int
	var conflictMode Mode
	for otherTransaction, otherMode := range targetNode.locks {
		if otherTransaction == transaction || CompatibleModes(targetMode, otherMode) {
			continue
		}
		if conflictTransaction == 0 || otherTransaction < conflictTransaction {
			conflictTransaction = otherTransaction
			conflictMode = otherMode
		}
	}
	if conflictTransaction != 0 {
		return &ConflictError{Transaction: conflictTransaction, Mode: conflictMode}
	}

	targetNode.locks[transaction] = targetMode
	return nil
}

func (manager *Manager) Unlock(transaction int, node string) error {
	if transaction <= 0 {
		return ErrInvalidTransaction
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	targetNode, exists := manager.nodes[node]
	if !exists {
		return ErrNodeNotFound
	}
	if _, held := targetNode.locks[transaction]; !held {
		return ErrLockNotHeld
	}

	for descendantID := range manager.nodes {
		if descendantID != node && manager.isDescendantLocked(descendantID, node) {
			if _, held := manager.nodes[descendantID].locks[transaction]; held {
				return ErrDescendantLocked
			}
		}
	}

	delete(targetNode.locks, transaction)
	return nil
}

func (manager *Manager) ReleaseAll(transaction int) error {
	if transaction <= 0 {
		return ErrInvalidTransaction
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	for _, node := range manager.nodes {
		delete(node.locks, transaction)
	}
	return nil
}

func (manager *Manager) Held(transaction int, node string) (Mode, bool, error) {
	if transaction <= 0 {
		return "", false, ErrInvalidTransaction
	}

	manager.mu.RLock()
	defer manager.mu.RUnlock()

	targetNode, exists := manager.nodes[node]
	if !exists {
		return "", false, ErrNodeNotFound
	}

	mode, held := targetNode.locks[transaction]
	return mode, held, nil
}

func (manager *Manager) Holders(node string) ([]Holder, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	targetNode, exists := manager.nodes[node]
	if !exists {
		return nil, ErrNodeNotFound
	}

	holders := make([]Holder, 0, len(targetNode.locks))
	for transaction, mode := range targetNode.locks {
		holders = append(holders, Holder{Transaction: transaction, Mode: mode})
	}
	sortHolders(holders)
	return holders, nil
}

func (manager *Manager) ancestorChainLocked(node string) []string {
	var chain []string
	current := manager.nodes[node].parent
	for current != "" {
		chain = append(chain, current)
		current = manager.nodes[current].parent
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return chain
}

func (manager *Manager) isDescendantLocked(node, ancestor string) bool {
	current := manager.nodes[node].parent
	for current != "" {
		if current == ancestor {
			return true
		}
		current = manager.nodes[current].parent
	}
	return false
}

func sortHolders(holders []Holder) {
	for index := 1; index < len(holders); index++ {
		for previous := index; previous > 0 && holders[previous-1].Transaction > holders[previous].Transaction; previous-- {
			holders[previous-1], holders[previous] = holders[previous], holders[previous-1]
		}
	}
}
