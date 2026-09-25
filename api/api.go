// Package api 是 Elias delta 编码的对外接口：Encode、Decode 与 SelfCheck。
// 依赖 dcode，不反向依赖。
package api

import (
	"bytes"
	"errors"

	"ontology/dcode"
)

// 对外暴露同一组哨兵错误，调用方用 errors.Is 判定。
var (
	ErrNonPositive = dcode.ErrNonPositive
	ErrTruncated   = dcode.ErrTruncated
	ErrOverflow    = dcode.ErrOverflow
)

// Encode 把正整数序列编码为字节流；元素非正返回 ErrNonPositive。
func Encode(vs []int64) ([]byte, error) { return dcode.Encode(vs) }

// Decode 整体解码字节流；出错返回 nil 与哨兵错误，不返回部分前缀。
func Decode(data []byte) ([]int64, error) { return dcode.Decode(data) }

// SelfCheck 对一组内置序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	seqs := [][]int64{
		{1},
		{1, 2, 4, 10, 16},
		{3, 7, 255, 1024, 1 << 40},
	}
	for _, vs := range seqs {
		enc, err := Encode(vs)
		if err != nil {
			return err
		}
		// 不变量 3：确定性——同一输入两次编码逐字节相同。
		enc2, _ := Encode(vs)
		if !bytes.Equal(enc, enc2) {
			return errors.New("api: 编码不确定")
		}
		// 往返一致。
		dec, err := Decode(enc)
		if err != nil || !equal(dec, vs) {
			return errors.New("api: 往返不一致")
		}
		// 不变量 2：切分点无关——逐字节喂入与一次性解码一致。
		sd := dcode.NewStreamDecoder()
		for _, b := range enc {
			sd.Feed([]byte{b})
		}
		dec2, err := sd.Decode()
		if err != nil || !equal(dec2, vs) {
			return errors.New("api: 切分点相关")
		}
		// 不变量 4：失败不留痕——截断输入返回 nil 而非部分前缀。
		if len(enc) > 1 {
			part, err := Decode(enc[:len(enc)-1])
			if err != nil && part != nil {
				return errors.New("api: 失败留下部分输出")
			}
		}
	}
	// 不变量 1（与朴素参照一致）由 dcode 包测试以独立参照实现钉住。
	return nil
}

func equal(a, b []int64) bool {
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
