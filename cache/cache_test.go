package cache

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/key"
)

func fr(status int, vis Visibility, ttl int64, vary []string, size int64, body string) *FetchResult {
	return &FetchResult{Status: status, Vis: vis, TTL: ttl, Vary: vary, Size: size, Body: []byte(body)}
}

func gq(method, path, subj string, scopes []string, head map[string]string) Request {
	return Request{Method: method, Path: path, Subject: subj, Scopes: scopes, Headers: head}
}

func hh(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func fetchConst(results ...*FetchResult) (FetchFunc, *int64) {
	var n int64
	i := 0
	return func(req Request) (*FetchResult, error) {
		atomic.AddInt64(&n, 1)
		r := results[0]
		if i < len(results) {
			r = results[i]
		}
		i++
		return r, nil
	}, &n
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

var errBoom = errors.New("boom")

// waitForWaiters blocks until the coalescing group for the canonical request
// has at least n followers parked on it, making concurrency tests independent
// of goroutine scheduling.
func waitForWaiters(c *Cache, path string, head map[string]string, n int) {
	ck := key.CoalescingKey(path, head).Encode()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		grp := c.groups[ck]
		got := 0
		if grp != nil {
			got = grp.waiters
		}
		c.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	panic("waiters never joined the coalescing group")
}

func TestFreshnessBoundary(t *testing.T) {
	c := New(1000, 4)
	f, n := fetchConst(fr(200, Public, 10, nil, 10, "x"))
	r, err := c.Get(gq("GET", "/a", "", nil, nil), 0, f)
	must(t, err)
	if r.Source != Miss {
		t.Fatalf("want Miss got %s", r.Source)
	}
	r, err = c.Get(gq("GET", "/a", "", nil, nil), 9, f)
	must(t, err)
	if r.Source != Hit {
		t.Fatalf("at 9 want Hit got %s", r.Source)
	}
	r, err = c.Get(gq("GET", "/a", "", nil, nil), 10, f)
	must(t, err)
	if r.Source != Miss {
		t.Fatalf("at expiry boundary want Miss got %s", r.Source)
	}
	if got := atomic.LoadInt64(n); got != 2 {
		t.Fatalf("fetches=%d want 2", got)
	}
}

func TestVaryMissingEqualsEmpty(t *testing.T) {
	c := New(1000, 4)
	f, _ := fetchConst(fr(200, Public, 100, []string{"x-token"}, 10, "v"))
	_, err := c.Get(gq("GET", "/a", "", nil, hh()), 0, f)
	must(t, err)
	r, err := c.Get(gq("GET", "/a", "", nil, hh("x-token", "")), 1, f)
	must(t, err)
	if r.Source != Hit {
		t.Fatalf("empty should equal missing, got %s", r.Source)
	}
	r, err = c.Get(gq("GET", "/a", "", nil, hh("x-token", "z")), 2, f)
	must(t, err)
	if r.Source != Miss {
		t.Fatalf("different value should miss, got %s", r.Source)
	}
}

func TestVaryStarNotStored(t *testing.T) {
	c := New(1000, 4)
	f, n := fetchConst(fr(200, Public, 100, []string{"*"}, 10, "v"))
	r, _ := c.Get(gq("GET", "/a", "", nil, nil), 0, f)
	if r.Source != Miss {
		t.Fatalf("got %s", r.Source)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 1, f)
	if r.Source != Miss {
		t.Fatalf("* must not be stored, got %s", r.Source)
	}
	if atomic.LoadInt64(n) != 2 {
		t.Fatalf("fetches=%d want 2", atomic.LoadInt64(n))
	}
}

func TestPublicSharedPrivateIsolated(t *testing.T) {
	c := New(1000, 8)
	fpub, npub := fetchConst(fr(200, Public, 100, nil, 10, "pub"))
	r, _ := c.Get(gq("GET", "/p", "", nil, nil), 0, fpub)
	if r.Source != Miss {
		t.Fatal(r.Source)
	}
	for _, subj := range []string{"u1", "u2", ""} {
		r, _ := c.Get(gq("GET", "/p", subj, nil, nil), 1, fpub)
		if r.Source != Hit {
			t.Fatalf("public subject=%q got %s", subj, r.Source)
		}
	}
	if got := atomic.LoadInt64(npub); got != 1 {
		t.Fatalf("public fetches=%d want 1", got)
	}

	fpriv, npriv := fetchConst(fr(200, Private, 100, nil, 10, "priv"))
	r, _ = c.Get(gq("GET", "/me", "u1", nil, nil), 2, fpriv)
	if r.Source != Miss {
		t.Fatal(r.Source)
	}
	r, _ = c.Get(gq("GET", "/me", "u1", nil, nil), 3, fpriv)
	if r.Source != Hit {
		t.Fatalf("owner u1 want Hit got %s", r.Source)
	}
	for _, subj := range []string{"u2", ""} {
		r, _ = c.Get(gq("GET", "/me", subj, nil, nil), 4, fpriv)
		if r.Source != Miss {
			t.Fatalf("private must not leak to %q, got %s", subj, r.Source)
		}
	}
	if got := atomic.LoadInt64(npriv); got != 3 {
		t.Fatalf("private fetches=%d want 3", got)
	}

	anon, n2 := fetchConst(fr(200, Private, 100, nil, 10, "x"))
	r, _ = c.Get(gq("GET", "/anon", "", nil, nil), 5, anon)
	if r.Source != Miss {
		t.Fatal(r.Source)
	}
	r, _ = c.Get(gq("GET", "/anon", "", nil, nil), 6, anon)
	if r.Source != Miss {
		t.Fatalf("anon private must not be stored, got %s", r.Source)
	}
	if atomic.LoadInt64(n2) != 2 {
		t.Fatal("anon private fetched twice")
	}
}

func TestConcurrentPublicCoalesce(t *testing.T) {
	c := New(1000, 8)
	release := make(chan struct{})
	var n int64
	f := func(req Request) (*FetchResult, error) {
		atomic.AddInt64(&n, 1)
		waitForWaiters(c, "/a", hh("authorization", "tok", "x", "1"), 7)
		<-release
		return fr(200, Public, 100, nil, 10, "ok"), nil
	}
	sources := make([]Source, 8)
	var wg sync.WaitGroup
	for i := range sources {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := c.Get(gq("GET", "/a", fmt.Sprintf("u%d", i), nil,
				hh("authorization", "tok", "x", "1")), 0, f)
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}
			sources[i] = r.Source
		}(i)
	}
	close(release)
	wg.Wait()
	if n != 1 {
		t.Fatalf("fetches=%d want 1", n)
	}
	var miss, shared int
	for _, s := range sources {
		switch s {
		case Miss:
			miss++
		case Shared:
			shared++
		}
	}
	if miss != 1 || shared != 7 {
		t.Fatalf("want 1 Miss 7 Shared, got %d/%d", miss, shared)
	}
}

func TestConcurrentPrivateEachFetches(t *testing.T) {
	c := New(1000, 8)
	release := make(chan struct{})
	var leader sync.Once
	var n int64
	f := func(req Request) (*FetchResult, error) {
		atomic.AddInt64(&n, 1)
		leader.Do(func() { waitForWaiters(c, "/a", hh("authorization", "t"), 4) })
		<-release
		return fr(200, Private, 100, nil, 10, "p"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := c.Get(gq("GET", "/a", "u", nil, hh("authorization", "t")), 0, f); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(release)
	wg.Wait()
	if n != 5 {
		t.Fatalf("private fetches=%d want 5", n)
	}
}

func TestConcurrentFetchErrorShared(t *testing.T) {
	c := New(1000, 8)
	release := make(chan struct{})
	var n int64
	f := func(req Request) (*FetchResult, error) {
		atomic.AddInt64(&n, 1)
		waitForWaiters(c, "/a", nil, 5)
		<-release
		return nil, errBoom
	}
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Get(gq("GET", "/a", "", nil, nil), 0, f)
		}(i)
	}
	close(release)
	wg.Wait()
	if n != 1 {
		t.Fatalf("fetches=%d want 1", n)
	}
	for i, e := range errs {
		if !errors.Is(e, errBoom) {
			t.Fatalf("waiter %d err=%v want boom", i, e)
		}
	}
}

func TestWriteInvalidation(t *testing.T) {
	c := New(1000, 8)
	fg, _ := fetchConst(fr(200, Public, 100, nil, 10, "g"))
	c.Get(gq("GET", "/a", "", nil, nil), 0, fg)

	fp, _ := fetchConst(fr(200, Public, 100, nil, 10, "w"))
	r, _ := c.Get(gq("POST", "/a", "", nil, nil), 1, fp)
	if r.Source != Bypass {
		t.Fatal(r.Source)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 2, fg)
	if r.Source != Miss {
		t.Fatalf("after success write want Miss got %s", r.Source)
	}

	c.Get(gq("GET", "/a", "", nil, nil), 3, fg)
	fp500, _ := fetchConst(fr(500, Public, 100, nil, 10, "w"))
	c.Get(gq("PUT", "/a", "", nil, nil), 4, fp500)
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 5, fg)
	if r.Source != Hit {
		t.Fatalf("500 must not invalidate, got %s", r.Source)
	}
	fp400, _ := fetchConst(fr(400, Public, 100, nil, 10, "w"))
	c.Get(gq("DELETE", "/a", "", nil, nil), 6, fp400)
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 7, fg)
	if r.Source != Hit {
		t.Fatalf("400 must not invalidate, got %s", r.Source)
	}
	fp399, _ := fetchConst(fr(399, Public, 100, nil, 10, "w"))
	c.Get(gq("PATCH", "/a", "", nil, nil), 8, fp399)
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 9, fg)
	if r.Source != Miss {
		t.Fatalf("399 must invalidate, got %s", r.Source)
	}
}
