package frame

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"math/rand"
	"testing"
)

// 朴素模型：严格按规格条文逐步重写，与被测实现交叉对照。

type model struct {
	F, S  int
	f     int
	skip  int
	curS  int
	probe bool
	buf   []byte
	out   []byte
	hdr   bool
	log   []string
}

func newModel(F, S int) *model { return &model{F: F, S: S, curS: S} }

func modelCompress(src []byte) []byte {
	var out, lit []byte
	flush := func() {
		if len(lit) > 0 {
			out = append(out, byte(len(lit)-1))
			out = append(out, lit...)
			lit = nil
		}
	}
	for i := 0; i < len(src); {
		r := 1
		for i+r < len(src) && src[i+r] == src[i] {
			r++
		}
		if r >= 3 {
			flush()
			n := r
			if n > 130 {
				n = 130
			}
			out = append(out, byte(128+n-3), src[i])
			i += n
		} else {
			for ; r > 0; r-- {
				lit = append(lit, src[i])
				i++
				if len(lit) == 128 {
					flush()
				}
			}
		}
	}
	flush()
	return out
}

func (m *model) write(p []byte) {
	for len(p) > 0 {
		k := 65536 - len(m.buf)
		if k > len(p) {
			k = len(p)
		}
		m.buf = append(m.buf, p[:k]...)
		p = p[k:]
		if len(m.buf) == 65536 {
			m.chunk()
		}
	}
}

func (m *model) flush() {
	if len(m.buf) > 0 {
		m.chunk()
	}
}

func (m *model) close() {
	m.flush()
	if !m.hdr {
		m.out = append(m.out, identifierChunk...)
		m.hdr = true
	}
}

func (m *model) chunk() {
	if !m.hdr {
		m.out = append(m.out, identifierChunk...)
		m.hdr = true
	}
	n := len(m.buf)
	typ := byte(0x01)
	var data []byte
	var why string
	switch {
	case m.skip > 0:
		m.skip--
		why = fmt.Sprintf("skip 期间原样（skip→%d）", m.skip)
		if m.skip == 0 {
			m.probe = true
			why += "，置 probe"
		}
	case n < 16:
		why = "n<16 原样，状态不变"
	default:
		comp := modelCompress(m.buf)
		if len(comp) < n-n/8 {
			typ = 0x00
			data = comp
			m.f, m.probe, m.curS = 0, false, m.S
			why = fmt.Sprintf("压缩收益 %d < %d，压缩，f=0 curS=S", len(comp), n-n/8)
		} else if m.probe {
			m.probe = false
			m.curS = 2 * m.curS
			if m.curS > 8*m.S {
				m.curS = 8 * m.S
			}
			m.skip, m.f = m.curS, 0
			why = fmt.Sprintf("探测失败，curS→%d skip=%d", m.curS, m.skip)
		} else {
			m.f++
			why = fmt.Sprintf("失败 f=%d", m.f)
			if m.f == m.F {
				m.skip, m.f = m.curS, 0
				why += fmt.Sprintf("=F，skip=%d", m.skip)
			}
		}
	}
	if data == nil {
		data = m.buf
	}
	m.log = append(m.log, fmt.Sprintf("块 n=%d 类型=%02X：%s", n, typ, why))
	m.out = append(m.out, typ, byte(4+len(data)), byte((4+len(data))>>8), byte((4+len(data))>>16))
	crc := crc32.Checksum(m.buf, castagnoli)
	sum := ((crc >> 15) | (crc << 17)) + 0xa282ead8
	m.out = append(m.out, byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
	m.out = append(m.out, data...)
	m.buf = m.buf[:0]
}

// randomData 生成各种形态的输入。
func randomData(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	switch rng.Intn(5) {
	case 0: // 全相同字节
		for i := range b {
			b[i] = byte(rng.Intn(256))
		}
		if n > 0 {
			c := byte(rng.Intn(256))
			for i := range b {
				b[i] = c
			}
		}
	case 1: // 纯随机（不可压缩）
		rng.Read(b)
	case 2: // 相邻互不相同
		for i := range b {
			b[i] = byte(i)
		}
	case 3: // 随机 + 随机游程
		for i := 0; i < n; {
			if rng.Intn(2) == 0 {
				r := 1 + rng.Intn(200)
				c := byte(rng.Intn(256))
				for ; r > 0 && i < n; r-- {
					b[i] = c
					i++
				}
			} else {
				b[i] = byte(rng.Intn(256))
				i++
			}
		}
	default: // 小段重复图案
		pat := make([]byte, 1+rng.Intn(7))
		rng.Read(pat)
		for i := range b {
			b[i] = pat[i%len(pat)]
		}
	}
	return b
}

func TestModelCrossCheck(t *testing.T) {
	for iter := 0; iter < 2000; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)*7919 + 13))
		F, S := 1+rng.Intn(5), 1+rng.Intn(6)
		w, err := NewWriter(F, S)
		if err != nil {
			t.Fatal(err)
		}
		m := newModel(F, S)
		var total []byte
		nw := 1 + rng.Intn(8)
		for k := 0; k < nw; k++ {
			var n int
			switch rng.Intn(20) {
			case 0:
				n = 65536 + rng.Intn(3) - 1 // 跨越整块边界
			case 1, 2:
				n = rng.Intn(17) // 小区间，覆盖 n<16
			default:
				n = rng.Intn(400)
			}
			p := randomData(rng, n)
			total = append(total, p...)
			// 随机切分 Write。
			for len(p) > 0 {
				c := 1 + rng.Intn(len(p))
				if _, err := w.Write(p[:c]); err != nil {
					t.Fatal(err)
				}
				m.write(p[:c])
				p = p[c:]
			}
			if rng.Intn(2) == 0 {
				if err := w.Flush(); err != nil {
					t.Fatal(err)
				}
				m.flush()
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		m.close()

		got := w.Bytes()
		if !bytes.Equal(got, m.out) {
			t.Fatalf("iter %d (F=%d,S=%d): 输出与模型不一致\n输入总長=%d\n模型判定:\n%s",
				iter, F, S, len(total), joinLines(m.log))
		}
		t.Logf("iter %d F=%d S=%d 输入=%d 字节 输出=%d 字节", iter, F, S, len(total), len(got))
		for _, line := range m.log {
			t.Logf("  %s", line)
		}

		// 读取端按随机切分喂入，解出须等于总输入。
		r := NewReader()
		var dec []byte
		rest := got
		for len(rest) > 0 {
			c := 1 + rng.Intn(len(rest))
			d, err := r.Feed(rest[:c])
			if err != nil {
				t.Fatalf("iter %d Feed: %v", iter, err)
			}
			dec = append(dec, d...)
			rest = rest[c:]
		}
		if err := r.Close(); err != nil {
			t.Fatalf("iter %d reader Close: %v", iter, err)
		}
		if !bytes.Equal(dec, total) {
			t.Fatalf("iter %d: 解出 %d 字节，输入 %d 字节", iter, len(dec), len(total))
		}
		if r.maxBuffered > maxBuffered {
			t.Fatalf("iter %d: maxBuffered=%d 超限", iter, r.maxBuffered)
		}
	}
}

func joinLines(lines []string) string {
	s := ""
	for _, l := range lines {
		s += "  " + l + "\n"
	}
	return s
}
