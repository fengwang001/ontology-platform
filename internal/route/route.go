// Package route 只负责亲和键的算术定位：hash(key)= 各字节值之和，
// home = hash(key) % n。本包不依赖项目内任何其他包。
package route

// Hash 返回 key 各字节值之和（单字节 ASCII 字符即其字节值）。
func Hash(key string) int {
	sum := 0
	for i := 0; i < len(key); i++ {
		sum += int(key[i])
	}
	return sum
}

// Home 返回 key 在 n 个分区中的归属分区。调用方必须保证 n > 0。
func Home(key string, n int) int {
	return Hash(key) % n
}
