package featureflag

import "hash/fnv"

// bucketCount 是放量桶总数（0..9999）。
const bucketCount = 10000

// bucket 把「开关键 + 用户标识」做确定性哈希，映射到 0..9999。
// 使用 FNV-1a 64 位：实现内建、无第三方依赖、跨进程稳定。
// 同一 (flagKey, userID) 永远落入同一桶，因此调权重时按声明顺序
// 划分的累计区间只影响区间边界附近的用户（见 pickVariant）。
func bucket(flagKey, userID string) int {
	// 用 NUL 分隔，避免「键拼接」造成的碰撞歧义。
	h := fnv.New64a()
	h.Write([]byte(flagKey))
	h.Write([]byte{0})
	h.Write([]byte(userID))
	return int(h.Sum64() % bucketCount)
}

// pickVariant 按变体声明顺序划分累计区间 [0,w0) [w0,w0+w1) ...，
// 返回桶号落在哪个变体。
//
// 因为区间自起点累计，把权重从后一个变体挪给前一个变体时，
// 原先落在前一个变体区间内的用户桶号不变、区间前缀也不变，
// 所以这些用户必然仍命中前一个变体，不发生漂移。
func pickVariant(weights []Weight, b int) string {
	cut := 0
	for _, w := range weights {
		cut += w.Weight
		if b < cut {
			return w.Variant
		}
	}
	// 校验已保证权重之和为 bucketCount，正常不可达。
	return weights[len(weights)-1].Variant
}
