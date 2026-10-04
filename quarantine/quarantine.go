package quarantine

import (
	"errors"
	"sort"
)

// ErrInvalidParam 表示构造参数非法（C/K/Dmax 越界或 K>C）。
var ErrInvalidParam = errors.New("quarantine: invalid parameter")

var (
	ErrFull    = errors.New("quarantine: full")
	ErrKeyFull = errors.New("quarantine: key queue full")
	ErrNoQueue = errors.New("quarantine: no queue for key")
)

type State int

const (
	Quarantined State = iota + 1
	Held
)

type Entry struct {
	Seq        int64
	Key        string
	Fields     map[string]int64
	RV         int64
	Violations []string
	State      State
}

type q struct {
	buf  []Entry // 环形队列逻辑：有效区间 [head, len(buf))
	head int
}

func (q *q) len() int { return len(q.buf) - q.head }

func (q *q) push(e Entry) { q.buf = append(q.buf, e) }

func (q *q) front() *Entry {
	if q.head >= len(q.buf) {
		return nil
	}
	return &q.buf[q.head]
}

func (q *q) pop() (Entry, bool) {
	if q.head >= len(q.buf) {
		return Entry{}, false
	}
	e := q.buf[q.head]
	q.buf[q.head] = Entry{}
	q.head++
	// 已出队过半时压缩，避免长生命周期队列长期占用双倍内存。
	if q.head > 0 && q.head*2 >= len(q.buf) {
		q.buf = append([]Entry(nil), q.buf[q.head:]...)
		q.head = 0
	}
	return e, true
}

// Zone 是隔离区：总容量 C、每键队列上限 K、每键封禁阈值 Dmax。
// 本身不加锁；并发安全由上层 flow.Gate 单锁串行化保证。
type Zone struct {
	c, k    int
	dmax    int
	total   int
	queues  map[string]*q
	discard map[string]int
}

// New 校验 1<=C<=1e5、1<=K<=C、1<=Dmax<=1000。
func New(C, K, Dmax int) (*Zone, error) {
	if C < 1 || C > 100000 || K < 1 || K > C || Dmax < 1 || Dmax > 1000 {
		return nil, ErrInvalidParam
	}
	return &Zone{
		c:       C,
		k:       K,
		dmax:    Dmax,
		queues:  make(map[string]*q),
		discard: make(map[string]int),
	}, nil
}

// Total 返回隔离区内当前记录总数（Quarantined 与 Held 都算）。
func (z *Zone) Total() int { return z.total }

// Cap 返回总容量 C 与每键队列上限 K。
func (z *Zone) Cap() (C, K int) { return z.c, z.k }

// Len 返回某键队列长度（无队列时为 0）。
func (z *Zone) Len(key string) int {
	if qq := z.queues[key]; qq != nil {
		return qq.len()
	}
	return 0
}

// Has 报告某键当前是否有非空队列。
func (z *Zone) Has(key string) bool { return z.Len(key) > 0 }

// Keys 按字节序返回所有有（非空）队列的键。
func (z *Zone) Keys() []string {
	keys := make([]string, 0, len(z.queues))
	for key, qq := range z.queues {
		if qq.len() > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// DiscardCount 返回某键累计 Discard 次数。
func (z *Zone) DiscardCount(key string) int { return z.discard[key] }

// MarkDiscarded 在队首被移入丢弃日志后记账一次。
func (z *Zone) MarkDiscarded(key string) int {
	z.discard[key]++
	return z.discard[key]
}

// Banned 报告某键累计丢弃次数是否已达 Dmax。
func (z *Zone) Banned(key string) bool { return z.discard[key] >= z.dmax }

// Append 把记录排入键队列：先查每键上限 K，再查总容量 C（错误次序 ErrKeyFull>ErrFull）。
func (z *Zone) Append(key string, e Entry) error {
	qq := z.queues[key]
	if qq != nil && qq.len() >= z.k {
		return ErrKeyFull
	}
	if z.total >= z.c {
		return ErrFull
	}
	if qq == nil {
		qq = &q{}
		z.queues[key] = qq
	}
	qq.push(e)
	z.total++
	return nil
}

// Front 返回队首记录的可修改指针；无队列（或空队列）时返回 nil。
func (z *Zone) Front(key string) *Entry {
	if qq := z.queues[key]; qq != nil {
		return qq.front()
	}
	return nil
}

// At 返回键队列中下标 i（0 为队首）记录的只读拷贝，供观测使用。
func (z *Zone) At(key string, i int) (Entry, bool) {
	qq := z.queues[key]
	if qq == nil || i < 0 || qq.head+i >= len(qq.buf) {
		return Entry{}, false
	}
	return qq.buf[qq.head+i], true
}

// PopFront 弹出队首记录；键队列空时返回 ok=false（ErrNoQueue 由上层判定并翻译）。
func (z *Zone) PopFront(key string) (Entry, bool) {
	qq := z.queues[key]
	if qq == nil || qq.len() == 0 {
		return Entry{}, false
	}
	e, ok := qq.pop()
	z.total--
	return e, ok
}
