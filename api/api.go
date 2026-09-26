// Package api 是对外门面：字符串编解码与自检。依赖 codec。
package api

import (
	"bytes"
	"fmt"

	"ontology/b64"
	"ontology/codec"
)

// API 是对外句柄，无状态，可并发使用。
type API struct{}

// New 构造一个 API。
func New() *API { return &API{} }

// EncodeString 编码字符串为 Base64 文本。
func (a *API) EncodeString(s string) string { return string(b64.Encode([]byte(s))) }

// DecodeString 严格解码 Base64 文本，非法输入整体拒绝。
func (a *API) DecodeString(s string) (string, error) {
	b, err := b64.Decode([]byte(s))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SelfCheck 对内置向量核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	// 不变量 2（规范输出，与参照表一致）。
	vectors := []struct{ in, want string }{
		{"", ""}, {"f", "Zg=="}, {"fo", "Zm8="}, {"foo", "Zm9v"},
		{"foob", "Zm9vYg=="}, {"fooba", "Zm9vYmE="}, {"foobarb", "Zm9vYmFyYg=="},
	}
	for _, v := range vectors {
		if got := a.EncodeString(v.in); got != v.want {
			return fmt.Errorf("selfcheck: EncodeString(%q)=%q, 应为 %q", v.in, got, v.want)
		}
	}
	// 不变量 1（往返一致，覆盖 0..8 及以上各长度）。
	for n := 0; n <= 64; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*73 + n)
		}
		d, err := b64.Decode(b64.Encode(b))
		if err != nil || !bytes.Equal(d, b) {
			return fmt.Errorf("selfcheck: 往返不一致 n=%d", n)
		}
	}
	// 不变量 4（失败不留痕：整体拒绝且不返回部分结果）。
	for _, s := range []string{"Zm$v", "TWE", "Zm=v", "Z===", "Zm9="} {
		if r, err := b64.Decode([]byte(s)); err == nil || r != nil {
			return fmt.Errorf("selfcheck: %q 未被整体拒绝", s)
		}
	}
	// 经 codec 逐块解码的拼接与整段解码一致。
	raw := []byte("foobarb")
	enc := b64.Encode(raw)
	var joined []byte
	for k := 0; k < codec.BlockCount(enc); k++ {
		blk, err := codec.DecodeBlockAt(enc, k)
		if err != nil {
			return fmt.Errorf("selfcheck: DecodeBlockAt(%d): %w", k, err)
		}
		joined = append(joined, blk...)
	}
	if !bytes.Equal(joined, raw) {
		return fmt.Errorf("selfcheck: 逐块拼接 %q != 整段 %q", joined, raw)
	}
	return nil
}
