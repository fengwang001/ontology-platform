package ontology

import "sync"

type TxStatus int

const (
	Active TxStatus = iota
	Prepared
	Committed
	Aborted
)

type version struct {
	value   int
	creator int
	ender   int
}

type transaction struct {
	id       int
	readTime int
	endTime  int
	status   TxStatus
	finished bool
}

type Engine struct {
	mu           sync.Mutex
	keyCount     int
	clock        int
	nextTxID     int
	transactions map[int]*transaction
	versions     [][]*version
	dependencies map[int]map[int]struct{}
	dependents   map[int]map[int]struct{}
}

const noTx = 0

func NewEngine(k int) (*Engine, error) {
	if k < 1 || k > 64 {
		return nil, ErrInvalidKeyCount
	}

	e := &Engine{
		keyCount:     k,
		nextTxID:     1,
		transactions: make(map[int]*transaction),
		versions:     make([][]*version, k),
		dependencies: make(map[int]map[int]struct{}),
		dependents:   make(map[int]map[int]struct{}),
	}
	for key := range e.versions {
		e.versions[key] = []*version{{
			value:   0,
			creator: noTx,
			ender:   noTx,
		}}
	}
	return e, nil
}

func (e *Engine) Begin() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.clock++
	id := e.nextTxID
	e.nextTxID++
	e.transactions[id] = &transaction{
		id:       id,
		readTime: e.clock,
		status:   Active,
	}
	e.dependencies[id] = make(map[int]struct{})
	e.dependents[id] = make(map[int]struct{})
	return id
}

func (e *Engine) Read(txID int, key int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.activeTx(txID)
	if err != nil {
		return 0, err
	}
	if err := validKey(e.keyCount, key); err != nil {
		return 0, err
	}

	for i := len(e.versions[key]) - 1; i >= 0; i-- {
		v := e.versions[key][i]
		if e.isGarbage(v) {
			continue
		}
		if !e.startVisible(tx, v) {
			continue
		}
		if !e.endVisible(tx, v) {
			continue
		}

		if v.creator != noTx && v.creator != txID {
			creator := e.transactions[v.creator]
			if creator.status == Prepared {
				e.addDependency(txID, v.creator)
			}
		}
		return v.value, nil
	}

	return 0, ErrNoVisibleVersion
}

func (e *Engine) Write(txID int, key int, value int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.activeTx(txID)
	if err != nil {
		return err
	}
	if err := validKey(e.keyCount, key); err != nil {
		return err
	}

	v := e.latestNonGarbage(key)
	if v.creator == txID {
		v.value = value
		return nil
	}

	if e.effectiveEnder(v) != noTx {
		e.abortFromConflict(tx)
		return ErrWriteConflict
	}

	creator := e.transactions[v.creator]
	if v.creator != noTx && creator.status == Active {
		e.abortFromConflict(tx)
		return ErrWriteConflict
	}
	if e.creatorEndTime(v.creator) >= tx.readTime {
		e.abortFromConflict(tx)
		return ErrWriteConflict
	}

	v.ender = txID
	e.versions[key] = append(e.versions[key], &version{
		value:   value,
		creator: txID,
		ender:   noTx,
	})
	if creator != nil && creator.status == Prepared {
		e.addDependency(txID, v.creator)
	}
	return nil
}

func (e *Engine) Precommit(txID int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.activeTx(txID)
	if err != nil {
		return 0, err
	}
	e.clock++
	tx.status = Prepared
	tx.endTime = e.clock
	return tx.endTime, nil
}

func (e *Engine) Finish(txID int) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.txByID(txID)
	if err != nil {
		return nil, err
	}
	if tx.status != Prepared || tx.finished {
		return nil, ErrInvalidStatus
	}

	tx.finished = true
	if len(e.dependencies[txID]) == 0 {
		return e.commitReady(txID), nil
	}
	return []int{}, nil
}

func (e *Engine) Abort(txID int) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx, err := e.txByID(txID)
	if err != nil {
		return nil, err
	}
	if tx.status != Active && tx.status != Prepared {
		return nil, ErrInvalidStatus
	}

	aborted := make([]int, 0)
	e.cascadeAbort(txID, &aborted)
	return aborted, nil
}

func (e *Engine) txByID(id int) (*transaction, error) {
	tx, ok := e.transactions[id]
	if !ok {
		return nil, ErrUnknownTx
	}
	return tx, nil
}

func (e *Engine) activeTx(id int) (*transaction, error) {
	tx, err := e.txByID(id)
	if err != nil {
		return nil, err
	}
	if tx.status != Active {
		return nil, ErrInvalidStatus
	}
	return tx, nil
}

func validKey(k, key int) error {
	if key < 0 || key >= k {
		return ErrInvalidKey
	}
	return nil
}

func (e *Engine) isGarbage(v *version) bool {
	return v.creator != noTx && e.transactions[v.creator].status == Aborted
}

func (e *Engine) effectiveEnder(v *version) int {
	if v.ender == noTx {
		return noTx
	}
	if e.transactions[v.ender].status == Aborted {
		return noTx
	}
	return v.ender
}

func (e *Engine) startVisible(reader *transaction, v *version) bool {
	if v.creator == noTx {
		return true
	}
	if v.creator == reader.id {
		return true
	}

	creator := e.transactions[v.creator]
	switch creator.status {
	case Active, Aborted:
		return false
	case Prepared, Committed:
		return creator.endTime < reader.readTime
	default:
		return false
	}
}

func (e *Engine) endVisible(reader *transaction, v *version) bool {
	enderID := e.effectiveEnder(v)
	if enderID == noTx {
		return true
	}
	if enderID == reader.id {
		return false
	}

	ender := e.transactions[enderID]
	switch ender.status {
	case Active:
		return true
	case Prepared, Committed:
		return reader.readTime < ender.endTime
	default:
		return false
	}
}

func (e *Engine) latestNonGarbage(key int) *version {
	for i := len(e.versions[key]) - 1; i >= 0; i-- {
		v := e.versions[key][i]
		if !e.isGarbage(v) {
			return v
		}
	}
	return nil
}

func (e *Engine) creatorEndTime(id int) int {
	if id == noTx {
		return 0
	}
	return e.transactions[id].endTime
}

func (e *Engine) addDependency(dependent, dependency int) {
	if _, ok := e.dependencies[dependent][dependency]; !ok {
		e.dependencies[dependent][dependency] = struct{}{}
		e.dependents[dependency][dependent] = struct{}{}
	}
}

func (e *Engine) removeDependency(dependent, dependency int) {
	delete(e.dependencies[dependent], dependency)
	delete(e.dependents[dependency], dependent)
}

func (e *Engine) abortFromConflict(tx *transaction) {
	aborted := make([]int, 0)
	e.cascadeAbort(tx.id, &aborted)
}

func (e *Engine) cascadeAbort(rootID int, aborted *[]int) {
	queue := []int{rootID}
	seen := map[int]struct{}{rootID: {}}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for dependent := range e.dependents[id] {
			if _, ok := seen[dependent]; !ok {
				seen[dependent] = struct{}{}
				queue = append(queue, dependent)
			}
		}
	}

	for id := range seen {
		tx := e.transactions[id]
		tx.status = Aborted
		for dependency := range e.dependencies[id] {
			delete(e.dependents[dependency], id)
		}
	}

	*aborted = sortedKeys(seen)
	for id := range seen {
		e.dependencies[id] = make(map[int]struct{})
		e.dependents[id] = make(map[int]struct{})
	}
}

func (e *Engine) commitReady(firstID int) []int {
	committed := make([]int, 0)
	ready := map[int]struct{}{firstID: {}}

	for len(ready) > 0 {
		id := smallestKey(ready)
		delete(ready, id)

		tx := e.transactions[id]
		tx.status = Committed
		committed = append(committed, id)

		dependents := sortedKeys(e.dependents[id])
		for _, dependentID := range dependents {
			e.removeDependency(dependentID, id)
			dependent := e.transactions[dependentID]
			if dependent.status == Active {
				continue
			}
			if dependent.status == Prepared && dependent.finished && len(e.dependencies[dependentID]) == 0 {
				ready[dependentID] = struct{}{}
			}
		}
	}

	return committed
}

func smallestKey(values map[int]struct{}) int {
	result := 0
	first := true
	for value := range values {
		if first || value < result {
			result = value
			first = false
		}
	}
	return result
}

func sortedKeys(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sortInts(result)
	return result
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
