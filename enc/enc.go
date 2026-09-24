// Package enc 在 dict 之上实现 changelog 的增量编码：put/ref/reset token 流、
// 字典边界重置，以及连续同码 ref 的 RLE 压缩，并提供可往返的 Decode。
package enc

import (
	"errors"

	"ontology/dict"
)

// ErrBadToken 表示待解码 token 流非法：ref 指向未知 code、put 的 code
// 越出 [0,K)、或 ref 的次数 m<1。它与配置非法、值非法两类错误互不相同。
var ErrBadToken = errors.New("enc: invalid token")

// Kind 标识 token 种类。
type Kind uint8

const (
	KindPut   Kind = iota // 新值登记：dict[Code]=Value
	KindRef               // 命中已有值：输出 dict[Code]，共 Count 次
	KindReset             // 字典边界重置：清空解码字典
)

// Token 是流中的一个不可变条目。Count 仅对 KindRef 有意义，为总次数（≥1）；
// Value 仅对 KindPut 有意义。
type Token struct {
	Kind  Kind
	Code  int
	Value string
	Count int
}

// Put 构造 put(code,value)。
func Put(code int, value string) Token { return Token{Kind: KindPut, Code: code, Value: value} }

// Ref 构造 ref(code)（count==1）或 ref(code,count)（count≥2，总次数）。
func Ref(code, count int) Token { return Token{Kind: KindRef, Code: code, Count: count} }

// Reset 构造边界重置 token。
func Reset() Token { return Token{Kind: KindReset} }

// Encoder 持有编码端字典与已发射的 token 流。
type Encoder struct {
	d    *dict.Dict
	toks []Token
}

// NewEncoder 创建容量 k（须 >0，由上层 api 校验）的编码器。
func NewEncoder(k int) *Encoder {
	return &Encoder{d: dict.New(k)}
}

// Append 增量追加一个非空 value，不重排既有 code。
//
// 命中：发射 ref，并与流末尾同码 ref 合并（RLE，Count 为总次数）；
// 未命中且字典已满：先发射 reset 再清空字典（put/reset 天然打断 run），
// 随后 code 从 0 重新起；未命中发射 put(code,value)。
func (e *Encoder) Append(value string) {
	if code, ok := e.d.Lookup(value); ok {
		if n := len(e.toks); n > 0 && e.toks[n-1].Kind == KindRef && e.toks[n-1].Code == code {
			e.toks[n-1].Count++
		} else {
			e.toks = append(e.toks, Ref(code, 1))
		}
		return
	}
	if e.d.Full() {
		e.toks = append(e.toks, Reset())
		e.d.Reset()
	}
	code := e.d.Add(value)
	e.toks = append(e.toks, Put(code, value))
}

// Tokens 返回 token 流的快照拷贝，调用方修改返回切片不影响编码器。
func (e *Encoder) Tokens() []Token {
	out := make([]Token, len(e.toks))
	copy(out, e.toks)
	return out
}

// Decode 按容量 k 校验并展开 token 流，逐元素还原原始追加序列。
//
// 任何坏 token 都使整体失败并返回 ErrBadToken；解码只写局部状态，
// 失败不留任何痕迹。reset 清空解码字典；put 设 dict[code]=value 并输出；
// ref(code) 输出一次，ref(code,m) 输出 m 次。
func Decode(k int, toks []Token) ([]string, error) {
	dm := make(map[int]string, k)
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		switch t.Kind {
		case KindReset:
			clear(dm)
		case KindPut:
			if t.Code < 0 || t.Code >= k {
				return nil, ErrBadToken
			}
			dm[t.Code] = t.Value
			out = append(out, t.Value)
		case KindRef:
			if t.Count < 1 || t.Code < 0 || t.Code >= k {
				return nil, ErrBadToken
			}
			v, ok := dm[t.Code]
			if !ok {
				return nil, ErrBadToken
			}
			for i := 0; i < t.Count; i++ {
				out = append(out, v)
			}
		default:
			return nil, ErrBadToken
		}
	}
	return out, nil
}
