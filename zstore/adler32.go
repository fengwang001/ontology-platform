package zstore

const adlerMod = 65521

// adler32 按定义逐字节更新：a=1、b=0 起，
// 每个字节先 a=(a+字节) mod 65521，再 b=(b+a) mod 65521，
// 结果为 b<<16 | a。
type adler32 struct {
	a, b uint32
}

func newAdler32() adler32 {
	return adler32{a: 1, b: 0}
}

func (s *adler32) Write(p []byte) {
	a, b := s.a, s.b
	for _, c := range p {
		a = (a + uint32(c)) % adlerMod
		b = (b + a) % adlerMod
	}
	s.a, s.b = a, b
}

func (s adler32) Sum32() uint32 {
	return s.b<<16 | s.a
}
