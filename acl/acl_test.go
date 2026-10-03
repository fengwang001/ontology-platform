package acl

import (
	"sync"
	"testing"
)

func TestCanTerminate(t *testing.T) {
	a := New()
	owner, admin, guest := []byte("o"), []byte("a"), []byte("g")
	if a.CanTerminate(guest, owner) {
		t.Fatal("guest 初始无权")
	}
	if !a.CanTerminate(owner, owner) {
		t.Fatal("所有者可终止自己的 run")
	}
	a.Grant(admin)
	if !a.CanTerminate(admin, owner) {
		t.Fatal("admin 可终止他人 run")
	}
	a.Revoke(admin)
	if a.CanTerminate(admin, owner) {
		t.Fatal("撤销后不再是 admin")
	}
	if a.CanTerminate(nil, owner) {
		t.Fatal("空主体无权")
	}
	a.Grant(nil)
	a.Revoke(nil)
}

func TestConcurrentGrantRevoke(t *testing.T) {
	a := New()
	p := []byte("p")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); a.Grant(p) }()
		go func() { defer wg.Done(); a.CanTerminate(p, []byte("other")) }()
	}
	wg.Wait()
	if !a.CanTerminate(p, []byte("other")) {
		t.Fatal("并发 Grant 后应为 admin")
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Revoke(p) }()
	}
	wg.Wait()
	if a.CanTerminate(p, []byte("other")) {
		t.Fatal("并发 Revoke 后应无权")
	}
}
