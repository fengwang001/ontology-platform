// Package par 把大缓冲区按任意字节偏移切成 K 段并行规范化，
// 拼接输出与映射，结果与单线程逐字节相同（修正规则见 DESIGN.md 推导 4）。
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

type chunk struct {
	n    *norm.Norm
	base int
	data []byte
	skip bool // 首字节 \n 被前一段的 \r\n 合体消费
	err  error
}

// Normalize 按 cuts（升序、落在 [0,len(data)] 内）切分并行处理；策略在拼接后统一应用。
func Normalize(data []byte, cuts []int, opt norm.Options) ([]byte, *span.Map, error) {
	k := len(cuts) + 1
	bounds := append(append([]int{0}, cuts...), len(data))
	popt := opt
	popt.Policy = norm.Preserve
	chunks := make([]*chunk, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		c := &chunk{n: norm.New(popt), base: bounds[i], data: data[bounds[i]:bounds[i+1]]}
		chunks[i] = c
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c.err = c.n.Write(c.data)
		}()
	}
	wg.Wait()
	for _, c := range chunks {
		if c.err != nil {
			return nil, nil, shiftErr(c.err, c.base)
		}
	}
	for i, c := range chunks { // 段末待定状态修正（顺序、有界）
		if c.n.PendingCR() {
			j := nextNonEmpty(chunks, i+1)
			lf := j >= 0 && chunks[j].data[0] == '\n'
			if err := c.n.ResolveCR(lf); err != nil {
				return nil, nil, shiftErr(err, c.base)
			}
			if lf {
				chunks[j].skip = true
			}
		}
		if c.n.PendingWS() > 0 {
			if err := c.n.ResolveWS(!trailingWS(chunks, i+1)); err != nil {
				return nil, nil, shiftErr(err, c.base)
			}
		}
	}
	gm := &span.Map{}
	var out []byte
	for _, c := range chunks {
		o := c.n.Output()
		ubase, drop := len(out), 0
		if c.skip && len(o) > 0 {
			drop = 1
		}
		for ri, r := range c.n.Map().Runs() {
			if c.skip && ri == 0 { // 剥掉被合体的 \n：首区间可能已与后续 1:1 合并
				r = span.Run{Orig: r.Orig + 1, Out: r.Out + 1, OLen: r.OLen - 1, ULen: r.ULen - 1}
				if r.OLen == 0 && r.ULen == 0 {
					continue
				}
			}
			gm.Add(r.Orig+c.base, r.Out+ubase-drop, r.OLen, r.ULen)
		}
		out = append(out, o[drop:]...)
	}
	return norm.ApplyPolicy(out, gm, opt.Policy, len(data)), gm, nil
}

// nextNonEmpty 返回 i 之后第一个非空段的下标，没有则 -1。
func nextNonEmpty(chunks []*chunk, i int) int {
	for ; i < len(chunks); i++ {
		if len(chunks[i].data) > 0 {
			return i
		}
	}
	return -1
}

// trailingWS 判定 i 之前的待定空白串是否为行尾空白：
// 向后找第一个非空白字节，是行尾（或不存在）则为 true（应删除）。
func trailingWS(chunks []*chunk, i int) bool {
	for ; i < len(chunks); i++ {
		for _, b := range chunks[i].data {
			if b != ' ' && b != '\t' {
				return b == '\r' || b == '\n'
			}
		}
	}
	return true
}

func shiftErr(err error, base int) error {
	if at, ok := err.(*norm.ErrAt); ok {
		return &norm.ErrAt{Off: at.Off + base, Err: at.Err}
	}
	return err
}
