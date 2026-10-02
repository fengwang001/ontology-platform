package percolator

import "sync"

type mutex = sync.Mutex

type txnState int

const (
	txnFresh txnState = iota
	txnPrewritten
	txnCommitted
	txnAborted
)

type txnInfo struct {
	state    txnState
	muts     []Mutation
	primary  string
	commitTS int64
}

type keyState struct {
	versions []Version
	lock     *Lock
}

type storeData struct {
	oracle int64
	water  int64
	txns   map[int64]*txnInfo
	keys   map[string]*keyState
}
