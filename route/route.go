// Package route 只负责亲和键的哈希与算术路由，不依赖其他包。
package route

// Hash 返回 key 各字节值之和（单字节 ASCII 字符即其字节值）。
// 这是本题唯一指定的哈希：多字节 key 按其每个字节的值求和。
func Hash(key string) int {
	sum := 0
	for i := 0; i < len(key); i++ {
		sum += int(key[i])
	}
	return sum
}

// Home 返回 key 在 n 个分区中的 home 分区：hash(key) % n。
// 调用方保证 n > 0。
func Home(key string, n int) int {
	return Hash(key) % n
}
