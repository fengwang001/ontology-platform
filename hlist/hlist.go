// Package hlist 实现 Harris 无锁有序链表：升序、无重复，
// 删除分逻辑删除（mark 位）与物理摘除两步，遍历跳过已 mark 节点。
package hlist

import (
	"errors"
	"sync/atomic"

	"ontology/lnode"
)

// 可判定的哨兵错误。
var (
	ErrDuplicate = errors.New("hlist: duplicate key")
	ErrNotFound  = errors.New("hlist: key not found")
)

// List 是按 key 升序的无锁链表，带头哨兵，末尾以 nil 终止。
type List struct {
	head *lnode.Node

	// unlinkVisited 记录最近一次 Delete 物理摘除阶段访问的节点个数。
	// 非导出，不出现在任何公开接口。
	unlinkVisited atomic.Int64
}

// New 返回只含头哨兵的空链表。
func New() *List { return &List{head: lnode.NewHead()} }

// find 定位到 pred.Key < k 且 curr.Key >= k（或 curr==nil 到尾），
// 途中顺手物理摘除已 mark 的节点（helping）。返回时 pred 未 mark。
func (l *List) find(k int) (pred, curr *lnode.Node) {
retry:
	pred = l.head
	curr, _ = pred.Next()
	for curr != nil {
		succ, marked := curr.Next()
		for marked {
			if !pred.CASNext(curr, succ, false, false) {
				goto retry
			}
			curr = succ
			if curr == nil {
				return pred, nil
			}
			succ, marked = curr.Next()
		}
		if curr.Key >= k {
			return pred, curr
		}
		pred = curr
		curr = succ
	}
	return pred, nil
}

// Insert 按升序插入 k；已存在未删除的 k 报 ErrDuplicate 且不改状态。
func (l *List) Insert(k int) error {
	for {
		pred, curr := l.find(k)
		if curr != nil && curr.Key == k {
			return ErrDuplicate
		}
		n := lnode.New(k, curr)
		if pred.CASNext(curr, n, false, false) {
			return nil
		}
	}
}

// Delete 先 CAS 置 mark 位（逻辑删除），再 CAS 摘除（物理删除）。
// 不存在未删除的 k 报 ErrNotFound 且不改状态。
func (l *List) Delete(k int) error {
	for {
		pred, curr := l.find(k)
		if curr == nil || curr.Key != k {
			return ErrNotFound
		}
		succ, _ := curr.Next()
		if !curr.MarkNext() {
			continue // 被并发删除抢先，重试后按不存在处理
		}
		l.unlink(k, pred, curr, succ)
		return nil
	}
}

// unlink 物理摘除已 mark 的 curr：把前驱 pred 的 next 指向后继 succ。
// 无竞争时只访问前驱与后继这常数个（≤2）相邻节点，不重扫全链表；
// 仅当 CAS 因并发冲突失败时才重新 find 定位。
func (l *List) unlink(k int, pred, curr, succ *lnode.Node) {
	visited := 2 // 访问前驱与后继
	for !pred.CASNext(curr, succ, false, false) {
		pred, curr = l.find(k)
		if curr == nil || curr.Key != k {
			break // 已被 find 的 helping 或并发 Delete 摘除
		}
		succ, _ = curr.Next()
		visited += 2
	}
	l.unlinkVisited.Store(int64(visited))
}

// Contains 遍历跳过已 mark 节点；k 存在且未 mark 返回 true。
func (l *List) Contains(k int) bool {
	curr, _ := l.head.Next()
	for curr != nil {
		next, marked := curr.Next()
		if curr.Key > k {
			return false
		}
		if !marked && curr.Key == k {
			return true
		}
		curr = next
	}
	return false
}

// List 返回当前未删除 key 的升序切片。
func (l *List) List() []int {
	var out []int
	curr, _ := l.head.Next()
	for curr != nil {
		next, marked := curr.Next()
		if !marked {
			out = append(out, curr.Key)
		}
		curr = next
	}
	return out
}
