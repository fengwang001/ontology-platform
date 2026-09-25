package snapshot

import (
	"errors"
	"sort"
	"sync"

	"ontology/store"
)

// ErrSnapshotClosed 在快照关闭后仍尝试读取时返回。
var ErrSnapshotClosed = errors.New("snapshot closed")

// Order 决定导出键遍历顺序；一致性与顺序无关。
type Order int

const (
	Ascending Order = iota
	Descending
)

// Handle 是一个只读快照：固定版本水位，读取始终走该水位可见的旧值。
type Handle struct {
	st      *store.Store
	version uint64

	mu     sync.Mutex
	closed bool
	reads  int
	using  sync.WaitGroup // 正在进行的导出
}

// Take 在当前版本水位上建立快照，并通知 store 保留该水位可见的旧值。
func Take(st *store.Store) *Handle {
	v := st.Version()
	st.Begin(v)
	return &Handle{st: st, version: v}
}

func (h *Handle) Version() uint64 { return h.version }

// Acquire 在导出/续传开始时登记；快照已关闭（过期）则失败。
func (h *Handle) Acquire() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.using.Add(1)
	return true
}

// Release 表示一次导出结束。
func (h *Handle) Release() { h.using.Done() }

// Close 标记快照过期并等待进行中的导出结束；返回后保留旧值被释放。
func (h *Handle) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()
	h.using.Wait()
	h.st.End(h.version)
}

// Closed 报告快照是否已过期。
func (h *Handle) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// Get 读取快照版本可见值；导出期间该键被改写时仍返回快照时刻旧值。
func (h *Handle) Get(key string) ([]byte, bool, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, false, ErrSnapshotClosed
	}
	h.reads++
	h.mu.Unlock()
	v, ok := h.st.GetAt(key, h.version)
	return v, ok, nil
}

// Keys 按指定顺序返回快照可见的键集合。
func (h *Handle) Keys(order Order) ([]string, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrSnapshotClosed
	}
	h.mu.Unlock()
	keys := h.st.Keys(h.version)
	if order == Descending {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	}
	return keys, nil
}

// Retained 返回为该快照保留的旧值个数（与被改写键数成正比，与总键数无关）。
func (h *Handle) Retained() int { return h.st.Retained(h.version) }

// Reads 返回该快照自创建以来的读取次数。
func (h *Handle) Reads() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}
