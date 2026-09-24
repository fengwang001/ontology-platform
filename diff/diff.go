// Package diff 在源端依据目标端签名，用滚动窗口生成最小补丁。
package diff

import (
	"crypto/sha256"

	"ontology/chunk"
	"ontology/sig"
	"ontology/verify"
)

// Result 是一次 diff 的产物与计数。
type Result struct {
	Patch        verify.Patch
	WeakHits     int
	StrongCalls  int
	LiteralBytes int
	CopyCount    int
}

// Build 依据签名对源数据生成补丁。同弱和候选中，签名项按 index 升序，
// 首个强和通过者即为确定选择，故补丁只依赖输入、与并发/调度无关。
func Build(src []byte, s sig.Signature) (Result, error) {
	if s.BlockSize == 0 {
		return Result{}, verify.ErrBadBlockSize
	}
	n := int(s.BlockSize)
	table := s.Lookup()

	res := Result{Patch: verify.Patch{
		BlockSize: s.BlockSize,
		SrcLen:    uint64(len(src)),
		SrcHash:   sha256.Sum256(src),
	}}

	var lit []byte
	emitLit := func() {
		if len(lit) > 0 {
			res.Patch.Instrs = append(res.Patch.Instrs, verify.Instr{Op: verify.OpLit, Data: lit})
			res.LiteralBytes += len(lit)
			lit = nil
		}
	}

	sc, err := chunk.NewScanner(src, s.BlockSize)
	if err != nil {
		return Result{}, err
	}

	for sc.Valid() {
		pos := sc.Pos()
		cands, weakOK := table[sc.Weak()]
		if weakOK {
			sc.MarkWeakHit()
			strong := sc.ConfirmStrong()
			if idx, ok := matchBlock(cands, strong); ok {
				emitLit()
				res.Patch.Instrs = append(res.Patch.Instrs, verify.Instr{Op: verify.OpCopy, Idx: idx})
				res.CopyCount++
				// 跳过被复用的整个窗口，避免后续重复匹配。
				sc.SkipWindow()
				continue
			}
		}
		lit = append(lit, src[pos])
		sc.Advance()
	}

	// 处理末块不满（长度 < blockSize）的尾巴。
	if tail := sc.Tail(); len(tail) > 0 {
		if idx, ok := matchTail(table, tail); ok {
			emitLit()
			res.Patch.Instrs = append(res.Patch.Instrs, verify.Instr{Op: verify.OpCopy, Idx: idx})
			res.CopyCount++
		} else {
			lit = append(lit, tail...)
		}
	}
	emitLit()

	res.WeakHits = sc.WeakHits()
	res.StrongCalls = sc.StrongCalls()
	return res, nil
}

func matchBlock(cands []sig.Entry, strong [32]byte) (uint32, bool) {
	for _, e := range cands {
		if e.Strong == strong {
			return e.Index, true
		}
	}
	return 0, false
}

func matchTail(table map[uint32][]sig.Entry, tail []byte) (uint32, bool) {
	cands, ok := table[chunk.Weak(tail)]
	if !ok {
		return 0, false
	}
	h := chunk.Strong(tail)
	return matchBlock(cands, h)
}
