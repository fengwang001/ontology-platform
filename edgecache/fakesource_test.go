package edgecache

import (
	"context"
	"errors"
	"sync"
)

// fakeObj 是假源站中的一个对象版本。
type fakeObj struct {
	version string
	data    []byte
}

// fetchRecord 记录一次回源调用，用于断言回源次数与区间合并。
type fetchRecord struct {
	key         string
	first, last int64
	expect      string
}

// fakeSource 是可控的 Source 实现：支持改版本、下次回源前翻版本、
// 注入失败、以及用闸门阻塞回源以构造并发时序。
type fakeSource struct {
	s int64

	mu            sync.Mutex
	objs          map[string]fakeObj
	calls         int
	records       []fetchRecord
	failErr       error
	failRemaining int
	flips         map[string][]fakeObj     // 每个键的版本翻转队列，每次回源弹出队首
	gate          chan struct{}            // 非 nil 时每次回源前先取一个令牌
	gates         map[string]chan struct{} // 按对象键的闸门，优先于 gate
	started       chan struct{}            // 非 nil 时每次回源开始时发送信号
}

func newFakeSource(s int64) *fakeSource {
	return &fakeSource{
		s:     s,
		objs:  make(map[string]fakeObj),
		flips: make(map[string][]fakeObj),
	}
}

func (f *fakeSource) set(key, version string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objs[key] = fakeObj{version: version, data: data}
}

// armFlip 安排键 key 在接下来的回源前依次翻转到给定版本（每次回源消费一个）。
func (f *fakeSource) armFlip(key string, objs ...fakeObj) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flips[key] = append(f.flips[key], objs...)
}

// failNext 让接下来 n 次回源以 err 失败。
func (f *fakeSource) failNext(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failErr = err
	f.failRemaining = n
}

func (f *fakeSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSource) fetchRecords() []fetchRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fetchRecord(nil), f.records...)
}

func (f *fakeSource) FetchChunks(ctx context.Context, key string, first, last int64, expectVersion string) (SourceResult, error) {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	g := f.gate
	if f.gates != nil {
		if gk, ok := f.gates[key]; ok {
			g = gk
		}
	}
	if g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return SourceResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.records = append(f.records, fetchRecord{key: key, first: first, last: last, expect: expectVersion})
	if queue := f.flips[key]; len(queue) > 0 {
		f.objs[key] = queue[0]
		f.flips[key] = queue[1:]
	}
	if f.failRemaining > 0 {
		f.failRemaining--
		return SourceResult{}, f.failErr
	}
	obj, ok := f.objs[key]
	if !ok {
		return SourceResult{}, errors.New("fake source: no such object")
	}
	total := int64(len(obj.data))
	res := SourceResult{Version: obj.version, Total: total}
	if total > 0 {
		lastIdx := (total - 1) / f.s
		hi := min(last, lastIdx)
		for i := first; i <= hi; i++ {
			a := i * f.s
			b := min(a+f.s, total)
			res.Data = append(res.Data, obj.data[a:b])
		}
	}
	return res, nil
}
