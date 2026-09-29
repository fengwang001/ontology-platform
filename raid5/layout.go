package raid5

// parityDisk 返回条带 s 的校验盘号：N-1-(s mod N)。
func parityDisk(n, stripe int) int {
	return (n - 1) - (stripe % n)
}

// dataDisk 返回条带 s 中第 k 个数据块（k=0..N-2）所在盘号。
// 数据块从校验盘的下一块盘起依次排列并回绕。
func dataDisk(n, stripe, k int) int {
	p := parityDisk(n, stripe)
	return (p + 1 + k) % n
}

// blockDisk 返回逻辑块所在盘与其在条带内的数据序号；
// 当 ok=false 时该槽位是校验块。
func blockDisk(n, stripe, slot int) (disk int, dataIndex int, ok bool) {
	p := parityDisk(n, stripe)
	// 校验盘的下一槽（回绕）起是数据块。
	d := (p + 1 + slot) % n
	return d, slot, true
}

// blockLocation 把连续逻辑块号转换为 (条带, 条带内数据序号, 盘号)。
func blockLocation(n, logicalBlock int) (stripe, slot, disk int) {
	dataPerStripe := n - 1
	stripe = logicalBlock / dataPerStripe
	slot = logicalBlock % dataPerStripe
	disk, _, _ = blockDisk(n, stripe, slot)
	return
}

// stripeMap 返回条带 s 的盘布局：parity 为校验盘号，
// data[k] 为第 k 个数据块所在盘号。
func stripeMap(n, s int) (parity int, data []int) {
	parity = parityDisk(n, s)
	data = make([]int, n-1)
	for k := range data {
		data[k] = dataDisk(n, s, k)
	}
	return
}

// xorBlocks 把多个块逐字节异或到 dst 上（dst 需预先清零或直接传入第一个块）。
func xorBlocks(dst []byte, blocks ...[]byte) {
	for _, b := range blocks {
		for i := range dst {
			dst[i] ^= b[i]
		}
	}
}
