package chunkcache

import (
	"context"
	"fmt"
	"sync"
)

// mockOrigin 是测试源站：保存多版本对象数据，按下标区间切片返回。
//
//   - versions: 版本标识 -> 完整字节内容
//   - order:    版本的时间先后（用于切换 current）
//   - failMask: 命中 (first,last) 区间的调用返回错误（-1 表示全失败）
//   - block:    每次回源进入时关闭、离开时释放，供并发测试栅栏
type mockOrigin struct {
	mu       sync.Mutex
	s        int64
	versions map[string][]byte
	current  string

	calls     []mockCall
	inflight  int
	maxFlight int

	failEvery int // >0 时每 N 次调用失败一次
	callCount int

	useGate  bool // true 时第一次在途回源阻塞于 release，供并发测试
	gateOnce sync.Once
	gate     chan struct{}
	release  chan struct{}
}

type mockCall struct {
	key             string
	first, last     int
	expectedVersion string
	gotVersion      string
}

func newMockOrigin(s int64, versions map[string][]byte, current string) *mockOrigin {
	return &mockOrigin{
		s:        s,
		versions: versions,
		current:  current,
		gate:     make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (m *mockOrigin) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	m.mu.Lock()
	m.callCount++
	if m.failEvery > 0 && m.callCount%m.failEvery == 0 {
		m.mu.Unlock()
		return FetchResult{}, fmt.Errorf("mock origin forced failure call=%d", m.callCount)
	}
	data := m.versions[m.current]
	ver := m.current
	m.calls = append(m.calls, mockCall{
		key: req.Key, first: req.First, last: req.Last,
		expectedVersion: req.ExpectedVersion, gotVersion: ver,
	})
	m.inflight++
	if m.inflight > m.maxFlight {
		m.maxFlight = m.inflight
	}
	useGate := m.useGate && m.inflight == 1
	if useGate {
		m.gateOnce.Do(func() { close(m.gate) })
	}
	m.mu.Unlock()

	if useGate {
		select {
		case <-m.release:
		case <-ctx.Done():
			m.mu.Lock()
			m.inflight--
			m.mu.Unlock()
			return FetchResult{}, ctx.Err()
		}
	}

	m.mu.Lock()
	m.inflight--
	m.mu.Unlock()

	// 按缓存切片约定切分。
	chunks := make([][]byte, 0, req.Last-req.First+1)
	for i := req.First; i <= req.Last; i++ {
		lo := int64(i) * m.s
		if lo >= int64(len(data)) {
			if len(data) == 0 {
				return FetchResult{Version: ver, Length: 0, Chunks: nil}, nil
			}
			// 超出当前版本长度的切片返回空片，同时携带真实版本与总长度，
			// 使调用方据 Length 重判范围（空片不会在重判后被读取）。
			chunks = append(chunks, []byte{})
			continue
		}
		hi := lo + m.s
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		chunks = append(chunks, append([]byte(nil), data[lo:hi]...))
	}
	return FetchResult{Version: ver, Length: int64(len(data)), Chunks: chunks}, nil
}

func (m *mockOrigin) setCurrent(v string) {
	m.mu.Lock()
	m.current = v
	m.mu.Unlock()
}

func (m *mockOrigin) snapshotCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockOrigin) callCountNow() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}
