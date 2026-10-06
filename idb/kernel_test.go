package idb_test

import (
	"io"
	"log"
	"testing"
	"time"

	"ontology/idb"
)

func testKernel(t *testing.T) *idb.Kernel {
	t.Helper()
	var w io.Writer = io.Discard
	if testing.Verbose() {
		w = log.Writer()
	}
	k := idb.New(idb.WithLogger(log.New(w, "", log.Ltime|log.Lmicroseconds)))
	t.Cleanup(k.Close)
	return k
}

func waitState(t *testing.T, tx *idb.Transaction, want idb.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tx.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tx never reached state %v (got %v)", want, tx.State())
}

// waitStates 轮询直到所有事务同时处于期望状态（用于确定性地断言调度格局）。
func waitStates(t *testing.T, want map[*idb.Transaction]idb.State) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for tx, s := range want {
			if tx.State() != s {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	for tx, s := range want {
		t.Logf("tx state=%v want %v", tx.State(), s)
	}
	return false
}

// kindSentinel 让测试用 errors.Is 按错误 Kind 比较。
type kindSentinel idb.ErrKind

func errKind(k idb.ErrKind) error    { return kindSentinel(k) }
func (s kindSentinel) Error() string { return idb.ErrKind(s).String() }

// upgrade 打开到 v，在版本变更事务中执行 setup 后提交。
func upgrade(t *testing.T, k *idb.Kernel, name string, v int,
	setup func(c *idb.Connection, vt *idb.Transaction)) *idb.Connection {
	t.Helper()
	c, err := k.Open(name, v)
	if err != nil {
		t.Fatalf("open v=%d: %v", v, err)
	}
	vt := c.VersionChange()
	waitState(t, vt, idb.Running)
	if setup != nil {
		setup(c, vt)
	}
	if err := vt.Commit(); err != nil {
		t.Fatalf("vc commit: %v", err)
	}
	if err := vt.Wait(); err != nil {
		t.Fatalf("vc wait: %v", err)
	}
	return c
}

func mkStores(names ...string) func(c *idb.Connection, vt *idb.Transaction) {
	return func(c *idb.Connection, vt *idb.Transaction) {
		for _, n := range names {
			if err := c.CreateObjectStore(n); err != nil {
				panic(err)
			}
		}
	}
}

func putKV(t *testing.T, tx *idb.Transaction, store, key, val string, addOnly bool) {
	t.Helper()
	if err := idb.SyncPut(tx, store, key, val, addOnly); err != nil {
		t.Fatalf("put %s=%s: %v", key, val, err)
	}
}

func getKV(t *testing.T, tx *idb.Transaction, store, key, want string, wantExists bool) {
	t.Helper()
	v, ok, err := idb.SyncGet(tx, store, key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	if ok != wantExists || v != want {
		t.Fatalf("get %s = (%q,%v), want (%q,%v)", key, v, ok, want, wantExists)
	}
}

func rwTx(t *testing.T, c *idb.Connection, stores []string, fn func(*idb.Transaction)) {
	t.Helper()
	if err := idb.WithTx(c, idb.ReadWrite, stores, func(tx *idb.Transaction) error {
		fn(tx)
		return nil
	}); err != nil {
		t.Fatalf("rw tx: %v", err)
	}
}

func roTx(t *testing.T, c *idb.Connection, stores []string, fn func(*idb.Transaction)) {
	t.Helper()
	if err := idb.WithTx(c, idb.ReadOnly, stores, func(tx *idb.Transaction) error {
		fn(tx)
		return nil
	}); err != nil {
		t.Fatalf("ro tx: %v", err)
	}
}
