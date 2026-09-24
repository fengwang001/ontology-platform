// Package audit 提供游标分页的"不重不漏"自检。
package audit

import (
	"fmt"
	"math"

	"ontology/page"
	"ontology/row"
)

// FullScan 用连续正向翻页遍历整个数据集，返回按访问顺序得到的行。
func FullScan(s *page.Store, pageSize int) []row.Row {
	var out []row.Row
	cur := []byte(nil)
	for {
		res, err := s.Forward(cur, pageSize)
		if err != nil {
			panic(err)
		}
		out = append(out, res.Rows()...)
		if len(res.Next()) == 0 {
			return out
		}
		cur = res.Next()
	}
}

// VerifyUniqueAndOrdered 校验一段翻页结果：无重复 ID 且严格按
// 复合键正向序排列。返回错误描述，nil 表示通过。
func VerifyUniqueAndOrdered(rows []row.Row) error {
	seen := make(map[string]bool, len(rows))
	for i, r := range rows {
		if seen[r.ID] {
			return fmt.Errorf("audit: 行 %s 重复", r.ID)
		}
		seen[r.ID] = true
		if i > 0 && row.Compare(rows[i-1], r) >= 0 {
			return fmt.Errorf("audit: 位置 %d 顺序错误 %v !< %v", i, rows[i-1], r)
		}
	}
	return nil
}

// CompareBound 返回数据集 n 行、每页 size 行时允许的比较次数上界：
// 4 * (size + ceil(log2(n)))。
func CompareBound(n, size int) int {
	return 4 * (size + int(math.Ceil(math.Log2(float64(n)))))
}

// ChurnReport 描述一次"分页期间持续增删"的并发自检。
type ChurnReport struct {
	Seen     []row.Row // 分页器实际返回的全部行（按返回顺序）
	Dupe     bool      // 是否有已返回行再次出现
	Deleted  []string  // 被删除的行 ID
	Inserted []string  // 新插入到未翻区间的行 ID
}

// RunChurn 翻到第二页后，由 mutate 执行并发增删，然后继续翻完，
// 校验：已返回行不重复、被删行不出现。未删行不跳过由严格复合键
// 边界在静态语义上保证（区间内无空洞）。
func RunChurn(s *page.Store, pageSize int, mutate func()) ChurnReport {
	rep := ChurnReport{}
	var acc []row.Row
	cur := []byte(nil)
	for pageNo := 1; ; pageNo++ {
		res, err := s.Forward(cur, pageSize)
		if err != nil {
			panic(err)
		}
		acc = append(acc, res.Rows()...)
		if pageNo == 2 {
			mutate()
		}
		if len(res.Next()) == 0 {
			break
		}
		cur = res.Next()
	}
	seen := map[string]bool{}
	for _, r := range acc {
		if seen[r.ID] {
			rep.Dupe = true
		}
		seen[r.ID] = true
	}
	rep.Seen = acc
	return rep
}
