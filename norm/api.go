// 末尾换行策略、整缓冲区便捷封装，以及供 par 拼接使用的段末待定状态钩子。
package norm

import (
	"ontology/eol"
	"ontology/span"
)

// Policy 是末尾换行策略。
type Policy int

const (
	Preserve      Policy = iota // 保留原样
	EnsureOne                   // 确保恰好一个末尾换行（空输入保持空）
	StripTrailing               // 删除全部末尾空行，非空时保留一个换行
)

// Normalize 一次性规范化整个缓冲区。
func Normalize(data []byte, opt Options) ([]byte, *span.Map, error) {
	n := New(opt)
	if _, err := n.Write(data); err != nil {
		return n.out, &n.m, err
	}
	if err := n.Close(); err != nil {
		return n.out, &n.m, err
	}
	return n.out, &n.m, nil
}

// PendingCR/PendingWS 报告段末待定状态（供 par 拼接）。
func (n *Norm) PendingCR() bool { return n.sc.Pending() }
func (n *Norm) PendingWS() int  { return n.wsb.Len() }

// ResolveCR 判定段末待定 \r；lf=true 表示下一段首字节是 \n（\r\n 合体）。
func (n *Norm) ResolveCR(lf bool) error {
	_, off := n.sc.Finish()
	n.sc = eol.Scanner{}
	if lf {
		return n.emit('\n', off, 2)
	}
	return n.emit('\n', off, 1)
}

// ResolveWS 判定段末待定空白（emit=true 输出，false 删除）。
func (n *Norm) ResolveWS(emit bool) error { return n.resolveWS(emit) }

// ApplyPolicy 对 Preserve 输出统一应用末尾换行策略并修正映射（见 DESIGN.md 推导 1）。
func ApplyPolicy(out []byte, m *span.Map, p Policy, origLen int) []byte {
	switch p {
	case EnsureOne:
		if len(out) == 0 {
			return out
		}
		k := 0
		for k < len(out) && out[len(out)-1-k] == '\n' {
			k++
		}
		if k > 0 { // 保留最后一个 \n 的真实映射，其余转为删除区间
			m.TruncateOut(len(out) - k + 1)
			return out[:len(out)-k+1]
		}
		m.Add(origLen, len(out), 0, 1) // 合成的换行映射到原文终点
		return append(out, '\n')
	case StripTrailing:
		i := len(out)
		for i > 0 && out[i-1] == '\n' {
			i--
		}
		if i < len(out) {
			m.TruncateOut(i)
			out = out[:i]
		}
		if len(out) == 0 {
			return out
		}
		m.Add(origLen, len(out), 0, 1)
		return append(out, '\n')
	}
	return out
}
