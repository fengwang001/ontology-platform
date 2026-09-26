// Package api 是定界帧编解码的对外门面，依赖 frame（进而依赖 esc）。
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/esc"
	"ontology/frame"
)

// API 无状态，可并发使用。
type API struct{}

// New 返回一个可并发使用的 API 实例。
func New() *API { return &API{} }

// EncodeFrames 把帧列表编成字节流。
func (a *API) EncodeFrames(frames [][]byte) []byte { return frame.Encode(frames) }

// DecodeFrames 单趟解码整段流；任何错误整体失败，返回 (nil, error)。
func (a *API) DecodeFrames(b []byte) ([][]byte, error) { return frame.Decode(b) }

// naiveEncode 是教科书参照实现：逐字节转义，帧间插 '\n'。
func naiveEncode(frames [][]byte) []byte {
	var out []byte
	for _, f := range frames {
		for _, b := range f {
			switch b {
			case '\\':
				out = append(out, '\\', '\\')
			case '\n':
				out = append(out, '\\', 'n')
			default:
				out = append(out, b)
			}
		}
		out = append(out, '\n')
	}
	return out
}

// SelfCheck 对一组内置帧列表核验四条不变量：往返一致、转义可逆、
// 与朴素参照一致、失败不留痕。全部通过返回 nil。
func (a *API) SelfCheck() error {
	lists := [][][]byte{
		{},
		{nil, {}, []byte("hi"), []byte("a\nb"), []byte(`x\y`), []byte(`\n\`)},
		{[]byte("hi"), []byte("a\nb"), []byte(`x\y`)},
	}
	for i, frames := range lists {
		enc := a.EncodeFrames(frames)
		if !bytes.Equal(enc, naiveEncode(frames)) { // 不变量 3
			return fmt.Errorf("selfcheck[%d]: encode differs from naive reference", i)
		}
		back, err := a.DecodeFrames(enc)
		if err != nil || !equalFrames(back, frames) { // 不变量 1、2
			return fmt.Errorf("selfcheck[%d]: round trip failed: %v", i, err)
		}
	}
	// 不变量 4：失败不留痕
	bad := []struct {
		stream []byte
		want   error
	}{
		{[]byte("a\\xb\n"), esc.ErrInvalidEscape},
		{[]byte("ab\\\n"), esc.ErrDanglingEscape},
		{[]byte("ab"), frame.ErrMissingTerminator},
	}
	for i, c := range bad {
		got, err := a.DecodeFrames(c.stream)
		if !errors.Is(err, c.want) || got != nil {
			return fmt.Errorf("selfcheck bad[%d]: got %v err %v", i, got, err)
		}
	}
	r := frame.NewReader([]byte("ok\n\\x\n"))
	if _, err := r.NextFrame(); err != nil {
		return fmt.Errorf("selfcheck reader: %v", err)
	}
	if _, err := r.NextFrame(); err == nil || r.Pos() != 3 {
		return fmt.Errorf("selfcheck reader: cursor moved on error")
	}
	return nil
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
