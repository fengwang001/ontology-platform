// Package scan 前缀范围扫描：只用块目录的首末值筛出候选块，
// 未命中块不解压（由 dict 的块解压计数器佐证）。
package scan

import (
	"strings"

	"ontology/dict"
)

// Prefix 返回字典中所有以 prefix 开头的条目，结果有序。
func Prefix(d *dict.Dict, prefix string) ([]string, error) {
	upper := successor(prefix)
	var out []string
	for b := 0; b < d.NumBlocks(); b++ {
		if d.BlockLast(b) < prefix {
			continue
		}
		if upper != "" && d.BlockFirst(b) >= upper {
			continue
		}
		entries, err := d.DecodeBlock(b)
		if err != nil {
			return out, err
		}
		for _, s := range entries {
			if strings.HasPrefix(s, prefix) {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// successor 返回大于所有以 p 为前缀的串的最小串；不存在（全 0xff 或
// 空串）时返回 ""，表示无上界。按字节计算，与块格式的前缀口径一致。
func successor(p string) string {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}
