// Package api 是 K-slack 乱序事件计数的对外门面：并发安全封装 wcount。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/wcount"
)

// Event 是一条带序列号的变更事件（wcount.Event 的别名）。
type Event = wcount.Event

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyKey    = wcount.ErrEmptyKey
	ErrTooManyKeys = wcount.ErrTooManyKeys
	ErrBadParam    = errors.New("api: K<0 or maxKeys<=0")
)

// W 是并发安全的乱序计数器。
type W struct {
	mu sync.RWMutex
	c  *wcount.Counter
}

// New 构造实例；K<0 或 maxKeys<=0 返回 ErrBadParam。
func New(K int64, maxKeys int) (*W, error) {
	if K < 0 || maxKeys <= 0 {
		return nil, ErrBadParam
	}
	return &W{c: wcount.New(K, maxKeys)}, nil
}

// Feed 批量处理事件；任一事件非法则整批不生效，之后仍可正常使用。
func (w *W) Feed(evs []Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.c.Feed(evs)
}

// Accepted 返回该 Key 的累计接受数。
func (w *W) Accepted(key string) int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.c.Accepted(key)
}

// Dropped 返回全局累计超窗丢弃数。
func (w *W) Dropped() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.c.Dropped()
}

// High 返回该 Key 的高水位；false 表示尚无高水位。
func (w *W) High(key string) (int64, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.c.High(key)
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。
// 只操作自建实例，不影响接收者状态，可并发调用。
func (w *W) SelfCheck() error {
	// 不变量 1+2+3：第三节八步序列的终态必须与朴素规则一致。
	w8, err := New(3, 8)
	if err != nil {
		return err
	}
	if err := w8.Feed([]Event{
		{Key: "a", Seq: 10}, {Key: "a", Seq: 8}, {Key: "a", Seq: 12}, {Key: "a", Seq: 5},
		{Key: "a", Seq: 8}, {Key: "a", Seq: 11}, {Key: "a", Seq: 9}, {Key: "a", Seq: 7},
	}); err != nil {
		return err
	}
	if h, _ := w8.High("a"); h != 12 || w8.Accepted("a") != 5 || w8.Dropped() != 3 {
		return errors.New("selfcheck: eight-step sequence mismatch")
	}
	// 不变量 4：整批失败不留痕（空 Key / 超限），之后仍可正常使用。
	before := w8.Dropped()
	if err := w8.Feed([]Event{{Key: "b", Seq: 1}, {Key: "", Seq: 2}}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: empty key not rejected")
	}
	if err := w8.Feed([]Event{
		{Key: "c", Seq: 1}, {Key: "d", Seq: 2}, {Key: "e", Seq: 3}, {Key: "f", Seq: 4},
		{Key: "g", Seq: 5}, {Key: "h", Seq: 6}, {Key: "i", Seq: 7}, {Key: "j", Seq: 8},
	}); !errors.Is(err, ErrTooManyKeys) {
		return errors.New("selfcheck: too many keys not rejected")
	}
	if w8.Dropped() != before || w8.Accepted("b") != 0 || w8.Accepted("j") != 0 {
		return errors.New("selfcheck: rejected batch left traces")
	}
	if err := w8.Feed([]Event{{Key: "b", Seq: 1}}); err != nil || w8.Accepted("b") != 1 {
		return errors.New("selfcheck: instance unusable after rejection")
	}
	// 参数非法可判定。
	if _, err := New(-1, 1); !errors.Is(err, ErrBadParam) {
		return errors.New("selfcheck: bad K not rejected")
	}
	if _, err := New(0, 0); !errors.Is(err, ErrBadParam) {
		return errors.New("selfcheck: bad maxKeys not rejected")
	}
	// 定位成本不随 Key 数增长（内部断言，不暴露计数器数值）。
	if !w8.c.SelfCheck() {
		return fmt.Errorf("selfcheck: lookup cost grows with key count")
	}
	return nil
}
