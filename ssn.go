package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig          = errors.New("invalid configuration")
	ErrTxNotFound             = errors.New("transaction not found")
	ErrTxFinished             = errors.New("transaction already finished")
	ErrKeyOutOfRange          = errors.New("key out of range")
	ErrSnapshotTooOld         = errors.New("snapshot too old")
	ErrWriteWriteConflict     = errors.New("write-write conflict")
	ErrSerializationSafetyNet = errors.New("serialization safety net rejection")
)

type AbortReason int

const (
	AbortNone AbortReason = iota
	AbortWriteWriteConflict
	AbortSerializationSafetyNet
)

type version struct {
	cs    int
	ps    int
	ss    int
	value int
}

type transaction struct {
	snapshot int
	readSet  map[int]*version
	writeSet map[int]int
	active   bool
}

type authenticatorState struct {
	keys         int
	history      int
	commitNumber int
	nextTxID     int
	versions     [][]*version
	transactions map[int]*transaction
}

type Authenticator struct {
	mu    sync.Mutex
	state authenticatorState
}

func NewAuthenticator(keys int, history int) (*Authenticator, error) {
	if keys < 1 || keys > 64 || history < 2 || history > 8 {
		return nil, ErrInvalidConfig
	}

	a := &Authenticator{}
	a.state.keys = keys
	a.state.history = history
	a.state.nextTxID = 1
	a.state.transactions = make(map[int]*transaction)
	a.state.versions = make([][]*version, keys)
	for key := range a.state.versions {
		a.state.versions[key] = []*version{{cs: 0, ps: 0, ss: infinity, value: 0}}
	}
	return a, nil
}

const infinity = int(^uint(0) >> 1)

func (a *Authenticator) Begin() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	txID := a.state.nextTxID
	a.state.nextTxID++
	a.state.transactions[txID] = &transaction{
		snapshot: a.state.commitNumber,
		readSet:  make(map[int]*version),
		writeSet: make(map[int]int),
		active:   true,
	}
	return txID
}

func (a *Authenticator) activeTransaction(txID int) (*transaction, error) {
	tx, ok := a.state.transactions[txID]
	if !ok {
		return nil, ErrTxNotFound
	}
	if !tx.active {
		return nil, ErrTxFinished
	}
	return tx, nil
}

func (a *Authenticator) Read(txID int, key int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	tx, err := a.activeTransaction(txID)
	if err != nil {
		return 0, err
	}
	if key < 0 || key >= a.state.keys {
		return 0, ErrKeyOutOfRange
	}

	if value, ok := tx.writeSet[key]; ok {
		return value, nil
	}
	if readVersion, ok := tx.readSet[key]; ok {
		return readVersion.value, nil
	}

	chain := a.state.versions[key]
	if oldest := chain[0]; oldest.cs > tx.snapshot {
		return 0, ErrSnapshotTooOld
	}

	var selected *version
	for _, candidate := range chain {
		if candidate.cs <= tx.snapshot {
			selected = candidate
		} else {
			break
		}
	}
	tx.readSet[key] = selected
	return selected.value, nil
}

func (a *Authenticator) Write(txID int, key int, value int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	tx, err := a.activeTransaction(txID)
	if err != nil {
		return err
	}
	if key < 0 || key >= a.state.keys {
		return ErrKeyOutOfRange
	}

	tx.writeSet[key] = value
	return nil
}

func (a *Authenticator) Commit(txID int) (int, AbortReason, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	tx, err := a.activeTransaction(txID)
	if err != nil {
		return 0, AbortNone, err
	}

	latestVersions := make(map[int]*version, len(tx.writeSet))
	for key := range tx.writeSet {
		latest := a.state.versions[key][len(a.state.versions[key])-1]
		latestVersions[key] = latest
		if latest.cs > tx.snapshot {
			tx.active = false
			return 0, AbortWriteWriteConflict, ErrWriteWriteConflict
		}
	}

	commitID := a.state.commitNumber + 1
	predatorHighWater := 0
	successorLowWater := commitID

	for _, readVersion := range tx.readSet {
		predatorHighWater = max(predatorHighWater, readVersion.cs)
		successorLowWater = min(successorLowWater, readVersion.ss)
	}
	for key := range tx.writeSet {
		latest := latestVersions[key]
		if readVersion, read := tx.readSet[key]; read {
			latest = readVersion
		}
		predatorHighWater = max(predatorHighWater, latest.cs, latest.ps)
	}

	if successorLowWater <= predatorHighWater {
		tx.active = false
		return 0, AbortSerializationSafetyNet, ErrSerializationSafetyNet
	}

	a.state.commitNumber = commitID
	for _, readVersion := range tx.readSet {
		readVersion.ps = max(readVersion.ps, commitID)
	}
	for key := range tx.writeSet {
		previous := latestVersions[key]
		chain := a.state.versions[key]
		previous.ss = successorLowWater
		a.state.versions[key] = append(chain, &version{
			cs:    commitID,
			ps:    0,
			ss:    infinity,
			value: tx.writeSet[key],
		})
		if len(a.state.versions[key]) > a.state.history {
			a.state.versions[key] = a.state.versions[key][1:]
		}
	}

	tx.active = false
	return commitID, AbortNone, nil
}

func (a *Authenticator) Abort(txID int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	tx, err := a.activeTransaction(txID)
	if err != nil {
		return err
	}
	tx.active = false
	return nil
}
