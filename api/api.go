// Package api 对外暴露变长字段长度前缀编解码能力与自检。
package api

import (
	"bytes"
	"errors"

	"ontology/frame"
	"ontology/lenp"
)

// Codec 无状态，可并发使用。
type Codec struct{}

// New 返回一个可并发使用的编解码器。
func New() *Codec { return &Codec{} }

// MarshalFields 把字段列表编码成一条长度前缀记录。
func (c *Codec) MarshalFields(fields [][]byte) []byte { return frame.Encode(fields) }

// UnmarshalFields 把记录切分回字段列表；截断/非法前缀整体失败。
func (c *Codec) UnmarshalFields(buf []byte) ([][]byte, error) { return frame.Decode(buf) }

// SelfCheck 对内置字段列表核验四条不变量：往返一致、前缀自洽、与朴素参照一致、失败不留痕。
func (c *Codec) SelfCheck() error {
	lists := [][][]byte{
		{[]byte("hi"), {}, []byte("world!"), []byte("A")},
		{},
		{{}},
		{[]byte("x"), {}, {}, bytes.Repeat([]byte("z"), 3000)},
	}
	for _, fs := range lists {
		rec := c.MarshalFields(fs)
		got, err := c.UnmarshalFields(rec) // 不变量 1：往返一致
		if err != nil || len(got) != len(fs) {
			return errors.New("api: round trip failed")
		}
		for i := range fs {
			if !bytes.Equal(got[i], fs[i]) {
				return errors.New("api: round trip mismatch")
			}
		}
		if !bytes.Equal(rec, naiveEncode(fs)) { // 不变量 3：与朴素参照字节级一致
			return errors.New("api: encode differs from naive reference")
		}
		r := frame.NewReader(rec) // 不变量 2：前缀自洽、无重叠无空洞
		for r.Pos() < len(rec) {
			l, err := lenp.GetLength(rec[r.Pos():])
			f, err2 := r.NextField()
			if err != nil || err2 != nil || len(f) != l {
				return errors.New("api: prefix inconsistent with payload")
			}
		}
		if r.Pos() != len(rec) {
			return errors.New("api: fields do not cover whole buffer")
		}
		if len(rec) > 0 { // 不变量 4：失败不留痕
			if bad, err := c.UnmarshalFields(rec[:len(rec)-1]); bad != nil || err == nil {
				return errors.New("api: truncated decode must fail as a whole")
			}
		}
	}
	r := frame.NewReader(c.MarshalFields([][]byte{[]byte("ab")}))
	if err := r.SkipField(); err != nil {
		return err
	}
	if err := r.SkipField(); err == nil || r.Pos() != 6 { // 越界 SkipField 失败且不推进游标
		return errors.New("api: failed SkipField must not advance cursor")
	}
	return nil
}

// naiveEncode 教科书参照实现：每字段写 4 字节小端长度 + 负载。
func naiveEncode(fields [][]byte) []byte {
	var buf []byte
	for _, f := range fields {
		buf = append(buf, lenp.PutLength(len(f))...)
		buf = append(buf, f...)
	}
	return buf
}
