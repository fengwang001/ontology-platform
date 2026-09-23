package headers

import (
	"bytes"

	"ontology/fold"
)

// Marshal 把集合回写为字节流：每行 CRLF，块以空行终止。
// 值在入口已校验，这里绝不可能写出 CR/LF/NUL 等结构字符；
// 配置 FoldWidth 时在已有空格处重折行，再解析值不变（往返无损）。
func (s *Set) Marshal() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b bytes.Buffer
	for _, e := range s.ents {
		for _, line := range fold.Fold(e.name, e.value, s.cfg.FoldWidth) {
			b.WriteString(line)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

// Clone 返回集合的深拷贝（含配置与策略表引用）。
func (s *Set) Clone() *Set {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := New(s.reg, s.cfg)
	out.ents = append(out.ents, s.ents...)
	for k, v := range s.index {
		out.index[k] = append([]int(nil), v...)
	}
	out.norm = s.norm
	return out
}
