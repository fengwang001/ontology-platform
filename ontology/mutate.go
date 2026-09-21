package rlebitmap

// setBit 在不加锁的前提下把位 b 置为 1，并保持 runs 规范。
// 调用方必须持有写锁。
func setBit(runs []run, b uint32) []run {
	panic("not implemented")
}

// clearBit 在不加锁的前提下把位 b 置为 0，并保持 runs 规范。
// 调用方必须持有写锁。
func clearBit(runs []run, b uint32) []run {
	panic("not implemented")
}

// containsBit 在不加锁的前提下判定位 b 是否置位。
// 调用方必须持有读锁或保证 runs 不会被并发修改。
func containsBit(runs []run, b uint32) bool {
	panic("not implemented")
}
