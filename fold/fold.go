// Package fold 处理字段折行（obs-fold）的展开。依赖 token 包。
package fold

import (
	"bytes"
	"errors"

	"ontology/token"
)

var (
	// ErrContinuationFirst 表示第一条物理行就是续行（以空白开头）。
	ErrContinuationFirst = errors.New("fold: first line is a continuation line")
	// ErrExpectedContinuation 表示在折行阶段遇到非续随行（未以空白开头）。
	ErrExpectedContinuation = errors.New("fold: expected continuation line")
	// ErrNoLines 表示没有任何物理行可展开。
	ErrNoLines = errors.New("fold: no lines")
)

// IsContinuation 报告物理行（不含行尾 CRLF）是否为续行。
func IsContinuation(line []byte) bool {
	return len(line) > 0 && token.IsOWS(line[0])
}

// Unfold 把同一字段的若干物理行展开为单个值。
//
// 规则：第一行不得以空白开头（否则 ErrContinuationFirst）；后续每行都必须是
// 续行（否则 ErrExpectedContinuation）。每个续行的前导 OWS 运行压缩为一个
// SP 后并入上一行；展开不做首尾裁剪，也不校验字节，二者交由上层规范化与
// token.ValidateValue 完成，保证职责单一。
func Unfold(lines [][]byte) (string, error) {
	if len(lines) == 0 {
		return "", ErrNoLines
	}
	if IsContinuation(lines[0]) {
		return "", ErrContinuationFirst
	}
	var b bytes.Buffer
	b.Write(lines[0])
	for _, ln := range lines[1:] {
		if !IsContinuation(ln) {
			return "", ErrExpectedContinuation
		}
		i := 0
		for i < len(ln) && token.IsOWS(ln[i]) {
			i++
		}
		b.WriteByte(' ')
		b.Write(ln[i:])
	}
	return b.String(), nil
}
