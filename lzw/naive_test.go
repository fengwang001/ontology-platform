package lzw

import "fmt"

// naiveCode 是朴素参考编码器产出的单个码及其输出码宽。
type naiveCode struct {
	code int
	bits uint
}

// naiveEncode 严格按题目规则逐步实现的参考编码器。
// 返回 (码序列, 打包字节)；码宽的每次变化都显式记录判定依据。
func naiveEncode(input []byte) ([]naiveCode, []byte) {
	tab := make(map[uint64]int)
	var seq []naiveCode
	free := firstFree
	width := uint(minBits)
	emit := func(code int) {
		seq = append(seq, naiveCode{code, width})
	}
	emit(ClearCode)
	w := -1
	reason := func(id int) {
		if id == 1<<width && width < maxBits {
			width++
		}
	}
	for _, c := range input {
		if w < 0 {
			w = int(c)
			continue
		}
		key := dictKey(w, c)
		if id, ok := tab[key]; ok {
			w = id
			continue
		}
		emit(w)
		id := free
		free++
		tab[key] = id
		reason(id)
		if id == maxCode {
			emit(ClearCode) // 当前码宽（12）
			tab = make(map[uint64]int)
			free = firstFree
			width = minBits
		}
		w = int(c)
	}
	if w >= 0 {
		emit(w)
		// Close：下一个空闲编号计为已占用（无内容），同样判定加宽。
		if free <= maxCode {
			id := free
			free++
			reason(id)
			if id == maxCode {
				emit(ClearCode)
				free = firstFree
				width = minBits
			}
		}
	}
	emit(EndCode)

	var out []byte
	var acc uint32
	var n uint
	for _, c := range seq {
		acc |= uint32(c.code) << n
		n += c.bits
		for n >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			n -= 8
		}
	}
	if n > 0 {
		out = append(out, byte(acc))
	}
	return seq, out
}

func codesString(seq []naiveCode) string {
	s := ""
	for i, c := range seq {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("%d/%d", c.code, c.bits)
	}
	return s
}
