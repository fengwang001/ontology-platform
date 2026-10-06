package rollout

import "hash/fnv"

// position 计算标识的归属位置：FNV-1a 64 位散列对 10000 取模。
// 同一标识恒定；不同标识在 [0,10000) 上近似均匀。
// 取模不引入额外偏差（2^64 不能被 10000 整除，偏差量级 1e-16，可忽略）。
func position(id string) int {
	h := fnv.New64a()
	h.Write([]byte(id))
	return int(h.Sum64() % 10000)
}
