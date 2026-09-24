// Package patch 在目标端应用补丁：先完整校验，再一次性落地。
package patch

import (
	"ontology/chunk"
	"ontology/verify"
)

// Apply 把 raw 补丁应用到 target，返回与源端一致的新数据。
// 目标数据从不被原地修改：先完整解析并校验 CRC 与指令越界，
// 在新切片上构建结果，最后比对整体强校验和；任何一步失败都返回
// 可判定错误，调用方持有的 target 保持逐字节不变。
func Apply(target []byte, raw []byte) ([]byte, error) {
	p, err := verify.Parse(raw)
	if err != nil {
		return nil, err
	}
	if p.BlockSize <= 0 {
		return nil, chunk.ErrBlockSize
	}
	blocks := chunk.FullBlocks(len(target), p.BlockSize)
	if err := verify.ValidateOps(p, blocks); err != nil {
		return nil, err
	}
	result := make([]byte, 0, p.SrcLen)
	for _, op := range p.Ops {
		if op.Reuse {
			lo := op.Index * p.BlockSize
			result = append(result, target[lo:lo+p.BlockSize]...)
		} else {
			result = append(result, op.Data...)
		}
	}
	if err := verify.CheckResult(p, result); err != nil {
		return nil, err
	}
	return result, nil
}
