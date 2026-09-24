// Package verify 块损坏检测与字典不变量自检。
package verify

import (
	"fmt"

	"ontology/block"
	"ontology/dict"
)

// Block 完整校验一个块：头部、重启点表、条目、CRC、有序性、前缀长度。
// 返回的错误可用 errors.Is 与 block 包的哨兵错误匹配。
func Block(data []byte) error {
	_, err := block.Decode(data)
	return err
}

// Dict 校验字典不变量：每块可完整解码、块内条目数与目录一致、目录
// 首末值与解压结果一致、相邻块严格递增、条目总数与头部一致。
func Dict(d *dict.Dict) error {
	total := 0
	prevLast := ""
	for b := 0; b < d.NumBlocks(); b++ {
		entries, err := d.DecodeBlock(b)
		if err != nil {
			return fmt.Errorf("verify: block %d: %w", b, err)
		}
		if len(entries) != d.BlockCount(b) {
			return fmt.Errorf("verify: block %d count %d != %d", b, len(entries), d.BlockCount(b))
		}
		if entries[0] != d.BlockFirst(b) || entries[len(entries)-1] != d.BlockLast(b) {
			return fmt.Errorf("verify: block %d directory first/last mismatch", b)
		}
		if b > 0 && prevLast >= entries[0] {
			return fmt.Errorf("verify: blocks %d/%d not strictly increasing", b-1, b)
		}
		prevLast = entries[len(entries)-1]
		total += len(entries)
	}
	if total != d.Count {
		return fmt.Errorf("verify: total %d != header count %d", total, d.Count)
	}
	return nil
}
