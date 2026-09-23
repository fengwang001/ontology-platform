package headers

import (
	"strings"

	"ontology/fold"
)

// Bytes 把集合按出现顺序回写成字节流。
// 每条头部按配置宽度重折行，统一 CRLF 行尾，以 CRLF 空行结束。
// 值在入库时已校验，回写不可能产生额外头部。
func (s *Set) Bytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bytesLocked()
}

// bytesLocked 是 Bytes 的无锁版本，调用者须持有锁或处于构建期。
func (s *Set) bytesLocked() []byte {
	var buf strings.Builder
	for _, e := range s.entries {
		if e.deleted {
			continue
		}
		line := e.name + ": " + e.value
		if e.value == "" {
			line = e.name + ":"
		}
		for _, line := range fold.Fold(line, s.cfg.Width) {
			buf.WriteString(line)
			buf.WriteString("\r\n")
		}
	}
	buf.WriteString("\r\n")
	return []byte(buf.String())
}
