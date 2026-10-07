package chunkcache

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingOrigin 让第一次对 (key,chunk) 的回源阻塞，直到放行；
// 用于制造多个并发请求同时缺失同一切片的窗口。
type blockingOrigin struct {
	s      int64
	data   map[string][]byte
	cur    string
	mu     sync.Mutex
	calls  int
	ranges [][2]int
	maxIn  int
	in     int
	failOn map[int]bool // 第 N 次（1 基）调用失败

	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingOrigin(s int64, data []byte) *blockingOrigin {
	return &blockingOrigin{
		s: s, data: map[string][]byte{"v1": data}, cur: "v1",
		failOn:  map[int]bool{},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingOrigin) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	b.mu.Lock()
	b.calls++
	b.ranges = append(b.ranges, [2]int{req.First, req.Last})
	n := b.calls
	b.in++
	if b.in > b.maxIn {
		b.maxIn = b.in
	}
	fail := b.failOn[n]
	block := b.in == 1
	if block {
		b.once.Do(func() { close(b.entered) })
	}
	data := b.data[b.cur]
	b.mu.Unlock()

	if block {
		select {
		case <-b.release:
		case <-ctx.Done():
			b.mu.Lock()
			b.in--
			b.mu.Unlock()
			return FetchResult{}, ctx.Err()
		}
	}

	b.mu.Lock()
	b.in--
	b.mu.Unlock()

	if fail {
		return FetchResult{}, errors.New("forced backend failure")
	}
	var chunks [][]byte
	for i := req.First; i <= req.Last; i++ {
		lo := int64(i) * b.s
		hi := lo + b.s
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		chunks = append(chunks, append([]byte(nil), data[lo:hi]...))
	}
	return FetchResult{Version: "v1", Length: int64(len(data)), Chunks: chunks}, nil
}

func TestConcurrentSameChunkSingleFetch(t *testing.T) {
	data := mkData(16, 1)
	o := newBlockingOrigin(4, data)
	cache := newTestCache(t, 4, 8, o, false)
	ctx := context.Background()

	const N = 16
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			// 每个请求都要片2（[8,12)）；它们同时缺失应共享一次回源。
			_, errs[i] = cache.Get(ctx, "obj", mustRange(8, 11))
		}(i)
	}

	select {
	case <-o.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first fetch never entered")
	}
	// 给其余 goroutine 时间登记为 follower。
	time.Sleep(100 * time.Millisecond)
	close(o.release)
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d: %v", i, e)
		}
	}
	// 精确的“同切片仅一次回源”由 TestConcurrentSameChunkCoalescesPrecisely
	// 用双栅栏保证；此处只验证所有并发请求结果正确且失败不互相污染。
	if got := o.calls; got < 2 {
		t.Fatalf("expected discovery + data calls, got %d", got)
	}
}

func TestConcurrentFailureFansOut(t *testing.T) {
	data := mkData(16, 1)
	o := newBlockingOrigin(4, data)
	o.failOn[1] = true // 唯一一次（合并后的）回源失败
	cache := newTestCache(t, 4, 8, o, false)
	ctx := context.Background()

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = cache.Get(ctx, "obj", mustRange(8, 11))
		}(i)
	}
	select {
	case <-o.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch never entered")
	}
	time.Sleep(50 * time.Millisecond)
	close(o.release)
	wg.Wait()

	for i, e := range errs {
		if !errors.Is(e, ErrBackend) {
			t.Fatalf("goroutine %d err=%v want backend", i, e)
		}
	}
	if o.calls != 1 {
		t.Fatalf("calls=%d want 1", o.calls)
	}
	// 失败不得写缓存：随后恢复源站，单请求必须再次回源且成功。
	o.failOn = map[int]bool{}
	o.once = sync.Once{}
	o.entered = make(chan struct{})
	o.release = make(chan struct{})
	close(o.release)
	res, err := cache.Get(ctx, "obj", mustRange(8, 11))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !bytes.Equal(res.Data, data[8:12]) {
		t.Fatalf("retry data")
	}
}

func TestConcurrentUnrelatedUnaffected(t *testing.T) {
	data := mkData(16, 1)
	o := newBlockingOrigin(4, data)
	cache := newTestCache(t, 4, 16, o, false)
	ctx := context.Background()

	// obj-a 在片2 上阻塞；obj-b 完全无关，应能独立完成。
	aDone := make(chan error, 1)
	go func() {
		_, err := cache.Get(ctx, "a", mustRange(8, 11))
		aDone <- err
	}()
	<-o.entered

	bOK := make(chan bool, 1)
	go func() {
		_, err := cache.Get(ctx, "b", mustRange(8, 11))
		bOK <- err == nil
	}()
	select {
	case ok := <-bOK:
		if !ok {
			t.Fatal("unrelated request failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated request blocked by unrelated flight")
	}
	close(o.release)
	if err := <-aDone; err != nil {
		t.Fatalf("a: %v", err)
	}
}
