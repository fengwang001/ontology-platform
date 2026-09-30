package lsh

// 本文件实现由种子确定性生成超平面法向量及签名的逻辑。
//
// 法向量分量取自整数区间 [-128, 127]，与整数向量的点积为精确整数，
// 因此符号判定（>= 0 取 1，否则取 0）不存在浮点误差，保证同一种子
// 与同一操作序列得到逐字节相同的签名。
//
// 法向量仅由 (seed, table, plane) 决定，与表数 L、位数 b 无关，
// 因此天然满足嵌套关系：表为前缀、位为前缀。

// splitmix64 为确定性伪随机混合函数。
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// normalComponent 返回第 table 张表第 plane 个超平面法向量的第 i 个分量。
func normalComponent(seed int64, table, plane, i int) int64 {
	h := splitmix64(uint64(seed))
	h = splitmix64(h ^ (uint64(table) << 1))
	h = splitmix64(h ^ (uint64(plane) << 17))
	h = splitmix64(h ^ uint64(i))
	return int64(h&0xff) - 128
}

// normalAt 返回第 table 张表第 plane 个超平面的法向量。
// 全零法向量（概率可忽略）被确定性地替换，保证签名有意义。
func normalAt(seed int64, table, plane, dim int) []int64 {
	n := make([]int64, dim)
	zero := true
	for i := range n {
		n[i] = normalComponent(seed, table, plane, i)
		if n[i] != 0 {
			zero = false
		}
	}
	if zero {
		n[0] = 1
	}
	return n
}

// signature 计算 vec 在第 table 张表下、取前 numBits 个超平面的签名位图。
// 第 p 位为 1 当且仅当 vec 与第 p 个法向量的点积 >= 0。
func signature(normals [][]int64, numBits int, vec []int64) uint64 {
	var sig uint64
	for p := 0; p < numBits; p++ {
		var dot int64
		for i, v := range vec {
			dot += v * normals[p][i]
		}
		if dot >= 0 {
			sig |= 1 << uint(p)
		}
	}
	return sig
}
