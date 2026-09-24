package norm

import "ontology/span"

func (n *Normalizer) applyTrailing() error {
	trailing := 0
	for trailing < len(n.out) && n.out[len(n.out)-1-trailing] == '\n' {
		trailing++
	}
	nonEmpty := n.ap > 0
	addOne := func() bool {
		if n.cfg.OutputLimit > 0 && n.op+1 > n.cfg.OutputLimit {
			return false
		}
		n.segs = append(n.segs, span.Seg{A0: n.ap, O0: n.op, OutLen: 1})
		n.out, n.op = append(n.out, '\n'), n.op+1
		return true
	}
	switch n.cfg.Mode {
	case EnsureOne:
		if nonEmpty && trailing == 0 && !addOne() {
			return n.fail(ErrOutputLimit, n.fed)
		}
	case Trim:
		switch {
		case !nonEmpty:
		case trailing == 0:
			if !addOne() {
				return n.fail(ErrOutputLimit, n.fed)
			}
		case trailing > 1:
			cut := trailing - 1
			n.out = n.out[:len(n.out)-cut]
			for cut > 0 {
				s := &n.segs[len(n.segs)-1]
				take := s.OutLen
				if take > cut {
					take = cut
				}
				if take == 0 {
					n.segs = n.segs[:len(n.segs)-1]
					continue
				}
				s.OutLen -= take
				n.op -= take
				cut -= take
				if s.OrigLen == 0 && s.OutLen == 0 {
					n.segs = n.segs[:len(n.segs)-1]
				}
			}
		}
	}
	return nil
}

// Finish 对已按 Keep 规范化（未做末尾策略）的段与输出套用 cfg 的末尾策略，
// 返回最终输出与映射。供 par 在拼接后调用。
func Finish(out []byte, segs []span.Seg, origLen int, cfg Config) ([]byte, *span.Map, error) {
	n := &Normalizer{cfg: cfg, out: out, segs: segs, ap: origLen,
		op: len(out), fed: origLen, wsRel: -1, done: true}
	if err := n.applyTrailing(); err != nil {
		return n.out, span.Build(n.segs), err
	}
	return n.out, span.Build(n.segs), nil
}

// Output 返回已产出的规范化字节（终态后保留已输出内容）。
func (n *Normalizer) Output() []byte { return n.out }

// Map 根据当前段构造映射。
func (n *Normalizer) Map() *span.Map { return span.Build(n.segs) }

// Segs 暴露当前段（供 par 拼接）。
func (n *Normalizer) Segs() []span.Seg { return n.segs }

// Normalize 一次性规范化 data。
func Normalize(data []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(data); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
