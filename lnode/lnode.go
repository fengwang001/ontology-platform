// Package lnode 提供 Harris 无锁链表的节点：key 加带 mark 位的原子 next 指针。
// mark 位是 next 指针的最低位；读 next 必须先清位再解引用。
//
// 实现说明：next 以 *byte 存储（对齐为 1，带标记的指针也能通过 checkptr），
// 标记/清位用 unsafe.Add 做指针运算——结果始终指向原节点分配内部，
// 对 GC 是合法内部指针，节点经 next 链保活。
package lnode

import (
	"math"
	"sync/atomic"
	"unsafe"
)

// Node 是链表节点。Key 不可变；next 的最低位是逻辑删除 mark 位。
type Node struct {
	Key  int
	next atomic.Pointer[byte] // *Node | mark 位
}

// markedNil 是「nil | mark 位」即 0x1：尾节点（next 为 nil）被逻辑删除时
// next 的标记形态。只在包初始化时计算一次。
var markedNil = markedNilPtr()

//go:nocheckptr // 0x1 是有意构造的标记值，不是可解引用的指针
func markedNilPtr() *byte {
	// 写成 nil 基址 + 偏移的形式，go vet 才认可这是合法的指针运算。
	return (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(nil)) + 1))
}

func pack(p *Node, marked bool) *byte {
	if p == nil {
		if marked {
			return markedNil
		}
		return nil
	}
	u := unsafe.Pointer(p)
	if marked {
		u = unsafe.Add(u, 1) // 置最低位：节点至少 16 字节，+1 仍在分配内
	}
	return (*byte)(u)
}

// Unpack 把带标记的指针拆成清掉 mark 位后的节点指针与 mark 标志。
func Unpack(b *byte) (*Node, bool) {
	if b == nil {
		return nil, false
	}
	if b == markedNil {
		return nil, true
	}
	if uintptr(unsafe.Pointer(b))&1 == 0 {
		return (*Node)(unsafe.Pointer(b)), false
	}
	// 清最低位：-1 回到节点起始地址。
	// 注意：必须显式分支，不能写成 unsafe.Add(b, -tag)——
	// 编译器会把 unsafe.Add 的结果当作非 nil，nil 输入会被误判。
	return (*Node)(unsafe.Add(unsafe.Pointer(b), -1)), true
}

// NewHead 返回头哨兵节点，key 视为 -∞。
func NewHead() *Node { return &Node{Key: math.MinInt} }

// New 返回一个 key 为 k、next 指向 next 的未标记节点。
func New(k int, next *Node) *Node {
	n := &Node{Key: k}
	n.next.Store(pack(next, false))
	return n
}

// Next 返回清掉 mark 位后的后继指针与当前 mark 标志。
func (n *Node) Next() (*Node, bool) { return Unpack(n.next.Load()) }

// Marked 报告节点是否已被逻辑删除。
func (n *Node) Marked() bool {
	_, m := n.Next()
	return m
}

// CASNext 当 next 等于 (old, oldMark) 时替换为 (new, newMark)。
func (n *Node) CASNext(old, new_ *Node, oldMark, newMark bool) bool {
	return n.next.CompareAndSwap(pack(old, oldMark), pack(new_, newMark))
}

// MarkNext 把 next 的 mark 位置 1（逻辑删除）。返回是否由本次调用置位。
func (n *Node) MarkNext() bool {
	for {
		p, m := n.Next()
		if m {
			return false
		}
		if n.CASNext(p, p, false, true) {
			return true
		}
	}
}
