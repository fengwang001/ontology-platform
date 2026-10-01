package fat12

import (
	"fmt"
	"testing"
)

// model 是按题意逐项 uint16 数组实现的朴素参考模型。
type model struct {
	c     int
	max   int
	fat   []uint16
	files map[int]struct{}
	rover int

	t     *testing.T
	steps []string
}

func newModel(t *testing.T, c int) *model {
	m := &model{
		c:     c,
		max:   c + 1,
		fat:   make([]uint16, c+2),
		files: make(map[int]struct{}),
		rover: 2,
		t:     t,
	}
	m.fat[0] = 0xFF8
	m.fat[1] = 0xFFF
	m.log("New(%d) => rover=%d image=%x", c, m.rover, m.image())
	return m
}

func (m *model) log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.steps = append(m.steps, msg)
	m.t.Log(msg)
}

// image 按 12 位朴素编码生成字节映像。
func (m *model) image() []byte {
	n := len(m.fat)
	buf := make([]byte, (n*3+1)/2)
	for i, v := range m.fat {
		o := i + i/2
		if i&1 == 0 {
			buf[o] = byte(v)
			buf[o+1] = buf[o+1]&0xF0 | byte(v>>8)&0x0F
		} else {
			buf[o] = buf[o]&0x0F | byte(v<<4)&0xF0
			buf[o+1] = byte(v >> 4)
		}
	}
	return buf
}

func (m *model) chain(h int) []int {
	out := []int{h}
	for {
		v := m.fat[h]
		if v >= 0xFF8 {
			return out
		}
		h = int(v)
		out = append(out, h)
	}
}

func (m *model) exists(h int) bool {
	_, ok := m.files[h]
	return ok
}

func (m *model) free() int {
	n := 0
	for c := 2; c <= m.max; c++ {
		if m.fat[c] == 0 {
			n++
		}
	}
	return n
}

// 以下为模型操作，返回 (值, 错误字符串或 "")，与实现走完全相同的规则。

func (m *model) create(n int) (int, string) {
	if n < 1 {
		m.log("Create(%d) => ErrInvalid", n)
		return 0, "invalid"
	}
	if m.free() < n {
		m.log("Create(%d) => ErrNoSpace free=%d", n, m.free())
		return 0, "nospace"
	}
	cs := m.alloc(n)
	for i := 0; i+1 < len(cs); i++ {
		m.fat[cs[i]] = uint16(cs[i+1])
	}
	m.fat[cs[len(cs)-1]] = 0xFFF
	m.files[cs[0]] = struct{}{}
	m.advance(cs[len(cs)-1])
	m.log("Create(%d) => %v handle=%d rover=%d", n, cs, cs[0], m.rover)
	return cs[0], ""
}

func (m *model) extend(h, n int) string {
	if n < 1 {
		m.log("Extend(%d,%d) => ErrInvalid", h, n)
		return "invalid"
	}
	if !m.exists(h) {
		m.log("Extend(%d,%d) => ErrNotFound", h, n)
		return "notfound"
	}
	if m.free() < n {
		m.log("Extend(%d,%d) => ErrNoSpace free=%d", h, n, m.free())
		return "nospace"
	}
	cs := m.alloc(n)
	old := m.chain(h)
	tail := old[len(old)-1]
	m.fat[tail] = uint16(cs[0])
	for i := 0; i+1 < len(cs); i++ {
		m.fat[cs[i]] = uint16(cs[i+1])
	}
	m.fat[cs[len(cs)-1]] = 0xFFF
	m.advance(cs[len(cs)-1])
	m.log("Extend(%d,%d) => %v rover=%d", h, n, cs, m.rover)
	return ""
}

func (m *model) truncate(h, k int) string {
	if k < 1 {
		m.log("Truncate(%d,%d) => ErrInvalid", h, k)
		return "invalid"
	}
	if !m.exists(h) {
		m.log("Truncate(%d,%d) => ErrNotFound", h, k)
		return "notfound"
	}
	ch := m.chain(h)
	if k > len(ch) {
		m.log("Truncate(%d,%d) => ErrOutOfRange len=%d", h, k, len(ch))
		return "outofrange"
	}
	rel := ch[k:]
	m.fat[ch[k-1]] = 0xFFF
	for _, c := range rel {
		m.fat[c] = 0
	}
	m.retreat(rel)
	m.log("Truncate(%d,%d) => released=%v rover=%d", h, k, rel, m.rover)
	return ""
}

func (m *model) del(h int) string {
	if !m.exists(h) {
		m.log("Delete(%d) => ErrNotFound", h)
		return "notfound"
	}
	ch := m.chain(h)
	for _, c := range ch {
		m.fat[c] = 0
	}
	delete(m.files, h)
	m.retreat(ch)
	m.log("Delete(%d) => released=%v rover=%d", h, ch, m.rover)
	return ""
}

func (m *model) markBad(c int) string {
	if c < 2 || c > m.max {
		m.log("MarkBad(%d) => ErrInvalid", c)
		return "invalid"
	}
	if m.fat[c] != 0 {
		m.log("MarkBad(%d) => ErrNotFree val=%03X", c, m.fat[c])
		return "notfree"
	}
	m.fat[c] = 0xFF7
	m.log("MarkBad(%d) => bad rover=%d", c, m.rover)
	return ""
}

func (m *model) defrag(h int) (int, string) {
	if !m.exists(h) {
		m.log("Defrag(%d) => ErrNotFound", h)
		return 0, "notfound"
	}
	old := m.chain(h)
	l := len(old)
	oldSet := mapInts(old)
	newChain := make([]int, 0, l)
	for c := 2; c <= m.max && len(newChain) < l; c++ {
		if m.fat[c] == 0 || oldSet[c] {
			newChain = append(newChain, c)
		}
	}
	if eqInts(old, newChain) {
		m.log("Defrag(%d) => unchanged handle=%d rover=%d", h, h, m.rover)
		return h, ""
	}
	newSet := mapInts(newChain)
	rel := make([]int, 0)
	for _, c := range old {
		if !newSet[c] {
			rel = append(rel, c)
		}
	}
	for _, c := range rel {
		m.fat[c] = 0
	}
	for i := 0; i+1 < len(newChain); i++ {
		m.fat[newChain[i]] = uint16(newChain[i+1])
	}
	m.fat[newChain[len(newChain)-1]] = 0xFFF
	delete(m.files, h)
	m.files[newChain[0]] = struct{}{}
	m.retreat(rel)
	m.log("Defrag(%d) => old=%v new=%v released=%v handle=%d rover=%d",
		h, old, newChain, rel, newChain[0], m.rover)
	return newChain[0], ""
}

func (m *model) alloc(n int) []int {
	start := -1
	run := 0
	for c := m.rover; c <= m.max; c++ {
		if m.fat[c] == 0 {
			if run == 0 {
				start = c
			}
			run++
			if run == n {
				return span(start, n)
			}
			continue
		}
		start, run = -1, 0
	}
	out := make([]int, 0, n)
	for c, seen := m.rover, 0; seen < m.c; seen++ {
		if m.fat[c] == 0 {
			out = append(out, c)
			if len(out) == n {
				return out
			}
		}
		c++
		if c > m.max {
			c = 2
		}
	}
	return out
}

func (m *model) advance(last int) {
	m.rover = last + 1
	if m.rover > m.max {
		m.rover = 2
	}
}

func (m *model) retreat(rel []int) {
	if len(rel) == 0 {
		return
	}
	min := rel[0]
	for _, c := range rel[1:] {
		if c < min {
			min = c
		}
	}
	if min < m.rover {
		m.rover = min
	}
}

func span(start, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = start + i
	}
	return out
}

func mapInts(xs []int) map[int]bool {
	m := make(map[int]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
