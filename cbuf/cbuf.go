// Package cbuf 是只收不发的因果广播接收端：本地向量、按(发送方,序号)
// 索引的缓冲区、交付与级联、交付序列、重复计数。仅依赖 vc 包。
package cbuf

import (
	"errors"
	"sort"

	"ontology/vc"
)

// ErrFull：消息需要进入缓冲但容量已满。与其余三类哨兵错误互不相同。
var ErrFull = errors.New("cbuf: buffer full")

// Buffer 是非并发安全的接收端核心；并发由上层 api 加锁。
type Buffer struct {
	n         int
	maxBuf    int
	local     []int64
	buf       []map[int64]vc.Msg // buf[j]：发送方 j 的 序号 -> 消息
	size      int
	delivered []vc.Msg
	dups      int64
	// checked 非导出：最近一次到达（含级联）检查过交付条件的缓冲消息条数，
	// 刚到达的消息不计。绝不经导出 API 暴露；白盒测试在包内直读。
	checked int64
}

// New 构造接收端；n、maxBuf 的合法性由 api.New 校验。
func New(n, maxBuf int) *Buffer {
	b := &Buffer{
		n:      n,
		maxBuf: maxBuf,
		local:  make([]int64, n),
		buf:    make([]map[int64]vc.Msg, n),
	}
	for i := range b.buf {
		b.buf[i] = map[int64]vc.Msg{}
	}
	return b
}

// Receive 处理一次到达：校验 → 判重 → 可交付则交付并级联，否则入缓冲。
// 除重复计数外，任何拒绝路径都不改变状态。返回本次到达触发的交付（先后序）。
func (b *Buffer) Receive(m vc.Msg) ([]vc.Msg, error) {
	if err := vc.Validate(m, b.n); err != nil {
		return nil, err
	}
	b.checked = 0 // 任何合法到达都开启一次新的处理计数
	j, seq := m.From, m.V[m.From]
	if vc.Seen(m, b.local) { // 已交付过
		b.dups++
		return nil, nil
	}
	if _, ok := b.buf[j][seq]; ok { // 缓冲中已有同 (From,序号)
		b.dups++
		return nil, nil
	}
	b.checked = 0
	if vc.Deliverable(m, b.local) {
		b.local[j]++
		b.delivered = append(b.delivered, m)
		return append([]vc.Msg{m}, b.cascade()...), nil // 本次到达 + 级联，按交付先后
	}
	if b.size >= b.maxBuf { // 满则先判后写，拒绝不留痕
		return nil, ErrFull
	}
	b.buf[j][seq] = m
	b.size++
	return nil, nil
}

// cascade 反复取缓冲区当前所有可交付消息里 From 最小者交付，直到不动点。
// 每发送方至多一个候选：序号恰为 local[j]+1，map 直定位，不整表扫描。
func (b *Buffer) cascade() []vc.Msg {
	var out []vc.Msg
	for {
		found := false
		for j := 0; j < b.n; j++ {
			m, ok := b.buf[j][b.local[j]+1]
			if !ok {
				continue
			}
			b.checked++ // 取出的缓冲候选计一次条件检查
			if !vc.Deliverable(m, b.local) {
				continue
			}
			delete(b.buf[j], m.V[j])
			b.size--
			b.local[j]++
			b.delivered = append(b.delivered, m)
			out = append(out, m)
			found = true
			break // 交付后从 j=0 重扫，保证取最小 From
		}
		if !found {
			return out
		}
	}
}

// Delivered 返回交付序列的副本。
func (b *Buffer) Delivered() []vc.Msg { return append([]vc.Msg(nil), b.delivered...) }

// Buffered 返回缓冲消息副本，按 (From, 序号) 升序，便于与参照逐位对比。
func (b *Buffer) Buffered() []vc.Msg {
	out := make([]vc.Msg, 0, b.size)
	for j := 0; j < b.n; j++ {
		seqs := make([]int64, 0, len(b.buf[j]))
		for s := range b.buf[j] {
			seqs = append(seqs, s)
		}
		sort.Slice(seqs, func(p, q int) bool { return seqs[p] < seqs[q] })
		for _, s := range seqs {
			out = append(out, b.buf[j][s])
		}
	}
	return out
}

// Local 返回本地向量副本；Dups 返回重复计数。
func (b *Buffer) Local() []int64 { return append([]int64(nil), b.local...) }
func (b *Buffer) Dups() int64    { return b.dups }

// ComplexityBoundOK 只回判定、不泄露 checked 的数值：在多档 m 下验证
// （1）无关到达的检查条数为与 m 无关的小常数；（2）触发 m 条级联交付时
// 检查条数不超过 (交付数+1)*n 加常数。证明按 (发送方,序号) 定位而非整表扫描。
func ComplexityBoundOK() bool {
	const idle, slack = int64(6), int64(6)
	for _, m := range []int{100, 1000, 10000} {
		b := New(2, m+8)
		for s := 2; s <= m+1; s++ { // 发送方1序号2..m+1，序号1缺失
			if _, err := b.Receive(vc.Msg{From: 1, V: []int64{0, int64(s)}}); err != nil {
				return false
			}
		}
		if _, err := b.Receive(vc.Msg{From: 0, V: []int64{1, 0}}); err != nil || b.checked > idle {
			return false
		}
		n0 := len(b.Delivered())
		if _, err := b.Receive(vc.Msg{From: 1, V: []int64{1, 1}}); err != nil { // 触发全量级联
			return false
		}
		delivered := int64(len(b.Delivered()) - n0)
		if b.checked < int64(m) || b.checked > (delivered+1)*2+slack {
			return false
		}
	}
	return true
}
