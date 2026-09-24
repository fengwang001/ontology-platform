// Package cycle 提供环形重命名的破环编排与临时名生成。
package cycle

import "strconv"

// TempPrefix 是临时名的统一前缀，后接递增整数。
const TempPrefix = "#rename-tmp-"

// Temp 生成一个未被占用的临时名。used 报告某名字是否不可用
// （已存在于命名空间、或为本批任一旧名/新名）。从 start 开始
// 递增尝试，返回可用名字与下一个起始值；命名空间有限而编号
// 无上界，故必在有限步内成功。
func Temp(used func(string) bool, start int) (string, int) {
	for i := start; ; i++ {
		cand := TempPrefix + strconv.Itoa(i)
		if !used(cand) {
			return cand, i + 1
		}
	}
}

// IsTemp 报告名字是否为临时名形态。
func IsTemp(n string) bool {
	return len(n) > len(TempPrefix) && n[:len(TempPrefix)] == TempPrefix
}

// Break 把 k 元环 nodes（nodes[i] 要改名为 nodes[i+1]，末元素
// 改回 nodes[0]）拆解为无覆盖的执行步骤，恰用临时名 tmp 一个，
// 通过 emit 按执行顺序吐出 (old, new) 步骤，共 k+1 步：
//
//	nodes[0]->tmp, nodes[k-1]->nodes[0], ..., nodes[1]->nodes[2], tmp->nodes[1]
func Break(nodes []string, tmp string, emit func(old, new string)) {
	k := len(nodes)
	if k < 2 {
		return
	}
	emit(nodes[0], tmp)
	for i := k - 1; i >= 1; i-- {
		emit(nodes[i], nodes[(i+1)%k])
	}
	emit(tmp, nodes[1])
}
