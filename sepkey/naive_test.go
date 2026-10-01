package sepkey

import (
	"bytes"
	"fmt"
)

// 本文件是按题述规则逐字节写成的朴素实现，用于与 Builder 对拍。

func cloneBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}

// naiveSep 朴素生成非最后块的分隔键，并返回判定依据。
func naiveSep(last, next []byte) ([]byte, string) {
	d := 0
	for d < len(last) && d < len(next) && last[d] == next[d] {
		d++
	}
	if d == len(last) || d == len(next) {
		return cloneBytes(last), fmt.Sprintf("d=%d 等于 last 或 next 的长度, sep=last", d)
	}
	b := last[d]
	if b < 0xFF && b+1 < next[d] {
		sep := make([]byte, d+1)
		copy(sep, last[:d])
		sep[d] = b + 1
		return sep, fmt.Sprintf("d=%d b=0x%02X<0xFF 且 b+1=0x%02X < next[d]=0x%02X, sep=last[:%d]+[0x%02X]", d, b, b+1, next[d], d, b+1)
	}
	return cloneBytes(last), fmt.Sprintf("d=%d b=0x%02X next[d]=0x%02X, 不满足 b<0xFF 且 b+1<next[d], sep=last", d, b, next[d])
}

// naiveFinishSep 朴素生成最后一块的分隔键，并返回判定依据。
func naiveFinishSep(last []byte) ([]byte, string) {
	sep := cloneBytes(last)
	for i := 0; i < len(sep); i++ {
		if sep[i] != 0xFF {
			sep[i]++
			return sep[:i+1], fmt.Sprintf("首个非 0xFF 字节位于下标 %d, 加一并截断", i)
		}
	}
	return sep, "全为 0xFF, sep=last"
}

// naiveSeek 朴素线性扫描：返回第一个 sep >= key 的块下标，越界返回 -1。
func naiveSeek(seps [][]byte, key []byte) int {
	for i, s := range seps {
		if bytes.Compare(s, key) >= 0 {
			return i
		}
	}
	return -1
}
