package featureflag

import "hash/fnv"

// bucket 把「开关键 + 用户标识」确定性哈希到 [0, 9999] 的桶。
// 不同开关使用不同盐（开关键本身），因此同一用户在各开关之间互不相关。
func bucket(flagKey, userID string) int {
	h := fnv.New64a()
	h.Write([]byte(flagKey))
	h.Write([]byte{'\n'})
	h.Write([]byte(userID))
	return int(h.Sum64() % 10000)
}
