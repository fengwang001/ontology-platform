// Package par 把大缓冲区按任意字节偏移切成 K 段并行规范化后拼接（含映射拼接）。
package par

import (
	"errors"
	"sync"

	"ontology/norm"
	"ontology/span"
)

type frag struct {
	out []byte
	m   *span.Map
	err error
}

func isWS(b byte) bool { return b == ' ' || b == '\t' }

// Process 按 offsets（升序，可含 0 与 len(p)）把 p 切成 len(offsets)+1 段并发规范化，
// 再修正段缝并施加一次末尾策略；结果与单段 norm.Process 逐字节相同。
func Process(p []byte, offsets []int, cfg norm.Config) ([]byte, *span.Map, error) {
	bounds := append(append([]int{0}, offsets...), len(p))
	k := len(bounds) - 1
	res := make([]frag, k)
	var wg sync.WaitGroup
	wg.Add(k)
	for i := 0; i < k; i++ {
		go func(i int) {
			defer wg.Done()
			c := cfg
			c.Fragment, c.OutputLimit = true, 0
			out, m, err := norm.Process(p[bounds[i]:bounds[i+1]], c)
			res[i] = frag{out, m, err}
		}(i)
	}
	wg.Wait()
	for i := range res {
		var e *norm.OffsetError
		if errors.As(res[i].err, &e) {
			return nil, nil, &norm.OffsetError{Err: errors.Unwrap(e), Offset: e.Offset + bounds[i]}
		}
	}
	if err := seamWSCheck(p, bounds, cfg.WhitespaceLimit); err != nil {
		return nil, nil, err
	}

	gm := &span.Map{}
	var gout []byte
	oBase := 0
	for i := 0; i < k; i++ {
		res[i].m.Translate(bounds[i], oBase)
		gm.Concat(res[i].m)
		gout = append(gout, res[i].out...)
		oBase += len(res[i].out)
	}
	gout = applyDeletions(gm, gout, crossSeamDeletions(p))
	if cfg.OutputLimit > 0 && len(gout) > cfg.OutputLimit {
		return gout, gm, &norm.OffsetError{Err: norm.ErrOutputLimit, Offset: len(p)}
	}
	norm.ApplyEndPolicy(gm, &gout, cfg.End, len(p) > 0)
	return gout, gm, nil
}

// crossSeamDeletions 只在段缝附近收集需要"额外删除"的原文区间：
// 缝落在 \r|\n 中间时删该 \r；缝后的第一个非空白字节是行尾时，删缝前整段空白 run；
// 以及 EOF 末尾未判定的空白 run（与单段 Close 一致）。
func crossSeamDeletions(p []byte) [][2]int {
	var d [][2]int
	add := func(a, b int) {
		if b > a {
			d = append(d, [2]int{a, b})
		}
	}
	for i := 1; i < len(p); i++ {
		if p[i-1] == '\r' && p[i] == '\n' {
			add(i-1, i)
		}
		if isWS(p[i-1]) && (p[i] == '\r' || p[i] == '\n') {
			j := i - 1
			for j >= 0 && isWS(p[j]) {
				j--
			}
			add(j+1, i)
		}
	}
	j := len(p)
	for j > 0 && isWS(p[j-1]) {
		j--
	}
	add(j, len(p))
	return d
}

// applyDeletions 按原文坐标把删除区间逐个应用到映射，并裁掉对应的全局输出字节。
func applyDeletions(m *span.Map, out []byte, dels [][2]int) []byte {
	for z := len(dels) - 1; z >= 0; z-- {
		d := dels[z]
		from, to := d[0], d[1]
		o0, o1 := m.ToOut(from), m.ToOut(to)
		m.DeleteOrigRange(from, to)
		out = append(out[:o0], out[o1:]...)
	}
	return out
}

// seamWSCheck 检查"跨缝合并的空白 run"是否超缓冲上限（段内 run 已由各段判定）。
func seamWSCheck(p []byte, bounds []int, limit int) error {
	if limit <= 0 {
		return nil
	}
	isCut := func(i int) bool {
		for _, b := range bounds[1 : len(bounds)-1] {
			if b == i {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(p); i++ {
		if !isWS(p[i]) {
			continue
		}
		j := i
		for j < len(p) && isWS(p[j]) {
			j++
		}
		n := j - i
		// 该 run 只有被切点穿过时才可能逃过段内上限判定。
		cut := false
		for t := i + 1; t < j; t++ {
			if isCut(t) {
				cut = true
			}
		}
		if cut && n > limit {
			return &norm.OffsetError{Err: norm.ErrWhitespaceLimit, Offset: i + limit}
		}
		i = j - 1
	}
	return nil
}
