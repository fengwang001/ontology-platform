package idb_test

import (
	"testing"
	"time"

	"ontology/idb"
)

func errIsKind(err error, k idb.ErrKind) bool {
	e, ok := err.(*idb.Error)
	return ok && e.Kind == k
}

func TestVersionRelations(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 3, mkStores("s"))
	if k.Version("db") != 3 {
		t.Fatalf("version=%d", k.Version("db"))
	}
	c2b, err := k.Open("db", 3)
	if err != nil {
		t.Fatalf("equal: %v", err)
	}
	c2c, err := k.Open("db", 0)
	if err != nil {
		t.Fatalf("unspecified: %v", err)
	}
	if _, err := k.Open("db", 2); !errIsKind(err, idb.KindVersionTooLow) {
		t.Fatalf("want VersionTooLow, got %v", err)
	}

	c3, err := k.Open("db", 4)
	if err != nil {
		t.Fatal(err)
	}
	vt := c3.VersionChange()
	if vt.State() != idb.Queued {
		t.Fatalf("vc must be blocked, got %v", vt.State())
	}
	c.Close()
	c2b.Close()
	c2c.Close()
	waitState(t, vt, idb.Running)
	if err := vt.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = vt.Wait()
	if k.Version("db") != 4 {
		t.Fatalf("version=%d", k.Version("db"))
	}
}

func TestUpgradeBlockedAndNotified(t *testing.T) {
	k := testKernel(t)
	c1 := upgrade(t, k, "db", 1, mkStores("s"))
	notified := make(chan struct{}, 2)
	c1.OnVersionChange = func() { notified <- struct{}{} }

	c2, err := k.Open("db", 2)
	if err != nil {
		t.Fatal(err)
	}
	vt := c2.VersionChange()
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("not notified")
	}
	if vt.State() != idb.Queued {
		t.Fatalf("vc state=%v", vt.State())
	}
	c1.Close()
	select {
	case <-c1.Closed():
	case <-time.After(time.Second):
		t.Fatal("c1 not closed")
	}
	waitState(t, vt, idb.Running)
	if err := vt.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = vt.Wait()
	if k.Version("db") != 2 {
		t.Fatalf("version=%d", k.Version("db"))
	}
}

func TestQueuedOpenAfterNotification(t *testing.T) {
	k := testKernel(t)
	c1 := upgrade(t, k, "db", 1, mkStores("s"))
	release := make(chan struct{})
	notifyCh := make(chan struct{}, 4)
	c1.OnVersionChange = func() { notifyCh <- struct{}{} }

	type out struct {
		c   *idb.Connection
		err error
	}
	outs := make(chan out, 2)
	go func() {
		c, e := k.Open("db", 2)
		outs <- out{c, e}
	}()
	time.Sleep(30 * time.Millisecond)
	go func() {
		c, e := k.Open("db", 0)
		outs <- out{c, e}
	}()

	first := <-outs
	if first.err != nil {
		t.Fatal(first.err)
	}
	// 第一波通知来自升级打开；确保 c1 此时尚未关闭。
	select {
	case <-notifyCh:
	case <-time.After(time.Second):
		t.Fatal("c1 not notified")
	}
	vt := first.c.VersionChange()
	waitState(t, vt, idb.Queued)
	go func() { <-release; c1.Close() }()

	select {
	case o := <-outs:
		t.Fatalf("queued open returned early: %v", o)
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	waitState(t, vt, idb.Running)
	if err := vt.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = vt.Wait()

	select {
	case o := <-outs:
		if o.err != nil || o.c.Version() != 2 {
			t.Fatalf("queued open: err=%v v=%d", o.err, o.c.Version())
		}
	case <-time.After(time.Second):
		t.Fatal("queued open stuck")
	}
}

func TestReadOnlyOverlapParallel(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a", "b"))
	k.Pause()
	r1, _ := c.Transaction(idb.ReadOnly, []string{"a", "b"})
	r2, _ := c.Transaction(idb.ReadOnly, []string{"a"})
	if err := r1.Hold(); err != nil {
		t.Fatal(err)
	}
	if err := r2.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, r1, idb.Running)
	waitState(t, r2, idb.Running)
	_ = r1.Unhold()
	_ = r2.Unhold()
	_ = r1.Wait()
	_ = r2.Wait()
}

func TestReadWriteOverlapSerial(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	k.Pause()
	w1, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	r, _ := c.Transaction(idb.ReadOnly, []string{"a"})
	w2, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	if err := w1.Hold(); err != nil {
		t.Fatal(err)
	}
	if err := r.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, w1, idb.Running)
	if !waitStates(t, map[*idb.Transaction]idb.State{
		w1: idb.Running, r: idb.Queued, w2: idb.Queued,
	}) {
		t.Fatalf("overlap txns must queue behind running writer")
	}
	_ = r.Unhold()
	_ = w1.Unhold()
	_ = w1.Wait()
	waitState(t, w2, idb.Committed)
	_ = r.Wait()
}

func TestDisjointScopeParallel(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a", "b"))
	k.Pause()
	wa, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	wb, _ := c.Transaction(idb.ReadWrite, []string{"b"})
	if err := wa.Hold(); err != nil {
		t.Fatal(err)
	}
	if err := wb.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, wa, idb.Running)
	waitState(t, wb, idb.Running)
	_ = wa.Unhold()
	_ = wb.Unhold()
	_ = wa.Wait()
	_ = wb.Wait()
}

func TestNoOvertaking(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a", "b"))
	k.Pause()
	w1, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	w2, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	w3, _ := c.Transaction(idb.ReadWrite, []string{"b"})
	_ = w1.Hold()
	if err := w3.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, w1, idb.Running)
	waitState(t, w3, idb.Running)
	if w2.State() != idb.Queued {
		t.Fatalf("w2=%v", w2.State())
	}
	_ = w1.Unhold()
	_ = w1.Wait()
	waitState(t, w2, idb.Committed)
	_ = w3.Unhold()
	_ = w3.Wait()
}
