package fetch

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, n, w, p int) *Assembler {
	t.Helper()
	a, err := New(n, w, p)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) 意外失败: %v", n, w, p, err)
	}
	return a
}

func mustAppend(t *testing.T, a *Assembler, partition, size int, payload string) int {
	t.Helper()
	off, err := a.Append(partition, size, payload)
	if err != nil {
		t.Fatalf("Append(%d, %d, %q) 意外失败: %v", partition, size, payload, err)
	}
	return off
}

func mustFetch(t *testing.T, a *Assembler) *Response {
	t.Helper()
	resp, err := a.Fetch()
	if err != nil {
		t.Fatalf("Fetch 意外失败: %v", err)
	}
	return resp
}

func logResponse(t *testing.T, resp *Response) {
	t.Helper()
	t.Logf("响应: total=%d nextStart=%d firstOverBudget=%v records=%v",
		resp.TotalBytes, resp.NextStart, resp.FirstOverBudget, resp.Records)
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		n, w, p int
		want    error
	}{
		{0, 10, 5, ErrInvalidPartitionCount},
		{-1, 10, 5, ErrInvalidPartitionCount},
		{1, 0, 5, ErrInvalidTotalBudget},
		{1, 10, 0, ErrInvalidPartitionLimit},
		{1, 1, 1, nil},
	}
	for _, c := range cases {
		_, err := New(c.n, c.w, c.p)
		if !errors.Is(err, c.want) {
			t.Errorf("New(%d,%d,%d) err=%v, want %v", c.n, c.w, c.p, err, c.want)
		}
		t.Logf("输入 New(%d,%d,%d) -> err=%v (判定: 参数必须均 >= 1)", c.n, c.w, c.p, err)
	}
}

func TestAppendValidation(t *testing.T) {
	a := mustNew(t, 2, 10, 5)

	if _, err := a.Append(-1, 1, "x"); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Errorf("Append(-1,...) err=%v, want ErrPartitionOutOfRange", err)
	}
	if _, err := a.Append(2, 1, "x"); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Errorf("Append(2,...) err=%v, want ErrPartitionOutOfRange", err)
	}
	if _, err := a.Append(0, 0, "x"); !errors.Is(err, ErrInvalidMessageSize) {
		t.Errorf("Append(0,0,...) err=%v, want ErrInvalidMessageSize", err)
	}
	// 分区越界与大小非法同时成立时，报分区越界。
	if _, err := a.Append(9, 0, "x"); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Errorf("Append(9,0,...) err=%v, want ErrPartitionOutOfRange", err)
	}
	t.Log("判定依据: 分区编号越界优先于消息大小非法")

	// 被拒绝的追加不改变状态。
	if off := mustAppend(t, a, 0, 3, "a"); off != 0 {
		t.Fatalf("首条消息位点=%d, want 0", off)
	}
	if off := mustAppend(t, a, 0, 2, "b"); off != 1 {
		t.Fatalf("第二条消息位点=%d, want 1", off)
	}
	snap := a.Snapshot()
	if snap.Pending[0] != 2 || snap.Pending[1] != 0 || snap.RotationStart != 0 {
		t.Fatalf("快照异常: %+v", snap)
	}
	t.Logf("快照: %+v (被拒绝的追加未改变状态)", snap)
}

func TestExactLimits(t *testing.T) {
	// W=10, P=6: 分区 0 累计 4+2=6 恰等于 P；响应总计 6+3+1=10 恰等于 W。
	a := mustNew(t, 2, 10, 6)
	mustAppend(t, a, 0, 4, "p0#0")
	mustAppend(t, a, 0, 2, "p0#1")
	mustAppend(t, a, 0, 1, "p0#2")
	mustAppend(t, a, 1, 3, "p1#0")
	mustAppend(t, a, 1, 1, "p1#1")
	mustAppend(t, a, 1, 1, "p1#2")

	resp := mustFetch(t, a)
	logResponse(t, resp)
	t.Log("判定依据: 分区0 4+2=6<=P 可取, 再+1>6 停; 分区1 6+3=9<=W, 9+1=10<=W 恰等于可取, 再+1>10 停")

	if resp.TotalBytes != 10 {
		t.Fatalf("TotalBytes=%d, want 10", resp.TotalBytes)
	}
	if resp.PartitionBytes[0] != 6 || resp.PartitionBytes[1] != 4 {
		t.Fatalf("PartitionBytes=%v, want {0:6, 1:4}", resp.PartitionBytes)
	}
	if len(resp.Records) != 4 {
		t.Fatalf("Records 条数=%d, want 4", len(resp.Records))
	}
	if resp.NextStart != 0 {
		t.Fatalf("NextStart=%d, want 0 (最后取到的是分区1, 下一个环形为0)", resp.NextStart)
	}
}

func TestFirstMessageOverBudget(t *testing.T) {
	// 首条同时超过 P 与 W，仍返回且只此一条。
	a := mustNew(t, 2, 5, 3)
	mustAppend(t, a, 0, 10, "big")
	mustAppend(t, a, 0, 1, "small")

	resp := mustFetch(t, a)
	logResponse(t, resp)
	t.Log("判定依据: 响应总字节为 0 时无视 P/W 取下一条(仅一条); 之后 total=10, 再取任何消息都超 W")

	if len(resp.Records) != 1 || resp.Records[0].Payload != "big" {
		t.Fatalf("Records=%v, want 仅 [big]", resp.Records)
	}
	if !resp.FirstOverBudget {
		t.Fatal("FirstOverBudget 应为 true")
	}
	if resp.TotalBytes != 10 {
		t.Fatalf("TotalBytes=%d, want 10", resp.TotalBytes)
	}

	// 第二次拉取：small 仍在，未被跳过。
	resp2 := mustFetch(t, a)
	logResponse(t, resp2)
	if len(resp2.Records) != 1 || resp2.Records[0].Payload != "small" || resp2.Records[0].Offset != 1 {
		t.Fatalf("Records=%v, want 仅 [small@1]", resp2.Records)
	}
}

func TestBlockedMessageNotSkipped(t *testing.T) {
	a := mustNew(t, 1, 10, 10)
	mustAppend(t, a, 0, 8, "m0")
	mustAppend(t, a, 0, 5, "m1")
	mustAppend(t, a, 0, 1, "m2")

	resp := mustFetch(t, a)
	logResponse(t, resp)
	t.Log("判定依据: 8 取走后 8+5>10, m1 受阻且分区结束, 不得跳过 m1 去取更小的 m2")

	if len(resp.Records) != 1 || resp.Records[0].Payload != "m0" {
		t.Fatalf("Records=%v, want 仅 [m0]", resp.Records)
	}

	resp2 := mustFetch(t, a)
	logResponse(t, resp2)
	if len(resp2.Records) != 2 || resp2.Records[0].Payload != "m1" || resp2.Records[1].Payload != "m2" {
		t.Fatalf("Records=%v, want [m1 m2] 按位点升序", resp2.Records)
	}
}

func TestBudgetShortfallLaterPartitionStillServed(t *testing.T) {
	a := mustNew(t, 3, 10, 10)
	mustAppend(t, a, 0, 8, "p0")
	mustAppend(t, a, 1, 5, "p1")
	mustAppend(t, a, 2, 1, "p2")

	resp := mustFetch(t, a)
	logResponse(t, resp)
	t.Log("判定依据: p0 取 8 后总余量 2; p1 的 5 放不下而结束, 但不影响继续访问 p2, p2 的 1 仍放得下")

	if len(resp.Records) != 2 || resp.Records[0].Partition != 0 || resp.Records[1].Partition != 2 {
		t.Fatalf("Records=%v, want [p0 p2]", resp.Records)
	}
	if resp.TotalBytes != 9 {
		t.Fatalf("TotalBytes=%d, want 9", resp.TotalBytes)
	}
	if resp.NextStart != 0 {
		t.Fatalf("NextStart=%d, want 0 (最后取到分区2, 环形下一个为0)", resp.NextStart)
	}
}

func TestRotationAdvanceAndWrap(t *testing.T) {
	a := mustNew(t, 3, 100, 100)

	// 仅分区 2 有数据：最后取到分区 2，下一起点环形回绕为 0。
	mustAppend(t, a, 2, 1, "p2#0")
	resp := mustFetch(t, a)
	logResponse(t, resp)
	if resp.NextStart != 0 {
		t.Fatalf("NextStart=%d, want 0 (环形回绕)", resp.NextStart)
	}

	// 仅分区 1 有数据：起点推进到 2。
	mustAppend(t, a, 1, 1, "p1#0")
	resp = mustFetch(t, a)
	logResponse(t, resp)
	if resp.NextStart != 2 {
		t.Fatalf("NextStart=%d, want 2", resp.NextStart)
	}

	// 起点为 2：环形访问顺序为 2,0,1，分区 2 的消息应先出现在响应里。
	mustAppend(t, a, 0, 1, "p0#0")
	mustAppend(t, a, 2, 1, "p2#1")
	resp = mustFetch(t, a)
	logResponse(t, resp)
	t.Log("判定依据: 从轮转起点 2 起环形访问 2->0->1")
	if len(resp.Records) != 2 || resp.Records[0].Partition != 2 || resp.Records[1].Partition != 0 {
		t.Fatalf("Records=%v, want 先分区2后分区0", resp.Records)
	}
	if resp.NextStart != 1 {
		t.Fatalf("NextStart=%d, want 1", resp.NextStart)
	}
	if snap := a.Snapshot(); snap.RotationStart != 1 {
		t.Fatalf("Snapshot.RotationStart=%d, want 1", snap.RotationStart)
	}
}

func TestNoData(t *testing.T) {
	a := mustNew(t, 2, 10, 5)

	if _, err := a.Fetch(); !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch err=%v, want ErrNoData", err)
	}
	t.Log("判定依据: 所有分区都没有可取消息 -> ErrNoData, 状态不变")

	mustAppend(t, a, 1, 2, "x")
	resp := mustFetch(t, a)
	if resp.NextStart != 0 {
		t.Fatalf("NextStart=%d, want 0 (N=2, (1+1)%%2=0)", resp.NextStart)
	}

	// 取空后再次拉取：ErrNoData 且轮转起点等状态不变。
	if _, err := a.Fetch(); !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch err=%v, want ErrNoData", err)
	}
	snap := a.Snapshot()
	if snap.RotationStart != 0 || snap.ConsumePositions[1] != 1 {
		t.Fatalf("被拒绝的拉取改变了状态: %+v", snap)
	}
	t.Logf("快照: %+v (ErrNoData 后状态未变)", snap)
}

func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		a := mustNew(t, 3, 7, 4)
		var out []string
		ops := []struct {
			part, size int
			payload    string
		}{
			{0, 3, "a"}, {1, 4, "b"}, {2, 2, "c"}, {0, 4, "d"}, {1, 1, "e"},
		}
		for _, op := range ops {
			off, _ := a.Append(op.part, op.size, op.payload)
			out = append(out, fmt.Sprintf("append(%d,%d,%q)->%d", op.part, op.size, op.payload, off))
		}
		for i := 0; i < 4; i++ {
			resp, err := a.Fetch()
			if err != nil {
				out = append(out, "fetch->"+err.Error())
				continue
			}
			out = append(out, fmt.Sprintf("fetch->%+v", *resp))
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放不一致 at %d:\n%s\n%s", i, first[i], second[i])
		}
		t.Logf("重放[%d]: %s", i, first[i])
	}
}
