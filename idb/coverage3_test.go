package idb_test

import (
	"testing"
	"time"

	"ontology/idb"
)

func TestCloseCancelsQueuedAllowsRunning(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))

	k.Pause()
	running, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	if err := running.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, running, idb.Running)

	queued, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	time.Sleep(30 * time.Millisecond)
	if queued.State() != idb.Queued {
		t.Fatalf("queued=%v", queued.State())
	}

	c.Close()
	if _, err := c.Transaction(idb.ReadOnly, []string{"a"}); !errIsKind(err, idb.KindInvalidState) {
		t.Fatalf("create during closing: %v", err)
	}
	waitState(t, queued, idb.Aborted)

	select {
	case <-c.Closed():
		t.Fatal("closed before running tx finished")
	case <-time.After(30 * time.Millisecond):
	}
	if err := running.Unhold(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Closed():
	case <-time.After(time.Second):
		t.Fatal("connection did not close after running tx finished")
	}
}

func TestVersionChangeAbortRestoresStores(t *testing.T) {
	k := testKernel(t)
	c1 := upgrade(t, k, "db", 1, mkStores("keep", "drop"))
	rwTx(t, c1, []string{"keep"}, func(tx *idb.Transaction) {
		putKV(t, tx, "keep", "k", "v", false)
	})

	c1.OnVersionChange = func() { c1.Close() }
	c2, err := k.Open("db", 2)
	if err != nil {
		t.Fatal(err)
	}
	vt := c2.VersionChange()
	waitState(t, vt, idb.Running)

	if err := c2.CreateObjectStore("added"); err != nil {
		t.Fatal(err)
	}
	if err := c2.DeleteObjectStore("drop"); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Transaction(idb.ReadOnly, []string{"drop"}); err == nil {
		t.Fatal("deleted store must be inaccessible in same vc")
	}
	if err := vt.Abort(); err != nil {
		t.Fatal(err)
	}
	_ = vt.Wait()
	if k.Version("db") != 1 {
		t.Fatalf("version=%d, want 1", k.Version("db"))
	}

	c3, err := k.Open("db", 0)
	if err != nil {
		t.Fatal(err)
	}
	roTx(t, c3, []string{"keep"}, func(tx *idb.Transaction) {
		getKV(t, tx, "keep", "k", "v", true)
	})
	if _, err := c3.Transaction(idb.ReadOnly, []string{"drop"}); err != nil {
		t.Fatalf("drop store should be restored: %v", err)
	}
	if _, err := c3.Transaction(idb.ReadOnly, []string{"added"}); err == nil {
		t.Fatal("added store should not exist after vc abort")
	}
}

func TestAddOnlyConflictAndMissingKey(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "k", "v", true)
	})

	// 可忽略的键冲突：事务存活，旧值保留。
	err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		failed := make(chan bool, 1)
		_, e := tx.PutEx("a", []byte("k"), []byte("v2"), true,
			idb.WithIgnorable(),
			idb.WithOnError(func(e *idb.Error) {
				if !errIsKind(e, idb.KindConstraint) {
					t.Fatalf("err=%v", e)
				}
				failed <- true
			}),
			idb.WithOnSuccess(func(*idb.RequestResult) { failed <- false }))
		if e != nil {
			return e
		}
		if !<-failed {
			t.Fatal("add-only on existing key must conflict")
		}
		putKV(t, tx, "a", "other", "z", false)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "k", "v", true)
		getKV(t, tx, "a", "other", "z", true)
		getKV(t, tx, "a", "missing", "", false)
	})

	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "k", "v3", false) // 覆盖
	})
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "k", "v3", true)
	})
}

func TestDeleteDatabaseVersionZero(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "k", "v", false)
	})

	notified := make(chan struct{}, 1)
	c.OnVersionChange = func() { notified <- struct{}{} }

	done := make(chan error, 1)
	go func() { done <- k.DeleteDatabase("db") }()
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("not notified of delete")
	}
	select {
	case <-done:
		t.Fatal("delete finished while connection open")
	case <-time.After(50 * time.Millisecond):
	}
	c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("delete stuck")
	}
	if k.Version("db") != 0 {
		t.Fatalf("after delete version=%d, want 0", k.Version("db"))
	}
	upgrade(t, k, "db", 1, mkStores("b"))
}

func TestSelfReadsUncommittedWrites(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		putKV(t, tx, "a", "k", "uncommitted", false)
		getKV(t, tx, "a", "k", "uncommitted", true)
		if e := idb.SyncDelete(tx, "a", "k"); e != nil {
			t.Fatal(e)
		}
		getKV(t, tx, "a", "k", "", false)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "k", "", false) // 中止回滚后不存在
	})
}
