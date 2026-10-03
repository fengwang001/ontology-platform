package registry

import (
	"errors"
	"testing"
)

func TestSpecExample(t *testing.T) {
	r, _ := New(100, 1000)
	p1, p2, w := []byte("p1"), []byte("p2"), []byte("w")
	run, err := r.Start(w, p1, 2, 0, 0)
	t.Logf("Start(w,p1,Reject,Fail,0)=(%d,%v) 无记录新建", run, err)
	if run != 1 || err != nil {
		t.Fatalf("first start = %d,%v", run, err)
	}
	if err := r.Finish(w, 1, stCompleted, 10); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := r.Start(w, p1, 2, 0, 50); !errors.Is(err, ErrReuse) {
		t.Fatalf("reject on completed err=%v", err)
	}
	if _, err := r.Start(w, p1, 1, 0, 50); !errors.Is(err, ErrReuse) {
		t.Fatalf("AllowFailedOnly on Completed err=%v", err)
	}
	if _, err := r.Start(w, p1, 2, 0, 109); !errors.Is(err, ErrReuse) {
		t.Fatalf("109 still alive err=%v", err)
	}
	run, err = r.Start(w, p1, 2, 0, 110)
	if run != 2 || err != nil {
		t.Fatalf("expired start = %d,%v want 2", run, err)
	}
	if n, _ := r.Count(110); n != 1 {
		t.Fatalf("Count=%d want 1", n)
	}
	if _, err := r.Start(w, p1, 0, 0, 120); !errors.Is(err, ErrRunning) {
		t.Fatalf("Running+Fail err=%v", err)
	}
	if _, err := r.Start(w, p2, 0, 2, 120); !errors.Is(err, ErrDenied) {
		t.Fatalf("p2 terminate err=%v", err)
	}
	run, err = r.Start(w, p1, 0, 2, 120)
	if run != 3 || err != nil {
		t.Fatalf("owner terminate = %d,%v want 3", run, err)
	}
	if err := r.Finish(w, 2, stFailed, 121); !errors.Is(err, ErrStale) {
		t.Fatalf("finish replaced run err=%v", err)
	}
}

func TestRejectionConsumesNothing(t *testing.T) {
	r, _ := New(10, 1)
	id, p := []byte("id"), []byte("p")
	if run, _ := r.Start(id, p, 2, 0, 5); run != 1 {
		t.Fatalf("run=%d want 1", run)
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Start(id, p, 0, 0, 6); !errors.Is(err, ErrRunning) {
			t.Fatalf("iter %d err=%v", i, err)
		}
	}
	if _, err := r.Start([]byte("other"), p, 2, 0, 6); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity err=%v (时钟应停在5，now=6合法)", err)
	}
	if run, err := r.Start(id, p, 2, 2, 6); run != 2 || err != nil {
		t.Fatalf("terminate replace = %d,%v want 2", run, err)
	}
}

func TestCapacityReplaceVsCreate(t *testing.T) {
	r, _ := New(1000, 1)
	id, other, p := []byte("id"), []byte("other"), []byte("p")
	r.Start(id, p, 2, 0, 0)
	if _, err := r.Start(other, p, 2, 0, 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("full err=%v", err)
	}
	if run, err := r.Start(id, p, 2, 2, 1); run != 2 || err != nil {
		t.Fatalf("terminate replace = %d,%v", run, err)
	}
	r.Finish(id, 2, stFailed, 2)
	if run, err := r.Start(id, p, 0, 0, 3); run != 3 || err != nil {
		t.Fatalf("reuse replace = %d,%v", run, err)
	}
	r.Start(id, p, 2, 2, 1003)
	r.Finish(id, 4, stCompleted, 1004)
	if _, err := r.Start(other, p, 2, 0, 2004); err != nil { // 恰等过期回收名额
		t.Fatalf("reclaim slot err=%v", err)
	}
}

func TestPermission(t *testing.T) {
	r, _ := New(1000, 10)
	id, owner, guest := []byte("id"), []byte("owner"), []byte("guest")
	run, _ := r.Start(id, owner, 2, 0, 0)
	if got, err := r.Start(id, guest, 2, 1, 1); got != run || err != nil {
		t.Fatalf("guest UseExisting = %d,%v", got, err)
	}
	if _, err := r.Start(id, guest, 0, 2, 2); !errors.Is(err, ErrDenied) {
		t.Fatalf("guest terminate err=%v", err)
	}
	r.Grant(guest)
	if got, err := r.Start(id, guest, 0, 2, 3); got != run+1 || err != nil {
		t.Fatalf("admin terminate = %d,%v", got, err)
	}
	r.Revoke(guest)
	if _, err := r.Start(id, []byte("stranger"), 2, 2, 4); !errors.Is(err, ErrDenied) {
		t.Fatalf("stranger err=%v", err)
	}
	if _, err := r.Start(id, guest, 2, 2, 4); err != nil { // run3 所有者是 guest
		t.Fatalf("owner terminate err=%v", err)
	}
}
