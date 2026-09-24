// Package cycle 用临时名破解环形重命名。
package cycle

import (
	"fmt"

	"ontology/name"
)

// Break 把一个简单环展开为 k+1 个无覆盖步骤，返回步骤与所借临时名。
// cyc 必须满足 cyc[i].New == cyc[i+1].Old 且首尾相接（环）。
// used 报告名字是否被占用（命名空间、本批请求名、已用临时名）。
//
// 设环为 [n0->n1, n1->n2, ..., nk-1->n0]，展开为：
//
//	n0->tmp, nk-1->n0, nk-2->nk-1, ..., n1->n2, tmp->n1
//
// 每一步的目标名都恰在上一步被腾空（或原本就空闲），因此零覆盖。
func Break(cyc []name.Rename, used func(string) bool) ([]name.Rename, string) {
	tmp := Temp(used)
	steps := make([]name.Rename, 0, len(cyc)+1)
	steps = append(steps, name.Rename{Old: cyc[0].Old, New: tmp})
	for i := len(cyc) - 1; i >= 1; i-- {
		steps = append(steps, cyc[i])
	}
	steps = append(steps, name.Rename{Old: tmp, New: cyc[0].New})
	return steps, tmp
}

// Temp 生成不与 used 冲突的临时名；候选无限递增枚举，冲突则重试，
// 因此即使调用方预先占用了前若干个候选也必然成功。
func Temp(used func(string) bool) string {
	for i := 0; ; i++ {
		cand := fmt.Sprintf(".tmp-rename-%d", i)
		if !used(cand) {
			return cand
		}
	}
}
