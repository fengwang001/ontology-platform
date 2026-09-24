// Package patch 在目标端校验并原子应用补丁。
package patch

import (
	"crypto/sha256"
	"os"
	"path/filepath"

	"ontology/verify"
)

// ApplyBytes 在内存中完整重放补丁并校验，任何失败都返回错误且不改动目标。
// 全部通过后返回结果；块越界返回 ErrBlockIndex，结果不符返回 ErrResultMismatch。
func ApplyBytes(target []byte, p verify.Patch) ([]byte, error) {
	n := int(p.BlockSize)
	if n == 0 {
		return nil, verify.ErrBadBlockSize
	}
	numBlocks := (len(target) + n - 1) / n

	var out []byte
	for _, in := range p.Instrs {
		switch in.Op {
		case verify.OpCopy:
			if int(in.Idx) >= numBlocks {
				return nil, verify.ErrBlockIndex
			}
			start := int(in.Idx) * n
			end := start + n
			if end > len(target) {
				end = len(target)
			}
			out = append(out, target[start:end]...)
		case verify.OpLit:
			out = append(out, in.Data...)
		default:
			return nil, verify.ErrSignature
		}
	}

	if uint64(len(out)) != p.SrcLen {
		return nil, verify.ErrResultMismatch
	}
	if sha256.Sum256(out) != p.SrcHash {
		return nil, verify.ErrResultMismatch
	}
	return out, nil
}

// ApplyFile 读取补丁、整体校验重放，成功后经临时文件 rename 原子替换目标。
// 失败时目标文件逐字节不变。
func ApplyFile(targetPath, patchPath string) error {
	raw, err := os.ReadFile(patchPath)
	if err != nil {
		return err
	}
	p, err := verify.Classify(raw)
	if err != nil {
		return err
	}
	target, err := os.ReadFile(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := ApplyBytes(target, p)
	if err != nil {
		return err
	}
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".patch-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, targetPath)
}
