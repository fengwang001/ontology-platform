// Package api 是带背压批量 flush 变更缓冲的对外门面，依赖方向：api -> bflush。
package api

import (
	"errors"
	"strconv"

	"ontology/bflush"
	"ontology/buf"
)

// Entry 与 Sink 直接复用内层定义。
type Entry = buf.Entry
type Sink = bflush.Sink

// 四类可判定哨兵错误（与 bflush 同一底层值，errors.Is 可判）。
var (
	ErrInvalidParam = bflush.ErrInvalidParam
	ErrHighWater    = bflush.ErrHighWater
	ErrEmptyKey     = bflush.ErrEmptyKey
	ErrSink         = bflush.ErrSink
)

var errBoom = errors.New("api: injected sink failure")

func errSelf(msg string) error { return errors.New("api selfcheck: " + msg) }

// recSink 记录成功交付；failOn>0 时在第 failOn 次 Apply（1 基）失败且不记录；越过 maxB 置 over。
type recSink struct {
	got    []Entry
	calls  int
	failOn int
	maxB   int
	over   bool
}

func (s *recSink) Apply(b []Entry) error {
	s.over = s.over || len(b) > s.maxB
	s.calls++
	if s.failOn > 0 && s.calls == s.failOn {
		return errBoom
	}
	s.got = append(s.got, b...)
	return nil
}

// Buffer 是对外句柄。
type Buffer struct{ e *bflush.Engine }

// New 构造缓冲：B 批量大小、H 高水位、L 延迟阈值。
func New(b, h, l int, sink Sink) (*Buffer, error) {
	e, err := bflush.New(b, h, l, sink)
	return &Buffer{e: e}, err
}

func (x *Buffer) Write(key string, val int) error { return x.e.Write(key, val) }
func (x *Buffer) Tick() (int, error)              { return x.e.Tick() }
func (x *Buffer) Flush() (int, error)             { return x.e.Flush() }
func (x *Buffer) FlushAll() error                 { return x.e.FlushAll() }
func (x *Buffer) Buffered() int                   { return x.e.Buffered() }
func (x *Buffer) Delivered() int64                { return x.e.Delivered() }

// SelfCheck 用内置事件序列核验第二节四条不变量，全部成立返回 nil。
func (x *Buffer) SelfCheck() error {
	s := &recSink{maxB: 3}
	e, _ := bflush.New(3, 9, 2, s)
	var want []Entry
	seed := int64(7)
	for i := 0; i < 120; i++ {
		seed = seed*2862933555777941757 + 3037000493
		switch seed % 5 {
		case 0, 1, 2:
			en := Entry{Key: strconv.Itoa(int(seed % 23)), Val: i}
			if e.Write(en.Key, en.Val) == nil {
				want = append(want, en)
			}
		case 3:
			if _, err := e.Tick(); err != nil {
				return err
			}
		default:
			if _, err := e.Flush(); err != nil {
				return err
			}
		}
	}
	if err := e.FlushAll(); err != nil {
		return err
	}
	if s.over || len(s.got) != len(want) {
		return errSelf("batch bound or count")
	}
	for i := range want {
		if s.got[i] != want[i] {
			return errSelf("fifo replay")
		}
	}
	// 不变量 3：age 1、2 不冲；age 恰好 ==L 的第 3 个 Tick 冲走。
	l, _ := bflush.New(5, 10, 3, &recSink{maxB: 5})
	l.Write("x", 1)
	for i := 0; i < 2; i++ {
		if _, err := l.Tick(); err != nil || l.Buffered() != 1 {
			return errSelf("flushed before age==L")
		}
	}
	if _, err := l.Tick(); err != nil || l.Buffered() != 0 || l.Delivered() != 1 {
		return errSelf("latency bound")
	}
	// 不变量 4：第 2 批失败整体回滚可恢复；拒收/非法参数不留痕。
	f := &recSink{maxB: 3, failOn: 2}
	g, _ := bflush.New(3, 4, 2, f)
	for _, k := range []string{"a", "b", "c"} {
		g.Write(k, 0)
	}
	g.Tick()
	for _, k := range []string{"d", "e", "f"} {
		g.Write(k, 0)
	}
	if n, err := g.Tick(); !errors.Is(err, bflush.ErrSink) || n != 1 ||
		g.Buffered() != 3 || g.Delivered() != 3 {
		return errSelf("failed tick rollback")
	}
	f.failOn = 0
	if err := g.FlushAll(); err != nil || g.Delivered() != 6 {
		return errSelf("retry after rollback")
	}
	cnt := map[string]int{}
	for _, en := range f.got {
		cnt[en.Key]++
	}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		if cnt[k] != 1 {
			return errSelf("duplicate after retry")
		}
	}
	h, _ := bflush.New(1, 1, 1, &recSink{maxB: 1})
	h.Write("k", 1)
	if err := h.Write("j", 1); !errors.Is(err, bflush.ErrHighWater) || h.Buffered() != 1 {
		return errSelf("high-water rejection traced")
	}
	if err := h.Write("", 1); !errors.Is(err, bflush.ErrEmptyKey) || h.Buffered() != 1 {
		return errSelf("empty-key rejection traced")
	}
	for _, c := range [3][3]int{{0, 1, 1}, {2, 1, 1}, {1, 1, 0}} {
		if _, err := bflush.New(c[0], c[1], c[2], &recSink{}); !errors.Is(err, bflush.ErrInvalidParam) {
			return errSelf("invalid parameter accepted")
		}
	}
	return nil
}
