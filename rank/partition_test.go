package rank

import (
	"math"
	"testing"
)

func TestPartitionsIsolatedAndOrdered(t *testing.T) {
	b, a := "b", "a"
	rows := []Row{
		{Partition: &b, Value: 9, ID: "b1"},
		{Partition: &a, Value: 1, ID: "a1"},
		{Partition: &b, Value: 9, ID: "b0"},
		{Partition: &a, Value: 2, ID: "a2"},
	}
	res := RankRows(rows, Asc)

	wantParts := []string{"a", "a", "b", "b"}
	for i, p := range wantParts {
		if res.Rankings[i].Partition != p {
			t.Fatalf("第 %d 行分区 = %q, 期望 %q（输出须按分区键字典序）",
				i, res.Rankings[i].Partition, p)
		}
	}
	// 两个分区的 ROW_NUMBER/RANK 都各自从 1 开始。
	assertTriples(t, &Result{Rankings: res.Rankings[:2]}, []triple{
		{1, 1, 1},
		{2, 2, 2},
	})
	assertTriples(t, &Result{Rankings: res.Rankings[2:]}, []triple{
		{1, 1, 1},
		{2, 1, 1},
	})
}

func TestEmptyPartitionIsValid(t *testing.T) {
	rows := []Row{
		{Partition: ptr(""), Value: 3, ID: "x"},
		{Partition: ptr(""), Value: 1, ID: "y"},
	}
	res := RankRows(rows, Asc)
	if res.Stats.SkippedNilPartition != 0 || res.Stats.SkippedNaN != 0 {
		t.Fatalf("空分区不应被跳过: %+v", res.Stats)
	}
	if len(res.Rankings) != 2 || res.Rankings[0].Partition != "" || res.Rankings[0].ID != "y" {
		t.Fatalf("空分区排名异常: %+v", res.Rankings)
	}
}

func TestNilPartitionAndNaNSkipped(t *testing.T) {
	p := "p"
	rows := []Row{
		{Partition: &p, Value: 1, ID: "ok1"},
		{Partition: nil, Value: 1, ID: "nil1"},
		{Partition: &p, Value: math.NaN(), ID: "nan1"},
		{Partition: nil, Value: math.NaN(), ID: "nilnan"},
		{Partition: &p, Value: 2, ID: "ok2"},
	}
	res := RankRows(rows, Asc)
	if res.Stats.SkippedNilPartition != 2 {
		t.Fatalf("SkippedNilPartition = %d, 期望 2", res.Stats.SkippedNilPartition)
	}
	if res.Stats.SkippedNaN != 2 {
		t.Fatalf("SkippedNaN = %d, 期望 2（nil+NaN 同时计入两计数）", res.Stats.SkippedNaN)
	}
	if len(res.Rankings) != 2 {
		t.Fatalf("合法行数 = %d, 期望 2", len(res.Rankings))
	}
	if res.Rankings[0].ID != "ok1" || res.Rankings[1].ID != "ok2" {
		t.Fatalf("被拒绝的行参与了排名: %+v", res.Rankings)
	}
}

func TestEmptyInput(t *testing.T) {
	res := RankRows(nil, Asc)
	if len(res.Rankings) != 0 || res.Stats != (Stats{}) {
		t.Fatalf("空输入结果异常: %+v", res)
	}
}
