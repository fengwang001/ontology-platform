package idb_test

import (
	"testing"
	"time"

	"ontology/idb"
)

func TestReadOnlySnapshotIsolation(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "k", "1", false)
	})

	k.Pause()
	snap, _ := c.Transaction(idb.ReadOnly, []string{"a"})
	if err := snap.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, snap, idb.Running)
	getKV(t, snap, "a", "k", "1", true)

	// 第二个连接上的重叠读写事务必须等只读快照结束；等待期间快照读不变。
	c2, err := k.Open("db", 0)
	if err != nil {
		t.Fatal(err)
	}
	k.Pause()
	writer, err := c2.Transaction(idb.ReadWrite, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	time.Sleep(40 * time.Millisecond)
	if writer.State() != idb.Queued {
		t.Fatalf("writer=%v, must wait behind snapshot", writer.State())
	}
	getKV(t, snap, "a", "k", "1", true)

	_ = snap.Unhold()
	_ = snap.Wait()
	waitState(t, writer, idb.Running)
	putKV(t, writer, "a", "k", "2", false)
	_ = writer.Unhold()
	if err := writer.Wait(); err != nil {
		t.Fatal(err)
	}
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "k", "2", true)
	})
}

func TestAutoCommitTiming(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	k.Pause()
	tx, err := c.Transaction(idb.ReadWrite, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.PutEx("a", []byte("k"), []byte("v"), false,
		idb.WithOnSuccess(func(*idb.RequestResult) {}))
	if err != nil {
		t.Fatal(err)
	}
	k.Resume()
	done := make(chan error, 1)
	go func() { done <- tx.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("auto commit did not happen")
	}
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "k", "v", true)
	})
}

func TestIgnorableErrorDoesNotAbort(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "existing", "x", false)
	})

	var sawConflict bool
	conflictSeen := make(chan struct{}, 1)
	err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		_, e := tx.PutEx("a", []byte("existing"), []byte("y"), true,
			idb.WithIgnorable(),
			idb.WithOnError(func(err *idb.Error) {
				if !errIsKind(err, idb.KindConstraint) {
					t.Fatalf("err=%v", err)
				}
				sawConflict = true
				conflictSeen <- struct{}{}
			}))
		if e != nil {
			return e
		}
		<-conflictSeen // 等待可忽略失败投递完毕，再继续发请求
		putKV(t, tx, "a", "other", "z", false)
		return nil
	})
	if err != nil {
		t.Fatalf("tx should survive ignorable error: %v", err)
	}
	if !sawConflict {
		t.Fatal("constraint error not observed")
	}
	roTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		getKV(t, tx, "a", "existing", "x", true)
		getKV(t, tx, "a", "other", "z", true)
	})
}

func TestNonIgnorableErrorAborts(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	rwTx(t, c, []string{"a"}, func(tx *idb.Transaction) {
		putKV(t, tx, "a", "existing", "x", false)
	})
	aborted := make(chan struct{}, 1)
	err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		tx.OnAbort = func(*idb.Error) { close(aborted) }
		_, _ = tx.PutEx("a", []byte("existing"), []byte("y"), true,
			idb.WithOnError(func(*idb.Error) {}),
			idb.WithOnSuccess(func(*idb.RequestResult) { t.Fatal("should fail") }))
		return nil
	})
	if err == nil {
		t.Fatal("expected abort from non-ignorable constraint error")
	}
	if !errIsKind(err, idb.KindConstraint) {
		t.Fatalf("abort cause=%v, want Constraint", err)
	}
	select {
	case <-aborted:
	default:
	}
}
