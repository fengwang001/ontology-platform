package zlibstore

// adlerState 是流式 Adler-32 的两个模 65521 分量：
// a 从 1 起、b 从 0 起，每来一个字节先 a=(a+byte) mod 65521，
// 再 b=(b+a) mod 65521；最终校验和为 b<<16 | a。
type adlerState struct {
	a uint32
	b uint32
}

// adlerMod 是 Adler-32 使用的最大素数模数 65521。
const adlerMod = 65521

func newAdler() adlerState { return adlerState{a: 1, b: 0} }

// update 逐字节更新：先 a=(a+byte) mod 65521，再 b=(b+a) mod 65521。
// 每个中间值都小于 2*65521-1，单次条件减法即可归约，同时避免溢出。
func (s *adlerState) update(data []byte) {
	a, b := s.a, s.b
	for _, c := range data {
		a += uint32(c)
		if a >= adlerMod {
			a -= adlerMod
		}
		b += a
		for b >= adlerMod {
			b -= adlerMod
		}
	}
	s.a, s.b = a, b
}

func (s adlerState) sum() uint32 { return s.b<<16 | s.a }

// adler32 对整段数据一次性计算 Adler-32，供测试中的朴素对照使用。
func adler32(data []byte) uint32 {
	s := newAdler()
	s.update(data)
	return s.sum()
}
