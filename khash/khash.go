// Package khash 实现题目规定的按键哈希、桶映射与采样判定。
// 本包无状态、不依赖工程内其他包：判定只由 (key, rate) 决定，
// 因此同键必同结论、多个独立实例结论天然一致（不变量 2）。
package khash

// Buckets 是桶的总数：采样率以万分比表示。
const Buckets = 10000

// Hash 按规定公式计算 h(key)：初值 0，对 key 的每个 UTF-8 字节 b
// 执行 h = h*31 + b，uint64 自然溢出回绕。
// 例如 h("ab") = 97*31 + 98 = 3105。
func Hash(key string) uint64 {
	var h uint64
	for i := 0; i < len(key); i++ {
		h = h*31 + uint64(key[i])
	}
	return h
}

// Bucket 返回 bucket(key) = h(key) mod 10000，范围 [0,10000)。
func Bucket(key string) int {
	return int(Hash(key) % Buckets)
}

// Sampled 报告 key 在采样率 r（整数万分比）下是否被采样：
// 当且仅当 bucket(key) < r（严格小于）。
// 谓词随 r 单调不减，是不变量 3（调高不丢键）的根源。
func Sampled(key string, r int) bool {
	return Bucket(key) < r
}
