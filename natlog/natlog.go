package natlog

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("natlog: invalid argument")
	ErrUnallocated     = errors.New("natlog: block unallocated at time")
)

type Kind int

const (
	Alloc Kind = iota
	Free
)

func (k Kind) String() string {
	if k == Alloc {
		return "ALLOC"
	}
	return "FREE"
}

// Entry 是一条块级生命周期日志。
type Entry struct {
	Seq  int64
	Kind Kind
	Sub  int64
	Addr int
	Lo   int
	Hi   int
	At   int64
}

// seg 是某块归属链上的一次 ALLOC 及其可选 FREE 封边。
type seg struct {
	sub    int64
	alloc  int64
	freeAt int64
	freed  bool
	hi     int
}

type blockKey struct {
	addr int
	lo   int
}

// Logger 为追加式块日志。所有方法可并发调用。
type Logger struct {
	mu      sync.Mutex
	seq     int64
	entries []Entry
	chains  map[blockKey][]seg
	// 每个地址已出现过 ALLOC 的块首端口，有序，用于把端口映射到块。
	spans map[int][]int
	// 已落地的最大操作时刻；FREE 的逻辑时刻不会超过它。
	maxNow int64
	// 上次 Lookup 在归属链上的比较计数，供测试核对复杂度。
	lastCmps int
}

func New() *Logger {
	return &Logger{
		chains: make(map[blockKey][]seg),
		spans:  make(map[int][]int),
	}
}

// Append 追加一条日志。调用方须保证时刻不减、ALLOC/FREE 配对合法。
func (l *Logger) Append(kind Kind, sub int64, addr, lo, hi int, at int64) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	e := Entry{Seq: l.seq, Kind: kind, Sub: sub, Addr: addr, Lo: lo, Hi: hi, At: at}
	l.entries = append(l.entries, e)
	if at > l.maxNow {
		l.maxNow = at
	}

	key := blockKey{addr: addr, lo: lo}
	switch kind {
	case Alloc:
		l.chains[key] = append(l.chains[key], seg{sub: sub, alloc: at, hi: hi})
		if !containsInt(l.spans[addr], lo) {
			l.spans[addr] = append(l.spans[addr], lo)
			sort.Ints(l.spans[addr])
		}
	case Free:
		chain := l.chains[key]
		chain[len(chain)-1].freed = true
		chain[len(chain)-1].freeAt = at
	}
	return e
}

// Entries 返回日志快照（拷贝）。
func (l *Logger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// LastNow 返回已落地日志涉及的最大时刻（即已接受操作的最大 now）。
func (l *Logger) LastNow() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxNow
}

// Touch 登记一个已接受操作的时刻，即使该操作未产生日志（如复用空闲块）。
// Lookup 的 t 上界是"已接受操作的最大 now"，而非最后一条日志时刻。
func (l *Logger) Touch(at int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at > l.maxNow {
		l.maxNow = at
	}
}

// LastLookupCmps 返回最近一次 Lookup 在块归属链上的比较次数。
func (l *Logger) LastLookupCmps() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastCmps
}

// Lookup 回答 t 时刻持有 (addr,port) 所在块的订户。
// 区间为 ALLOC 时刻 ≤ t < FREE 时刻，左闭右开；未释放时右端为无穷。
func (l *Logger) Lookup(addr, port int, t int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lastCmps = 0
	if addr < 0 || port < 0 || t < 0 || t > l.maxNow {
		return 0, ErrInvalidArgument
	}
	spans := l.spans[addr]
	lo, ok := l.blockFor(spans, port)
	if !ok {
		return 0, ErrUnallocated
	}
	chain := l.chains[blockKey{addr: addr, lo: lo}]
	if port > chain[0].hi {
		return 0, ErrUnallocated
	}

	// 二分找最后一个 alloc <= t；每次 alloc 比较计数一次。
	loIdx, hiIdx := 0, len(chain)
	for loIdx < hiIdx {
		mid := int(uint(loIdx+hiIdx) >> 1)
		l.lastCmps++
		if chain[mid].alloc <= t {
			loIdx = mid + 1
		} else {
			hiIdx = mid
		}
	}
	i := loIdx - 1
	if i < 0 {
		return 0, ErrUnallocated
	}
	s := chain[i]
	if s.freed {
		// 右端比较一次；总次数 ≤ floor(log2 n) + 2。
		l.lastCmps++
		if t >= s.freeAt {
			return 0, ErrUnallocated
		}
	}
	return s.sub, nil
}

// blockFor 在有序块首列表中找 port 所属块（lo <= port，且其后无更近块首）。
func (l *Logger) blockFor(spans []int, port int) (int, bool) {
	if len(spans) == 0 || port < spans[0] {
		return 0, false
	}
	i, j := 0, len(spans)
	for i < j {
		m := int(uint(i+j) >> 1)
		if spans[m] <= port {
			i = m + 1
		} else {
			j = m
		}
	}
	return spans[i-1], true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
