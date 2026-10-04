// Package slot 把文档（id，可空 routing）哈希定位到固定数量的路由槽与分片。
package slot

import (
	"errors"
	"hash/fnv"
	"sort"
)

// ErrMissingRouting 表示 P>1 时未显式给出 routing（SearchShards 时 routing 恒不可为空）。
var ErrMissingRouting = errors.New("missing routing")

// HashFunc 把字节串映射为 32 位无符号整数，记作 h(字节串)。
type HashFunc func([]byte) uint32

// FNV32 是默认的 32 位 FNV-1a 哈希。
func FNV32(b []byte) uint32 {
	h := fnv.New32a()
	h.Write(b)
	return h.Sum32()
}

// Params 为一次定位所需的不可变参数快照。
type Params struct {
	N int      // 主分片数
	R int      // 路由槽数
	P int      // 路由分区大小，1 表示关闭
	H HashFunc // 哈希函数
}

// EffectiveRouting 返回参与哈希的 routing：为空时以 id 充当。
// 调用方须保证 P>1 时 routing 非空。
func EffectiveRouting(id, routing []byte) []byte {
	if len(routing) > 0 {
		return routing
	}
	return id
}

// Slot 返回路由槽编号（[0,R)）：slot = (h(routing) + (h(id) mod P)) mod R。
// 加法在 uint64 中进行，h 接近 2^32 也不回绕；P==1 时偏移恒为 0。
func (p Params) Slot(id, routing []byte) int {
	effective := EffectiveRouting(id, routing)
	offset := uint64(0)
	if p.P > 1 {
		offset = uint64(p.H(id)) % uint64(p.P)
	}
	sum := uint64(p.H(effective)) + offset
	return int(sum % uint64(p.R))
}

// Shard 返回分片编号（[0,N)）：slot 整除 (R/N)。
func (p Params) Shard(id, routing []byte) int {
	return p.Slot(id, routing) / (p.R / p.N)
}

// SearchShards 返回给定 routing 的文档可能落入的全部分片（升序去重）。
// P==1 时恰为一个；否则枚举槽 h(routing)+0..P-1（各自 mod R，允许跨 R 回绕）。
// routing 为空一律报 ErrMissingRouting。
func SearchShards(p Params, routing []byte) ([]int, error) {
	if len(routing) == 0 {
		return nil, ErrMissingRouting
	}
	base := uint64(p.H(routing))
	step := p.R / p.N
	seen := make(map[int]struct{}, p.P)
	order := make([]int, 0, p.P)
	for off := 0; off < p.P; off++ {
		s := int((base + uint64(off)) % uint64(p.R))
		shard := s / step
		if _, ok := seen[shard]; !ok {
			seen[shard] = struct{}{}
			order = append(order, shard)
		}
	}
	sort.Ints(order)
	return order, nil
}
