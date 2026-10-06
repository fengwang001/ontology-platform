package pvbinding

import "sort"

// volumeIndex 按存储类维护可用卷。
//
// 每个存储类对应一个按 (容量升序, 名称字典序) 排序的切片；立即绑定的
// 选卷只会访问声明所属存储类的桶，因此开销不随其它存储类的卷数量增长。
type volumeIndex struct {
	buckets map[string][]*Volume
}

func newVolumeIndex() *volumeIndex { return nil }

func newVolumeIndexData() *volumeIndex {
	return &volumeIndex{buckets: make(map[string][]*Volume)}
}

// add 将卷插入对应存储类的有序位置。
func (idx *volumeIndex) add(v *Volume) {
	sc := v.Spec.StorageClass
	b := idx.buckets[sc]
	pos := sort.Search(len(b), func(i int) bool {
		return b[i].Spec.Capacity > v.Spec.Capacity ||
			(b[i].Spec.Capacity == v.Spec.Capacity && b[i].Name >= v.Name)
	})
	b = append(b, nil)
	copy(b[pos+1:], b[pos:])
	b[pos] = v
	idx.buckets[sc] = b
}

// remove 从索引中删除指定卷。
func (idx *volumeIndex) remove(v *Volume) {
	sc := v.Spec.StorageClass
	b := idx.buckets[sc]
	pos := sort.Search(len(b), func(i int) bool {
		return b[i].Spec.Capacity > v.Spec.Capacity ||
			(b[i].Spec.Capacity == v.Spec.Capacity && b[i].Name >= v.Name)
	})
	if pos < len(b) && b[pos].Name == v.Name {
		idx.buckets[sc] = append(b[:pos], b[pos+1:]...)
	}
}

// bucket 返回某存储类的有序可用卷切片，供顺序扫描。
func (idx *volumeIndex) bucket(storageClass string) []*Volume {
	return idx.buckets[storageClass]
}

// bucketSize 返回某存储类桶的卷数量。
func (idx *volumeIndex) bucketSize(storageClass string) int {
	return len(idx.buckets[storageClass])
}

// size 返回全部索引的卷数量。
func (idx *volumeIndex) size() int {
	n := 0
	for _, b := range idx.buckets {
		n += len(b)
	}
	return n
}

// pickImmediate 在声明所属存储类桶内按序扫描，返回第一个满足全部候选条件
// 的卷。桶按 (容量, 名称) 排序，所以首个匹配即为最小容量、同容量名称最小者。
// 返回的 examined 是本次实际检查过的卷数，用于性能验证。
func (idx *volumeIndex) pickImmediate(c *Claim, f candidateFilter) (vol *Volume, examined int) {
	for _, v := range idx.bucket(c.Spec.StorageClass) {
		examined++
		if f.matches(v, c) {
			return v, examined
		}
	}
	return nil, examined
}
