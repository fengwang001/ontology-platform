// Package slot 提供路由槽与分片的纯函数定位逻辑。
package slot

// HashFunc 把字节串哈希为 32 位无符号整数，记作 h(字节串)。
type HashFunc func(data []byte) uint32

// FNV1a32 是默认哈希函数：32 位 FNV-1a。
func FNV1a32(data []byte) uint32 {
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	h := offset32
	for _, b := range data {
		h ^= uint32(b)
		h *= prime32
	}
	return h
}

// Slot 按 slot = (h(routing) + (h(id) mod P)) mod R 计算路由槽。
// P 为 1 时偏移恒为 0；加法在 uint64 域完成，避免 32 位回绕。
func Slot(hid, hr uint32, R, P int) int {
	if P <= 1 {
		return int(uint64(hr) % uint64(R))
	}
	return int((uint64(hr) + uint64(hid%uint32(P))) % uint64(R))
}

// Shard 返回槽所属分片：slot 整除 (R/N)。
func Shard(slotValue, N, R int) int {
	return slotValue / (R / N)
}

// SearchSlots 返回 h(routing)+0 .. h(routing)+P-1（各 mod R）的槽序列。
// P 为 1 时只含一个槽。
func SearchSlots(hr uint32, R, P int) []int {
	slots := make([]int, P)
	base := uint64(hr)
	for offset := 0; offset < P; offset++ {
		slots[offset] = int((base + uint64(offset)) % uint64(R))
	}
	return slots
}
