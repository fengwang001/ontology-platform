// Package grapheme 把 UTF-8 字符串单遍解码为码点序列并切出字素簇，
// 字节偏移全部由增量累加得到，不回扫。依赖 seg。
package grapheme

import (
	"errors"
	"sync/atomic"
	"unicode/utf8"

	"ontology/seg"
)

// ErrInvalidUTF8 是非法 UTF-8 输入的哨兵错误，可用 errors.Is 判定。
var ErrInvalidUTF8 = errors.New("grapheme: invalid utf-8")

// InvalidUTF8Error 携带首个非法字节的偏移，同时匹配 ErrInvalidUTF8。
type InvalidUTF8Error struct{ Offset int }

func (e *InvalidUTF8Error) Error() string {
	return ErrInvalidUTF8.Error() + " at byte offset " + itoa(e.Offset)
}
func (e *InvalidUTF8Error) Is(target error) bool { return target == ErrInvalidUTF8 }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Cluster 是一个字素簇：字节偏移左闭右开，Runes 为码点数。
type Cluster struct {
	Start, End, Runes int
}

// 非导出计数器：解码 rune 总次数与为求偏移回扫的字节总数。
// 不出现于任何公开接口；仅包内测试可直接读取。
var (
	decoded   atomic.Int64
	rescanned atomic.Int64
)

// Decode 单遍扫描 s，产出簇列表。空串返回空列表；非法 UTF-8 整体失败。
func Decode(s string) ([]Cluster, error) {
	n := len(s)
	clusters := make([]Cluster, 0, 8)
	start, runes := 0, 0 // 当前簇起始字节偏移与码点数
	var prev rune
	riRun := 0 // 到 prev 为止的连续 Regional 个数
	for i := 0; i < n; {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			return nil, &InvalidUTF8Error{Offset: i}
		}
		decoded.Add(1)
		if i > 0 && !seg.NoBreak(prev, r, riRun) {
			clusters = append(clusters, Cluster{Start: start, End: i, Runes: runes})
			start, runes = i, 0
		}
		if seg.Regional(r) {
			riRun++
		} else {
			riRun = 0
		}
		prev, runes = r, runes+1
		i += size
	}
	if n > 0 {
		clusters = append(clusters, Cluster{Start: start, End: n, Runes: runes})
	}
	return clusters, nil
}
