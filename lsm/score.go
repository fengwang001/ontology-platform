package lsm

import "math/big"

// score 是精确有理数分数，内部用 big.Rat 表示，
// 比较不会发生浮点舍入，保证相同输入序列重放得到相同结果。
type score struct {
	r *big.Rat
}

// l0Score 零层分数：文件数 / 触发阈值。
func l0Score(fileCount int64, trigger int64) score {
	return score{r: big.NewRat(fileCount, trigger)}
}

// levelScore 非零层分数：本层总字节 / 本层目标字节。
func levelScore(totalBytes int64, targetBytes *big.Int) score {
	num := new(big.Rat).SetInt64(totalBytes)
	den := new(big.Rat).SetInt(targetBytes)
	return score{r: num.Quo(num, den)}
}

// atLeastOne 报告分数是否 >= 1。
func (s score) atLeastOne() bool {
	return s.r.Cmp(big.NewRat(1, 1)) >= 0
}

// cmp 精确比较两个分数：-1 小于、0 相等、+1 大于。
func (s score) cmp(o score) int {
	return s.r.Cmp(o.r)
}

// String 以 "分子/分母" 形式输出，便于日志与计划解释精确复现。
func (s score) String() string {
	return s.r.RatString()
}
