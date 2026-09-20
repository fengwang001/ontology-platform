// Package ontology 提供可复现的加权水塘抽样器。
//
// 本文件实现一个确定性的伪随机数发生器（splitmix64），
// 并记录消耗掉的随机数个数，供测试断言可复现性。
package ontology

// rng 是确定性的 splitmix64 伪随机数发生器。
// 它只依赖构造时给定的种子，不读时间、不用全局状态。
type rng struct {
	state uint64
	count uint64
}

func newRNG(seed uint64) *rng {
	return &rng{state: seed}
}

// nextUint64 返回下一个伪随机 uint64，并递增消耗计数。
func (r *rng) nextUint64() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	r.count++
	return z
}

// nextFloat64 返回 (0,1] 区间内的伪随机浮点数。
// 使用 (0,1] 而非 [0,1) 是为了保证取对数等运算不会碰到 0。
func (r *rng) nextFloat64() float64 {
	// 取高 53 位构造尾数，映射到 (0,1]。
	v := r.nextUint64() >> 11
	return float64(v+1) / (1 << 53)
}

// consumed 返回至今消耗的随机数个数。
func (r *rng) consumed() uint64 {
	return r.count
}
