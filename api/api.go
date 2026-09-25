// Package api 是有界乱序重排缓冲器的对外接口：并发安全包装、参数校验、自检。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"

	"ontology/reorder"
	"ontology/seq"
)

// ErrBadParam 表示 maxBuffered < 1 或 timeout < 0。
var ErrBadParam = errors.New("api: 参数非法")

// Buffer 是并发安全的重排缓冲器。
type Buffer struct {
	mu   sync.RWMutex
	core *reorder.Core
}

// New 构造 Buffer；参数非法返回 ErrBadParam，不产生任何状态。
func New(maxBuffered, timeout int) (*Buffer, error) {
	if maxBuffered < 1 || timeout < 0 {
		return nil, ErrBadParam
	}
	return &Buffer{core: reorder.New(maxBuffered, timeout)}, nil
}

// Feed 投递一个事件，返回本次发出的事件序列。
func (b *Buffer) Feed(ev seq.Event) ([]seq.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.core.Feed(ev)
}

// Tick 推进逻辑时钟，返回超时 flush 发出的事件序列。
func (b *Buffer) Tick() ([]seq.Event, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.core.Tick() }

// View 返回已发出事件序列的副本，可并发调用。
func (b *Buffer) View() []seq.Event { b.mu.RLock(); defer b.mu.RUnlock(); return b.core.View() }

// Lost 返回丢失 Seq 的升序副本，可并发调用。
func (b *Buffer) Lost() []int64 { b.mu.RLock(); defer b.mu.RUnlock(); return b.core.Lost() }

// State 返回 now、next 与缓冲内容副本，供演示与测试观测。
func (b *Buffer) State() (now, next int64, buffered []seq.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.core.Now(), b.core.Next(), b.core.Buffered()
}

// SelfCheck 对一组内置操作核验四条不变量；只操作内部新实例，可并发调用。
func (b *Buffer) SelfCheck() error {
	if err := checkReplay(); err != nil {
		return err
	}
	return checkAtomic()
}

// checkReplay 核验不变量 1（朴素重放一致）、2（严格递增）、3（缓冲不超界）。
func checkReplay() error {
	const maxB, timeout, n = 8, 3, 300
	f, err := New(maxB, timeout)
	if err != nil {
		return err
	}
	var accepted []seq.Event
	for i, p := range rand.New(rand.NewSource(7)).Perm(n) { // Seq 1..n 随机到达
		ev := seq.Event{Seq: int64(p + 1), Value: p}
		if _, err := f.Feed(ev); err == nil {
			accepted = append(accepted, ev)
		} else if !errors.Is(err, reorder.ErrOverflow) && !errors.Is(err, seq.ErrStale) {
			return fmt.Errorf("selfcheck: 意外拒绝: %w", err) // 溢出/过期是合法拒绝
		}
		if _, _, bf := f.State(); len(bf) > maxB {
			return errors.New("selfcheck: 不变量3 缓冲超界")
		}
		if i%5 == 4 { // 周期性 Tick 制造超时 flush
			f.Tick()
		}
	}
	for i := 0; i <= n*(timeout+1); i++ { // Tick 足够多次清空缓冲
		if _, _, bf := f.State(); len(bf) == 0 {
			break
		}
		f.Tick()
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].Seq < accepted[j].Seq })
	lost := map[int64]bool{}
	for _, s := range f.Lost() {
		lost[s] = true
	}
	var want []seq.Event
	for _, ev := range accepted { // 朴素重放：升序、跳过丢失
		if !lost[ev.Seq] {
			want = append(want, ev)
		}
	}
	got := f.View()
	if len(got) != len(want) {
		return fmt.Errorf("selfcheck: 不变量1 发出 %d 条 != 朴素 %d 条", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("selfcheck: 不变量1 第%d条 %v != %v", i, got[i], want[i])
		}
		if i > 0 && got[i].Seq <= got[i-1].Seq {
			return errors.New("selfcheck: 不变量2 发出序列非严格递增")
		}
	}
	return nil
}

// checkAtomic 核验不变量 4：四类拒绝可判定、互不相同、不留痕，拒绝后仍可用。
func checkAtomic() error {
	f, err := New(2, 1)
	if err != nil {
		return err
	}
	f.Feed(seq.Event{Seq: 1, Value: 1})
	f.Feed(seq.Event{Seq: 3, Value: 3})
	f.Feed(seq.Event{Seq: 4, Value: 4}) // 缓冲已满
	snap := func() string {
		now, next, bf := f.State()
		return fmt.Sprintf("%d|%d|%v|%v|%v", now, next, bf, f.View(), f.Lost())
	}
	before := snap()
	rejSeqs := []int64{0, 1, 5} // 非法 / 过期 / 溢出
	rejErrs := []error{seq.ErrInvalidSeq, seq.ErrStale, reorder.ErrOverflow}
	for i, s := range rejSeqs {
		if _, err := f.Feed(seq.Event{Seq: s}); !errors.Is(err, rejErrs[i]) {
			return fmt.Errorf("selfcheck: 拒绝错误不符, 得 %v", err)
		}
		if snap() != before {
			return errors.New("selfcheck: 不变量4 拒绝后状态被改变")
		}
	}
	for _, p := range [][2]int{{0, 1}, {1, -1}} { // maxBuffered<1 / timeout<0
		if _, err := New(p[0], p[1]); !errors.Is(err, ErrBadParam) {
			return errors.New("selfcheck: 参数非法未报错")
		}
	}
	if out, err := f.Feed(seq.Event{Seq: 2, Value: 2}); err != nil || len(out) != 3 {
		return errors.New("selfcheck: 拒绝后实例不可用")
	}
	return nil
}
