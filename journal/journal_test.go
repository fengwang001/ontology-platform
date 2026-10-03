package journal

import "testing"

// Recover 单次读取记录数应等于该实例日志条数，不随其他实例增长。
func TestReadCountScalesWithOwnPartition(t *testing.T) {
	j := New()
	j.Append("one", Record{Kind: KindBegin})
	for i := 0; i < 10000; i++ {
		j.Append("big", Record{Kind: KindIntent, Step: i})
	}
	base := j.reads.Load()
	j.Read("one")
	if got := j.reads.Load() - base; got != 1 {
		t.Fatalf("输入: 实例 one 有 1 条、big 有 10000 条；读取 one 应计 1 条，实际 %d", got)
	}
	base = j.reads.Load()
	j.Read("big")
	if got := j.reads.Load() - base; got != 10000 {
		t.Fatalf("输入: 实例 big 有 10000 条；读取 big 应计 10000 条，实际 %d", got)
	}
	t.Logf("判定依据: 按实例分区，读取条数只等于本分区长度（1 与 10000 两档互不影响）")
}

// Read 必须返回副本，调用方修改不影响日志。
func TestReadReturnsCopy(t *testing.T) {
	j := New()
	j.Append("a", Record{Kind: KindBegin})
	recs := j.Read("a")
	recs[0].Kind = KindFail
	if got := j.Read("a")[0].Kind; got != KindBegin {
		t.Fatalf("输入: 篡改 Read 返回的副本；期望日志仍为 B，实际 %v", got)
	}
	if j.Len("a") != 1 {
		t.Fatalf("Len 应为 1，实际 %d", j.Len("a"))
	}
}
