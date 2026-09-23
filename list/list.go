// Package list 实现确定性跳表本体：查找、插入、删除，并维护每层跨度不变量。
package list

import (
	"errors"
	"math"
	"sync"

	"ontology/key"
	"ontology/node"
)

var (
	ErrNotFound = errors.New("list: element not found")
	ErrLimit    = errors.New("list: resource limit exceeded")
)

// Config 是资源上限：MaxHeight 限层高，MaxElements 限元素总数（0 不限）。
type Config struct{ MaxHeight, MaxElements int }

func DefaultConfig() Config { return Config{MaxHeight: 20} }

type List struct {
	mu                     sync.RWMutex
	head, tail             *node.Node
	size                   int
	MaxHeight, MaxElements int
}

func New(cfg Config) *List {
	l := &List{MaxHeight: cfg.MaxHeight, MaxElements: cfg.MaxElements}
	l.head, l.tail = node.New(math.MinInt64, cfg.MaxHeight), node.New(math.MaxInt64, cfg.MaxHeight)
	for i := 1; i <= cfg.MaxHeight; i++ {
		l.head.At(i).Next = l.tail
	}
	return l
}

func (l *List) Size() int { return l.size }
func (l *List) RLock()    { l.mu.RLock() }
func (l *List) RUnlock()  { l.mu.RUnlock() }
func (l *List) Lock()     { l.mu.Lock() }
func (l *List) Unlock()   { l.mu.Unlock() }

func (l *List) findPath(k key.Key) (pred []*node.Node, rank []int) {
	pred, rank = make([]*node.Node, l.MaxHeight+1), make([]int, l.MaxHeight+1)
	x, pos := l.head, 0
	for level := l.MaxHeight; level >= 1; level-- {
		for y := x.At(level).Next; y != l.tail && y.Key < k; y = x.At(level).Next {
			pos, x = pos+x.At(level).Span, y
		}
		pred[level], rank[level] = x, pos
	}
	return
}
func (l *List) Insert(k key.Key) error {
	if l.MaxElements > 0 && l.size >= l.MaxElements {
		return ErrLimit
	}
	pred, rank := l.findPath(k)
	n := node.New(k, key.Height(k, l.MaxHeight))
	for level := 1; level <= n.Height(); level++ {
		link := pred[level].At(level)
		n.At(level).Next, n.At(level).Span = link.Next, link.Span-(rank[1]-rank[level])
		link.Next, link.Span = n, rank[1]-rank[level]+1
	}
	for level := n.Height() + 1; level <= l.MaxHeight; level++ {
		pred[level].At(level).Span++
	}
	l.size++
	return nil
}

func (l *List) Delete(k key.Key) error {
	pred, _ := l.findPath(k)
	v := pred[1].At(1).Next
	if v == l.tail || v.Key != k {
		return ErrNotFound
	}
	for level := 1; level <= v.Height(); level++ {
		link := pred[level].At(level)
		link.Span, link.Next = link.Span+v.At(level).Span-1, v.At(level).Next
	}
	for level := v.Height() + 1; level <= l.MaxHeight; level++ {
		pred[level].At(level).Span--
	}
	l.size--
	return nil
}

func (l *List) Snapshot() []key.Key {
	out := make([]key.Key, 0, l.size)
	for x := l.head.At(1).Next; x != l.tail; x = x.At(1).Next {
		out = append(out, x.Key)
	}
	return out
}

func (l *List) Equal(o *List) bool {
	if l.MaxHeight != o.MaxHeight || l.size != o.size {
		return false
	}
	a, b := l.head, o.head
	for {
		if !a.Equal(b) || (a == l.tail) != (b == o.tail) {
			return false
		}
		if a == l.tail {
			return true
		}
		a, b = a.At(1).Next, b.At(1).Next
	}
}

func (l *List) SelfCheck() error {
	for level := 1; level <= l.MaxHeight; level++ {
		x, seen := l.head, 0
		for y := x.At(level).Next; ; x, y = y, y.At(level).Next {
			if y == nil {
				return ErrLimit
			}
			if (x != l.head && y != l.tail && y.Key <= x.Key) ||
				l.bottomSpan(x, y) != x.At(level).Span {
				return errors.New("list: self-check failed")
			}
			if y != l.tail {
				seen += x.At(level).Span
			}
			if y == l.tail {
				break
			}
		}
		if seen != l.size {
			return errors.New("list: size mismatch")
		}
	}
	return nil
}

func (l *List) bottomSpan(from, to *node.Node) int {
	count := 0
	for x := from.At(1).Next; x != to; x = x.At(1).Next {
		if x == nil {
			return -1
		}
		count++
	}
	if to != l.tail {
		count++
	}
	return count
}
