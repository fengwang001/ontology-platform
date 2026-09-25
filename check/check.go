// Package check 提供 O(n^2) 朴素参照，用于校验 cnt 的计数结果。
package check

import "ontology/cnt"

// NaiveCount 用双重循环直接统计逆序对，作为朴素参照。
func NaiveCount(input []int) int64 {
	var inversions int64
	for i := 0; i < len(input); i++ {
		for j := i + 1; j < len(input); j++ {
			if input[i] > input[j] {
				inversions++
			}
		}
	}
	return inversions
}

// Agrees 报告 cnt 的结果与朴素参照是否一致，且 cnt 不返回错误。
func Agrees(input []int) bool {
	got, err := cnt.CountInversions(input)
	if err != nil {
		return false
	}
	return got == NaiveCount(input)
}
