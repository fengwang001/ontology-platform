package idb

import "sort"

// versionedValue 是某个键在一次提交中写入的值。
// seq 为提交序号，单调递增；同一键的版本按 seq 升序追加。
type versionedValue struct {
	seq   uint64
	value []byte
}

// storeData 以 MVCC 方式存放一个对象仓库的已提交数据。
// 提交只在被写键的版本链上追加，开销与仓库中无关键的数量无关。
type storeData struct {
	data map[string][]versionedValue
}

func newStoreData() *storeData {
	return &storeData{data: map[string][]versionedValue{}}
}

// get 返回在快照 seq 下可见的最新值。
func (s *storeData) get(key string, seq uint64) ([]byte, bool) {
	versions, ok := s.data[key]
	if !ok {
		return nil, false
	}
	i := sort.Search(len(versions), func(i int) bool { return versions[i].seq > seq }) - 1
	if i < 0 {
		return nil, false
	}
	return versions[i].value, true
}

// put 在提交序号 seq 处追加一个版本；调用方保证 seq 递增。
func (s *storeData) put(key string, seq uint64, value []byte) {
	s.data[key] = append(s.data[key], versionedValue{seq: seq, value: value})
}
