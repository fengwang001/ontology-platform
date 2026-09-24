package patch

import (
	"bytes"
	"os"
	"path/filepath"

	"ontology/chunk"
	"ontology/diff"
	"ontology/verify"
)

// ApplyBytes 在内存中重建并做完整性校验；校验失败返回错误。
func ApplyBytes(p *diff.Patch, target []byte) ([]byte, error) {
	blocks, err := chunk.Split(target, p.BlockSize)
	if err != nil {
		return nil, err
	}
	var rebuilt bytes.Buffer
	rebuilt.Grow(p.SourceLen)
	for _, in := range p.Instrs {
		switch in.Op {
		case diff.OpRef:
			if in.Block < 0 || in.Block >= len(blocks) {
				return nil, verify.ErrBlockOutOfRange
			}
			rebuilt.Write(blocks[in.Block].Data)
		case diff.OpLit:
			rebuilt.Write(in.Data)
		}
	}
	if err := verify.Check(p, len(blocks), rebuilt.Bytes()); err != nil {
		return nil, err
	}
	return rebuilt.Bytes(), nil
}

// ApplyFile 将编码后的补丁应用到路径 path：先整份解析、重建、校验，
// 全部通过后用临时文件 + rename 原子替换；任一步失败原文件逐字节不变。
func ApplyFile(patchBytes []byte, path string) error {
	target, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p, err := verify.Decode(patchBytes)
	if err != nil {
		return err
	}
	result, err := ApplyBytes(p, target)
	if err != nil {
		return err
	}
	dir, name := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, ".patch-"+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(result); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
