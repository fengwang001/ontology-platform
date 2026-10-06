package ontology

import (
	"sync"
	"testing"
)

type qresult struct {
	r   Result
	err error
}

// 同源前缀的并发未命中只发起一次上游，所有等待者共享结果。
func TestConcurrentCoalescing(t *testing.T) {
	clk := newFakeClock()
	up := newGatedUpstream([]Answer{{Kind: KindRecords, Records: []byte("shared"), TTL: 60, ScopePrefix: 24}})
	c := NewCache(10, clk, up)

	base := Query{Name: "c", Rrtype: 1, Family: FamilyV4, SrcPrefix: 24}
	q1 := base
	q1.Client = v4(192, 0, 2, 10)
	q2 := base
	q2.Client = v4(192, 0, 2, 20)
	q3 := base
	q3.Client = v4(192, 0, 2, 30)

	outs := make([]chan qresult, 3)
	var wg sync.WaitGroup
	for i, q := range []Query{q1, q2, q3} {
		ch := make(chan qresult, 1)
		outs[i] = ch
		wg.Add(1)
		go func(q Query, ch chan qresult) {
			defer wg.Done()
			r, err := c.Query(q)
			ch <- qresult{r, err}
		}(q, ch)
	}
	waitStarted(t, up.started, 1)
	assertNoMore(t, up.started)
	up.releaseAll()
	wg.Wait()

	for _, ch := range outs {
		res := <-ch
		if res.err != nil || string(res.r.Records) != "shared" {
			t.Fatalf("waiter got %+v/%v", res.r, res.err)
		}
	}
	if up.calls != 1 {
		t.Fatalf("want exactly 1 upstream call, got %d", up.calls)
	}

	// 后续同源查询命中缓存，无上游
	mustQuery(t, c, q2)
	if up.calls != 1 {
		t.Fatalf("post-flight query must hit cache, calls=%d", up.calls)
	}
}

// 不同源前缀长度即便是同一地址，也不合并。
func TestConcurrentDifferentPrefixNotCoalesced(t *testing.T) {
	clk := newFakeClock()
	up := newGatedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("a"), TTL: 60, ScopePrefix: 16},
		{Kind: KindRecords, Records: []byte("b"), TTL: 60, ScopePrefix: 24},
	})
	c := NewCache(10, clk, up)

	outs := make([]chan qresult, 2)
	var wg sync.WaitGroup
	for i, q := range []Query{
		{Name: "d", Rrtype: 1, Family: FamilyV4, Client: v4(192, 0, 2, 5), SrcPrefix: 16},
		{Name: "d", Rrtype: 1, Family: FamilyV4, Client: v4(192, 0, 2, 5), SrcPrefix: 24},
	} {
		ch := make(chan qresult, 1)
		outs[i] = ch
		wg.Add(1)
		go func(q Query, ch chan qresult) {
			defer wg.Done()
			r, err := c.Query(q)
			ch <- qresult{r, err}
		}(q, ch)
	}
	waitStarted(t, up.started, 2)
	up.releaseAll()
	wg.Wait()
	for _, ch := range outs {
		if res := <-ch; res.err != nil {
			t.Fatal(res.err)
		}
	}
}

// 上游范围比源前缀长：范围内等待者共享；范围外等待者独立重新查询。
// TTL>0 时，重查直接命中按源前缀写入的缓存条目，因此不产生第二次上游调用；
// 该等待者仍必须完整重走查询路径（由“结果一致且无死锁”间接验证），
// 而 TTL=0 场景下必须产生属于该等待者的第二次上游调用。
func TestConcurrentUncoveredRequery(t *testing.T) {
	t.Run("ttl_positive_hits_cached_source_prefix", func(t *testing.T) {
		clk := newFakeClock()
		up := newGatedUpstream([]Answer{
			{Kind: KindRecords, Records: []byte("inside"), TTL: 60, ScopePrefix: 24},
		})
		c := NewCache(10, clk, up)

		inside := Query{Name: "u", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 5, 5), SrcPrefix: 16}
		outside := Query{Name: "u", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 9, 9), SrcPrefix: 16}

		chIn := make(chan qresult, 1)
		chOut := make(chan qresult, 1)
		var wg sync.WaitGroup
		startWaiter := func(q Query, ch chan qresult) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := c.Query(q)
				ch <- qresult{r, err}
			}()
		}
		startWaiter(inside, chIn)
		waitStarted(t, up.started, 1) // 保证范围内客户端是 flight 发起者
		startWaiter(outside, chOut)
		fkPos := prefixKey{name: nameKey{"u", 1}, family: FamilyV4,
			prefix: string(v4(172, 16, 0, 0)), bits: 16}
		waitFlightWaiters(t, c, fkPos, 2)
		assertNoMore(t, up.started)
		up.releaseAll()
		wg.Wait()

		for _, ch := range []chan qresult{chIn, chOut} {
			res := <-ch
			if res.err != nil || string(res.r.Records) != "inside" {
				t.Fatalf("both must resolve through requery/cache, got %+v/%v", res.r, res.err)
			}
		}
		if up.calls != 1 {
			t.Fatalf("cached /16 entry should serve uncovered waiter, calls=%d", up.calls)
		}
	})

	t.Run("ttl_zero_forces_independent_upstream", func(t *testing.T) {
		clk := newFakeClock()
		up := newGatedUpstream([]Answer{
			{Kind: KindRecords, Records: []byte("nope-not-cached"), TTL: 0, ScopePrefix: 24},
			{Kind: KindRecords, Records: []byte("requeried"), TTL: 60, ScopePrefix: 16},
		})
		c := NewCache(10, clk, up)

		inside := Query{Name: "u0", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 5, 5), SrcPrefix: 16}
		outside := Query{Name: "u0", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 9, 9), SrcPrefix: 16}
		chIn := make(chan qresult, 1)
		chOut := make(chan qresult, 1)
		var wg sync.WaitGroup
		startWaiter := func(q Query, ch chan qresult) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := c.Query(q)
				ch <- qresult{r, err}
			}()
		}
		startWaiter(inside, chIn)
		waitStarted(t, up.started, 1) // 保证范围内客户端是 flight 发起者
		startWaiter(outside, chOut)
		fk0 := prefixKey{name: nameKey{"u0", 1}, family: FamilyV4,
			prefix: string(v4(172, 16, 0, 0)), bits: 16}
		waitFlightWaiters(t, c, fk0, 2) // 确认范围外等待者已并入在途组
		assertNoMore(t, up.started)
		up.releaseAll() // 第一次应答释放；范围外等待者应立即发起第二次上游
		waitStarted(t, up.started, 1)
		up.releaseAll() // 幂等：已打开，第二次调用直接通过
		wg.Wait()

		if res := <-chIn; res.err != nil || string(res.r.Records) != "nope-not-cached" {
			t.Fatalf("inside waiter got %+v/%v", res.r, res.err)
		}
		if res := <-chOut; res.err != nil || string(res.r.Records) != "requeried" {
			t.Fatalf("outside waiter must independently re-query, got %+v/%v", res.r, res.err)
		}
		if up.calls != 2 {
			t.Fatalf("want 2 upstream calls for uncovered waiter with ttl=0, got %d", up.calls)
		}
	})
}

// 上游失败时所有等待者收到上游失败，且不写入缓存（随后重试会再次上游）。
func TestConcurrentUpstreamFailure(t *testing.T) {
	clk := newFakeClock()
	up := newGatedUpstream(nil)
	c := NewCache(10, clk, up)

	q := Query{Name: "f", Rrtype: 1, Family: FamilyV4, Client: v4(5, 5, 5, 5), SrcPrefix: 24}
	outs := make([]chan qresult, 3)
	var wg sync.WaitGroup
	for i := range outs {
		ch := make(chan qresult, 1)
		outs[i] = ch
		wg.Add(1)
		go func(ch chan qresult) {
			defer wg.Done()
			_, err := c.Query(q)
			ch <- qresult{err: err}
		}(ch)
	}
	waitStarted(t, up.started, 1)
	assertNoMore(t, up.started)
	up.releaseAll()
	wg.Wait()
	if up.calls != 1 {
		t.Fatalf("want coalesced single upstream call, got %d", up.calls)
	}
	for _, ch := range outs {
		if res := <-ch; res.err != ErrUpstreamFailure {
			t.Fatalf("want upstream failure, got %v", res.err)
		}
	}
	// 失败不缓存：随后查询再次触发上游（门闩已打开，直接失败）
	if _, err := c.Query(q); err != ErrUpstreamFailure {
		t.Fatalf("retry still upstream failure, got %v", err)
	}
	if up.calls != 2 {
		t.Fatalf("failure must not be cached: want 2 total calls, got %d", up.calls)
	}
}
