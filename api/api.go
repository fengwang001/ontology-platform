// Package api 对外门面：New/Put/Del/Snapshot/Next/Resume/View/SelfCheck。
// 依赖 export。状态在进程内存，全部方法并发安全。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/export"
	"ontology/snap"
)

// 可判定哨兵错误，四者互不相同。
var (
	ErrBadChunk   = errors.New("api: chunk size must be > 0")
	ErrEmptyKey   = errors.New("api: empty key")
	ErrNoSnapshot = errors.New("api: no snapshot started")
	// ErrFinished 快照已导出完毕（done = true 之后再 Next/Resume）。
	ErrFinished = export.ErrFinished
)

// Entry 是导出的键值对。
type Entry = snap.Entry

// Handle 是 Snapshot 返回的快照句柄（内部为点时刻拷贝）。
type Handle struct{ snap *snap.Snapshot }

// API 是实时状态 + 当前快照导出器。
type API struct {
	mu   sync.RWMutex
	live map[string]int64
	size int
	exp  *export.Exporter
}

// New 建实例；chunkSize <= 0 返回 ErrBadChunk。
func New(chunkSize int) (*API, error) {
	if chunkSize <= 0 {
		return nil, ErrBadChunk
	}
	return &API{live: map[string]int64{}, size: chunkSize}, nil
}

// mutate 校验 key 后在写锁下改实时状态；空 key 返回 ErrEmptyKey 且状态不变。
func (a *API) mutate(key string, fn func()) error {
	if key == "" {
		return ErrEmptyKey
	}
	a.mu.Lock()
	fn()
	a.mu.Unlock()
	return nil
}

// Put 写入/覆盖；空 key 返回 ErrEmptyKey 且状态不变。
func (a *API) Put(key string, val int64) error { return a.mutate(key, func() { a.live[key] = val }) }

// Del 删除；空 key 返回 ErrEmptyKey 且状态不变。
func (a *API) Del(key string) error { return a.mutate(key, func() { delete(a.live, key) }) }

// Snapshot 捕获点时刻副本并开启一轮导出；此后的 Put/Del 不影响本轮。
func (a *API) Snapshot() Handle {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := snap.NewSnapshot(a.live)
	a.exp = export.NewExporter(s, a.size)
	return Handle{snap: s}
}

// Next 导下一块；未 Snapshot 返回 ErrNoSnapshot，完毕后返回 ErrFinished。
func (a *API) Next(cursor string) ([]Entry, string, bool, error) {
	a.mu.RLock()
	exp := a.exp
	a.mu.RUnlock()
	if exp == nil {
		return nil, "", false, ErrNoSnapshot
	}
	return exp.Next(cursor)
}

// Resume 等价 Next：传回上次的 newCursor 续导，位点键本身绝不重复。
func (a *API) Resume(cursor string) ([]Entry, string, bool, error) {
	return a.Next(cursor)
}

// View 返回实时状态的有序视图（拷贝）。
func (a *API) View() []Entry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return snap.NewSnapshot(a.live).All()
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	a, err := New(2)
	if err != nil {
		return err
	}
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := a.Put(k, int64(i+1)); err != nil {
			return err
		}
	}
	a.Snapshot()
	if err := a.Put("d", 99); err != nil { // 快照后写，不应影响本轮导出
		return err
	}
	var got []Entry
	cur := ""
	for {
		blk, nc, done, err := a.Next(cur)
		if err != nil {
			return err
		}
		got = append(got, blk...)
		cur = nc
		if done {
			break
		}
	}
	// 不变量 1+3：拼接 == 快照时刻全量（d 必须是 4）
	want := []Entry{{Key: "a", Val: 1}, {Key: "b", Val: 2}, {Key: "c", Val: 3},
		{Key: "d", Val: 4}, {Key: "e", Val: 5}, {Key: "f", Val: 6}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("selfcheck: concat=%v want %v", got, want)
	}
	for i := 1; i < len(got); i++ { // 不变量 2：不重复且不倒退
		if got[i-1].Key >= got[i].Key {
			return fmt.Errorf("selfcheck: order/dup violated at %q", got[i].Key)
		}
	}
	// 不变量 4：被拒操作不留痕
	before := fmt.Sprint(a.View())
	fresh, _ := New(1)
	_, _, _, errFin := a.Next(cur)
	_, _, _, errNo := fresh.Next("")
	_, errBad := New(0)
	if a.Put("", 1) != ErrEmptyKey || a.Del("") != ErrEmptyKey ||
		errFin != ErrFinished || errNo != ErrNoSnapshot || errBad != ErrBadChunk {
		return errors.New("selfcheck: rejection mismatch")
	}
	if fmt.Sprint(a.View()) != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
