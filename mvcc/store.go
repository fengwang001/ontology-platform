package mvcc

import (
	"fmt"
	"sync"
)

type TxState int

const (
	Running TxState = iota
	SubCommitted
	Committed
	Aborted
)

type EffectiveStatus int

const (
	EffActive EffectiveStatus = iota
	EffCommitted
	EffAborted
)

func (e EffectiveStatus) String() string {
	switch e {
	case EffCommitted:
		return "committed"
	case EffAborted:
		return "aborted"
	default:
		return "active"
	}
}

type transaction struct {
	id       int
	parent   int
	children map[int]bool
	state    TxState
	maxCmd   int
	hasCmd   bool
}

type tupleVersion struct {
	xmin    int
	cmin    int
	xmax    int
	cmax    int
	hasXmax bool
}

type Store struct {
	mu           sync.Mutex
	txs          map[int]*transaction
	tuples       map[string]*tupleVersion
	snapshots    map[int]map[int]bool
	nextSnapshot int
}

func NewStore() *Store {
	return &Store{
		txs:          make(map[int]*transaction),
		tuples:       make(map[string]*tupleVersion),
		snapshots:    make(map[int]map[int]bool),
		nextSnapshot: 1,
	}
}

func (s *Store) Begin(x int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x <= 0 {
		return ErrTxIDNotPositive
	}
	if _, ok := s.txs[x]; ok {
		return ErrTxExists
	}
	s.txs[x] = &transaction{id: x, children: make(map[int]bool), state: Running}
	return nil
}

func (s *Store) BeginSub(x, p int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x <= 0 {
		return ErrTxIDNotPositive
	}
	if _, ok := s.txs[x]; ok {
		return ErrTxExists
	}
	parent, ok := s.txs[p]
	if !ok {
		return ErrParentNotFound
	}
	if parent.state != Running {
		return ErrParentNotRunning
	}
	s.txs[x] = &transaction{id: x, parent: p, children: make(map[int]bool), state: Running}
	parent.children[x] = true
	return nil
}

func (s *Store) CommitSub(x int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.commitCheck(x, true)
	if err != nil {
		return err
	}
	tx.state = SubCommitted
	return nil
}

func (s *Store) Commit(x int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.commitCheck(x, false)
	if err != nil {
		return err
	}
	tx.state = Committed
	return nil
}

func (s *Store) commitCheck(x int, sub bool) (*transaction, error) {
	tx, ok := s.txs[x]
	if !ok {
		return nil, ErrTxNotFound
	}
	if tx.state != Running {
		return nil, ErrTxNotRunning
	}
	if isSub := tx.parent != 0; isSub != sub {
		return nil, ErrTxTypeMismatch
	}
	if s.hasRunningDescendant(tx) {
		return nil, ErrTxHasRunningDescendants
	}
	return tx, nil
}

func (s *Store) hasRunningDescendant(tx *transaction) bool {
	for id := range tx.children {
		child := s.txs[id]
		if child.state == Running || s.hasRunningDescendant(child) {
			return true
		}
	}
	return false
}

func (s *Store) Abort(x int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.txs[x]
	if !ok {
		return ErrTxNotFound
	}
	if tx.state != Running {
		return ErrTxNotRunning
	}
	s.abortTree(tx)
	return nil
}

func (s *Store) abortTree(tx *transaction) {
	tx.state = Aborted
	for id := range tx.children {
		s.abortTree(s.txs[id])
	}
}

func (s *Store) Snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	committed := make(map[int]bool)
	for _, tx := range s.txs {
		if tx.parent == 0 && tx.state == Committed {
			committed[tx.id] = true
		}
	}
	id := s.nextSnapshot
	s.snapshots[id] = committed
	s.nextSnapshot++
	return id
}

func (s *Store) Insert(t string, x, c int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.writeCheck(x, c)
	if err != nil {
		return err
	}
	if _, ok := s.tuples[t]; ok {
		return ErrTupleExists
	}
	s.tuples[t] = &tupleVersion{xmin: x, cmin: c}
	root.maxCmd = c
	root.hasCmd = true
	return nil
}

func (s *Store) Delete(t string, x, c int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.writeCheck(x, c)
	if err != nil {
		return err
	}
	tv, ok := s.tuples[t]
	if !ok {
		return ErrTupleNotFound
	}
	if tv.hasXmax && s.effective(s.txs[tv.xmax]) != EffAborted {
		return ErrTupleDeleted
	}
	tv.xmax = x
	tv.cmax = c
	tv.hasXmax = true
	root.maxCmd = c
	root.hasCmd = true
	return nil
}

func (s *Store) writeCheck(x, c int) (*transaction, error) {
	tx, ok := s.txs[x]
	if !ok {
		return nil, ErrTxNotFound
	}
	if tx.state != Running {
		return nil, ErrTxNotRunning
	}
	if c < 0 {
		return nil, ErrNegativeCommandID
	}
	root := s.rootOf(tx)
	if root.hasCmd && c < root.maxCmd {
		return nil, ErrCommandIDTooSmall
	}
	return root, nil
}

func (s *Store) rootOf(tx *transaction) *transaction {
	for tx.parent != 0 {
		tx = s.txs[tx.parent]
	}
	return tx
}

func (s *Store) effective(tx *transaction) EffectiveStatus {
	if tx.state == Aborted {
		return EffAborted
	}
	if tx.state == SubCommitted || tx.state == Committed {
		if s.rootOf(tx).state == Committed {
			return EffCommitted
		}
	}
	return EffActive
}

func (s *Store) Visible(t string, x, c, snap int) (bool, error) {
	visible, _, err := s.Explain(t, x, c, snap)
	return visible, err
}

func (s *Store) Explain(t string, x, c, snap int) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.txs[x]
	if !ok {
		return false, "", ErrTxNotFound
	}
	if tx.state != Running {
		return false, "", ErrTxNotRunning
	}
	if c < 0 {
		return false, "", ErrNegativeCommandID
	}
	committed, ok := s.snapshots[snap]
	if !ok {
		return false, "", ErrUnknownSnapshot
	}
	tv, ok := s.tuples[t]
	if !ok {
		return false, "", ErrTupleNotFound
	}
	xminSeen, xminWhy := s.seen(tv.xmin, tv.cmin, tx, c, committed)
	xmaxSeen := false
	xmaxWhy := "no delete marker"
	if tv.hasXmax {
		xmaxSeen, xmaxWhy = s.seen(tv.xmax, tv.cmax, tx, c, committed)
	}
	visible := xminSeen && !xmaxSeen
	why := fmt.Sprintf("xmin=(tx %d,cmd %d) seen=%v [%s]; xmax seen=%v [%s] => visible=%v",
		tv.xmin, tv.cmin, xminSeen, xminWhy, xmaxSeen, xmaxWhy, visible)
	return visible, why, nil
}

func (s *Store) seen(y, cy int, observer *transaction, c int, committed map[int]bool) (bool, string) {
	yTx := s.txs[y]
	yRoot := s.rootOf(yTx)
	xRoot := s.rootOf(observer)
	eff := s.effective(yTx)
	if yRoot.id == xRoot.id {
		seen := eff != EffAborted && cy < c
		return seen, fmt.Sprintf("same root %d, effective=%s, cmd %d < %d", yRoot.id, eff, cy, c)
	}
	inSnap := committed[yRoot.id]
	seen := eff == EffCommitted && inSnap
	return seen, fmt.Sprintf("different root %d, effective=%s, root in snapshot=%v", yRoot.id, eff, inSnap)
}
