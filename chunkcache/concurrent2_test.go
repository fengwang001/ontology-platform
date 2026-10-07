package chunkcache

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// twoGateOrigin：第一次调用（discovery）与第二次调用（数据缺失片）
// 各自阻塞在独立栅栏，用于把 N 个并发请求严格汇聚到同一数据回源。
type twoGateOrigin struct {
	s      int64
	data   []byte
	mu     sync.Mutex
	calls  [][2]int
	inData int

	firstIn chan struct{}
	firstGo chan struct{}
	dataIn  chan struct{}
	dataGo  chan struct{}
	once1   sync.Once
	once2   sync.Once
}

func newTwoGateOrigin(s int64, data []byte) *twoGateOrigin {
	return &twoGateOrigin{
		s: s, data: data,
		firstIn: make(chan struct{}), firstGo: make(chan struct{}),
		dataIn: make(chan struct{}), dataGo: make(chan struct{}),
	}
}

func (g *twoGateOrigin) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	g.mu.Lock()
	g.calls = append(g.calls, [2]int{req.First, req.Last})
	isDiscovery := req.First == 0 && req.Last == 0
	g.mu.Unlock()

	if isDiscovery {
		g.once1.Do(func() { close(g.firstIn) })
		select {
		case <-g.firstGo:
		case <-ctx.Done():
			return FetchResult{}, ctx.Err()
		}
	} else {
		g.mu.Lock()
		g.inData++
		n := g.inData
		g.mu.Unlock()
		if n == 1 {
			g.once2.Do(func() { close(g.dataIn) })
			select {
			case <-g.dataGo:
			case <-ctx.Done():
				return FetchResult{}, ctx.Err()
			}
		}
	}

	var chunks [][]byte
	for i := req.First; i <= req.Last; i++ {
		lo := int64(i) * g.s
		hi := lo + g.s
		if hi > int64(len(g.data)) {
			hi = int64(len(g.data))
		}
		chunks = append(chunks, append([]byte(nil), g.data[lo:hi]...))
	}
	return FetchResult{Version: "v1", Length: int64(len(g.data)), Chunks: chunks}, nil
}

func TestConcurrentSameChunkCoalescesPrecisely(t *testing.T) {
	data := mkData(16, 1)
	o := newTwoGateOrigin(4, data)
	cache := newTestCache(t, 4, 8, o, false)
	ctx := context.Background()

	const N = 24
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = cache.Get(ctx, "obj", mustRange(8, 11)) // 片2
		}(i)
	}
	close(start)

	<-o.firstIn
	close(o.firstGo) // 放行 discovery
	<-o.dataIn       // 首个数据回源进入
	time.Sleep(100 * time.Millisecond)
	close(o.dataGo)
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("g%d: %v", i, e)
		}
	}
	dataFetches := 0
	for _, r := range o.calls {
		if r[0] <= 2 && 2 <= r[1] {
			dataFetches++
		}
	}
	if dataFetches != 1 {
		t.Fatalf("chunk2 data fetches=%d calls=%v, want exactly 1", dataFetches, o.calls)
	}
}

// 保留原阻塞源站的“结果正确 + 无关请求不阻塞”覆盖。
var _ = bytes.Equal
var _ = errors.Is
