// Package api 是滑动去重窗口的对外门面，并发安全。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dedup"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrEmptyID            = errors.New("dedup: empty id")
	ErrNonPositiveWindow  = errors.New("dedup: window must be > 0")
	ErrNonPositiveMaxOpen = errors.New("dedup: maxOpen must be > 0")
)

// Deduper 是滑动去重窗口。
type Deduper struct {
	mu sync.Mutex
	t  *dedup.Table
}

// New 校验参数，失败时不产生任何状态。
func New(w int64, maxOpen int) (*Deduper, error) {
	if w <= 0 {
		return nil, ErrNonPositiveWindow
	}
	if maxOpen <= 0 {
		return nil, ErrNonPositiveMaxOpen
	}
	return &Deduper{t: dedup.New(w, maxOpen)}, nil
}

// Dedup 报告事件是否被接受；空 ID 整体失败且不留痕。
func (d *Deduper) Dedup(id string, ts int64) (bool, error) {
	if id == "" {
		return false, ErrEmptyID
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Dedup(id, ts), nil
}

// View 返回当前保留的 ID → last 快照。
func (d *Deduper) View() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.View()
}

// Duplicated 返回累计重复数。
func (d *Deduper) Duplicated() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Duplicated()
}

// Accepted 返回累计接受数。
func (d *Deduper) Accepted() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Accepted()
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。
// 自检在内部新建的实例上进行，不读写调用者状态，可并发调用。
func (d *Deduper) SelfCheck() error {
	// 不变量 1+3：八步序列的判定、last 与淘汰（W=5, maxOpen=3）
	d, err := New(5, 3)
	if err != nil {
		return err
	}
	type ev struct {
		id string
		ts int64
	}
	seq := []ev{{"X", 10}, {"X", 15}, {"Y", 20}, {"Z", 30}, {"Y", 24}, {"W", 40}, {"Y", 25}, {"Z", 32}}
	wantAcc := []bool{true, true, true, true, false, true, true, false}
	last := map[string]int64{}
	for i, e := range seq {
		got, err := d.Dedup(e.id, e.ts)
		if err != nil || got != wantAcc[i] {
			return fmt.Errorf("selfcheck step %d: got acc=%v err=%v", i, got, err)
		}
		if got {
			if prev, ok := last[e.id]; ok && e.ts-prev < 5 {
				return fmt.Errorf("selfcheck: window overlap for %s", e.id)
			}
			last[e.id] = e.ts
		}
	}
	v := d.View()
	if len(v) != 3 || v["Y"] != 25 || v["Z"] != 30 || v["W"] != 40 {
		return fmt.Errorf("selfcheck: bad final view %v", v)
	}
	// 不变量 2：与朴素参照一致（确定性伪随机序列，含乱序与重复）
	const n = 2000
	d2, _ := New(7, 50)
	naive := map[string]int64{}
	for i := 0; i < n; i++ {
		id := string(rune('a' + (i*37+i/13)%40)) // 40 个 ID < maxOpen，不涉及淘汰
		ts := int64((i*91 + i/7) % 400)
		got, _ := d2.Dedup(id, ts)
		prev, ok := naive[id]
		want := !ok || ts-prev >= 7
		if got != want {
			return fmt.Errorf("selfcheck: naive mismatch at %d", i)
		}
		if want {
			naive[id] = ts
		}
	}
	// 不变量 4：被拒操作不留痕
	acc, dup, view := d.Accepted(), d.Duplicated(), d.View()
	if _, err := d.Dedup("", 99); !errors.Is(err, ErrEmptyID) {
		return fmt.Errorf("selfcheck: empty id not rejected")
	}
	if d.Accepted() != acc || d.Duplicated() != dup || len(d.View()) != len(view) {
		return fmt.Errorf("selfcheck: rejected op left trace")
	}
	if _, err := New(0, 1); !errors.Is(err, ErrNonPositiveWindow) {
		return fmt.Errorf("selfcheck: W<=0 not rejected")
	}
	if _, err := New(1, 0); !errors.Is(err, ErrNonPositiveMaxOpen) {
		return fmt.Errorf("selfcheck: maxOpen<=0 not rejected")
	}
	return nil
}
