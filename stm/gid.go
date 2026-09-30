package stm

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
)

// activeTxns tracks goroutines currently executing a transaction body, used
// to detect nested transactions.
var activeTxns = struct {
	sync.Mutex
	m map[int64]bool
}{m: make(map[int64]bool)}

// goid returns the current goroutine's id.
func goid() int64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	id, _ := strconv.ParseInt(string(b), 10, 64)
	return id
}

// enterTxn registers the current goroutine as running a transaction body.
// It panics with ErrNested if the goroutine is already inside one.
func enterTxn() {
	id := goid()
	activeTxns.Lock()
	defer activeTxns.Unlock()
	if activeTxns.m[id] {
		panic(ErrNested)
	}
	activeTxns.m[id] = true
}

// leaveTxn unregisters the current goroutine.
func leaveTxn() {
	id := goid()
	activeTxns.Lock()
	delete(activeTxns.m, id)
	activeTxns.Unlock()
}
