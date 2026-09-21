package ontology

import (
	"encoding/binary"
	"testing"
)

// 测试用的两组互不相交的元素空间：ins-* 用于插入，qry-* 用于
// 查询（保证从未被插入过）。

func insElem(i int) []byte {
	buf := make([]byte, 11)
	copy(buf, "ins")
	binary.BigEndian.PutUint64(buf[3:], uint64(i))
	return buf
}

func qryElem(i int) []byte {
	buf := make([]byte, 11)
	copy(buf, "qry")
	binary.BigEndian.PutUint64(buf[3:], uint64(i))
	return buf
}

// fillFilter 构造 (n, p) 的过滤器并插入 insElem(0..n-1)。
func fillFilter(t testing.TB, n int, p float64) *Filter {
	t.Helper()
	f, err := New(n, p)
	if err != nil {
		t.Fatalf("New(%d, %g) failed: %v", n, p, err)
	}
	for i := 0; i < n; i++ {
		f.Add(insElem(i))
	}
	return f
}
