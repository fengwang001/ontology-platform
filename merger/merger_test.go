package merger

import (
	"reflect"
	"testing"
)

func ok(shard string, r Result) ShardResponse {
	return ShardResponse{Shard: shard, Kind: RespOK, Result: r}
}

func failed(shard string, kind RespKind) ShardResponse {
	return ShardResponse{Shard: shard, Kind: kind}
}

var twoShards = []ShardInfo{
	{Name: "s1", RowBound: 10, ValueBound: 100},
	{Name: "s2", RowBound: 20, ValueBound: 200},
}

// 全部成功时五种聚合都给出精确值。
func TestMergeExactAllSucceeded(t *testing.T) {
	resp := []ShardResponse{
		ok("s1", Result{Value: 7, Values: []uint64{90, 50}, HasValue: true}),
		ok("s2", Result{Value: 15, Values: []uint64{180, 30}, HasValue: true}),
	}

	count := Merge(twoShards, Aggregation{Kind: AggCount}, resp)
	t.Logf("[count] 输入: %+v 输出: %+v", resp, count)
	t.Logf("[count] 判定依据: 全部成功, 精确值 = 7+15 = 22")
	if !count.Complete || count.Lower != 22 || count.Upper != 22 {
		t.Fatalf("count: got %+v, want exact [22,22]", count)
	}

	sum := Merge(twoShards, Aggregation{Kind: AggSum}, resp)
	t.Logf("[sum] 输出: %+v", sum)
	t.Logf("[sum] 判定依据: 全部成功, 精确值 = 7+15 = 22")
	if !sum.Complete || sum.Lower != 22 || sum.Upper != 22 {
		t.Fatalf("sum: got %+v, want exact [22,22]", sum)
	}

	min := Merge(twoShards, Aggregation{Kind: AggMin}, resp)
	t.Logf("[min] 输出: %+v", min)
	t.Logf("[min] 判定依据: 全部成功, 精确值 = min(7,15) = 7")
	if !min.Complete || min.Lower != 7 || min.Upper != 7 {
		t.Fatalf("min: got %+v, want exact [7,7]", min)
	}

	max := Merge(twoShards, Aggregation{Kind: AggMax}, resp)
	t.Logf("[max] 输出: %+v", max)
	t.Logf("[max] 判定依据: 全部成功, 精确值 = max(7,15) = 15")
	if !max.Complete || max.Lower != 15 || max.Upper != 15 {
		t.Fatalf("max: got %+v, want exact [15,15]", max)
	}

	topk := Merge(twoShards, Aggregation{Kind: AggTopK, K: 3}, resp)
	t.Logf("[topk] 输出: %+v", topk)
	t.Logf("[topk] 判定依据: 全部成功, 合并排序取前 3 = [180 90 50], 全部为确定前缀")
	wantItems := []uint64{180, 90, 50}
	if !topk.Complete || !reflect.DeepEqual(topk.Items, wantItems) || topk.CertainPrefix != 3 {
		t.Fatalf("topk: got %+v, want items=%v prefix=3", topk, wantItems)
	}
}

// 有分片缺失时，计数与求和给出闭区间，方向为「已收到为下界，上界加缺失上界」。
func TestMergeCountSumIntervalWithMissing(t *testing.T) {
	shards := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 100},
		{Name: "s2", RowBound: 20, ValueBound: 200},
		{Name: "s3", RowBound: 5, ValueBound: 50},
	}
	resp := []ShardResponse{
		ok("s1", Result{Value: 8}),
		ok("s2", Result{Value: 12}),
		failed("s3", RespError),
	}

	count := Merge(shards, Aggregation{Kind: AggCount}, resp)
	t.Logf("[count] 输入: shards=%+v resp=%+v", shards, resp)
	t.Logf("[count] 输出: %+v", count)
	t.Logf("[count] 判定依据: 下界 = 已收到 8+12 = 20; 上界 = 20 + 缺失行数上界 5 = 25")
	if count.Complete || count.Lower != 20 || count.Upper != 25 {
		t.Fatalf("count: got %+v, want [20,25]", count)
	}

	sum := Merge(shards, Aggregation{Kind: AggSum}, resp)
	t.Logf("[sum] 输出: %+v", sum)
	t.Logf("[sum] 判定依据: 下界 = 20; 上界 = 20 + 缺失行数上界*取值上界 = 20 + 5*50 = 270")
	if sum.Complete || sum.Lower != 20 || sum.Upper != 270 {
		t.Fatalf("sum: got %+v, want [20,270]", sum)
	}
}

// 最小值缺失时只给「不大于已收到最小值」；最大值下端为已收到最大值，
// 上端为它与缺失取值上界的较大者。
func TestMergeMinMaxIntervalWithMissing(t *testing.T) {
	shards := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 100},
		{Name: "s2", RowBound: 20, ValueBound: 200},
		{Name: "s3", RowBound: 5, ValueBound: 500},
	}
	resp := []ShardResponse{
		ok("s1", Result{Value: 30, HasValue: true}),
		ok("s2", Result{Value: 40, HasValue: true}),
		failed("s3", RespTimeout),
	}

	min := Merge(shards, Aggregation{Kind: AggMin}, resp)
	t.Logf("[min] 输入: shards=%+v resp=%+v", shards, resp)
	t.Logf("[min] 输出: %+v", min)
	t.Logf("[min] 判定依据: 缺失分片可能含更小值, 只能断言 真值 <= 已收到最小值 30")
	if min.Complete || min.Upper != 30 || min.Lower != 0 {
		t.Fatalf("min: got %+v, want Upper=30 (<=30), Lower=0", min)
	}

	max := Merge(shards, Aggregation{Kind: AggMax}, resp)
	t.Logf("[max] 输出: %+v", max)
	t.Logf("[max] 判定依据: 下界 = 已收到最大值 40; 上界 = max(40, 缺失取值上界 500) = 500")
	if max.Complete || max.Lower != 40 || max.Upper != 500 {
		t.Fatalf("max: got %+v, want [40,500]", max)
	}

	// 缺失取值上界小于已收到最大值时，上端仍为已收到最大值。
	shards2 := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 100},
		{Name: "s3", RowBound: 5, ValueBound: 20},
	}
	resp2 := []ShardResponse{
		ok("s1", Result{Value: 90, HasValue: true}),
		failed("s3", RespError),
	}
	max2 := Merge(shards2, Aggregation{Kind: AggMax}, resp2)
	t.Logf("[max2] 输入: shards=%+v resp=%+v", shards2, resp2)
	t.Logf("[max2] 输出: %+v", max2)
	t.Logf("[max2] 判定依据: 下界 = 90; 上界 = max(90, 缺失取值上界 20) = 90")
	if max2.Lower != 90 || max2.Upper != 90 {
		t.Fatalf("max2: got %+v, want [90,90]", max2)
	}
}

// 前 K 名中严格大于全部缺失取值上界的前缀标为确定；等于上界则不确定。
func TestMergeTopKCertainPrefixBoundary(t *testing.T) {
	shards := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 1000},
		{Name: "s2", RowBound: 10, ValueBound: 1000},
		{Name: "s3", RowBound: 10, ValueBound: 100}, // 缺失, 取值上界 100
	}
	resp := []ShardResponse{
		ok("s1", Result{Values: []uint64{300, 101, 100, 99}}),
		ok("s2", Result{Values: []uint64{200}}),
		failed("s3", RespError),
	}
	ans := Merge(shards, Aggregation{Kind: AggTopK, K: 5}, resp)
	t.Logf("输入: shards=%+v resp=%+v", shards, resp)
	t.Logf("输出: %+v", ans)
	t.Logf("判定依据: 合并前 5 = [300 200 101 100 99]; 缺失上界 = 100; " +
		"严格大于 100 的前缀为 [300 200 101], 100 不严格大于 100 故前缀长度 = 3")
	wantItems := []uint64{300, 200, 101, 100, 99}
	if !reflect.DeepEqual(ans.Items, wantItems) {
		t.Fatalf("items: got %v, want %v", ans.Items, wantItems)
	}
	if ans.CertainPrefix != 3 {
		t.Fatalf("CertainPrefix: got %d, want 3 (100 等于缺失上界, 不确定)", ans.CertainPrefix)
	}

	// 边界另一侧: 101 严格大于 100, 计入确定前缀。
	resp2 := []ShardResponse{
		ok("s1", Result{Values: []uint64{101}}),
		ok("s2", Result{Values: []uint64{50}}),
		failed("s3", RespError),
	}
	ans2 := Merge(shards, Aggregation{Kind: AggTopK, K: 2}, resp2)
	t.Logf("[边界] 输入: resp=%+v 输出: %+v", resp2, ans2)
	t.Logf("[边界] 判定依据: 101 > 100 计入确定前缀, 50 < 100 不计入, 前缀长度 = 1")
	if ans2.CertainPrefix != 1 {
		t.Fatalf("CertainPrefix: got %d, want 1", ans2.CertainPrefix)
	}
}

// 四类故障分别计数：返回错误、超时、违反登记上界、重复返回（只取首个）。
func TestMergeFailureCategoriesCountedSeparately(t *testing.T) {
	shards := []ShardInfo{
		{Name: "ok", RowBound: 10, ValueBound: 100},
		{Name: "err", RowBound: 10, ValueBound: 100},
		{Name: "slow", RowBound: 10, ValueBound: 100},
		{Name: "bad", RowBound: 10, ValueBound: 100},
		{Name: "dup", RowBound: 10, ValueBound: 100},
		{Name: "conf", RowBound: 10, ValueBound: 100},
	}
	resp := []ShardResponse{
		ok("ok", Result{Value: 5}),
		failed("err", RespError),
		failed("slow", RespTimeout),
		ok("bad", Result{Value: 11}), // 11 > 行数上界 10, 违反登记上界
		ok("dup", Result{Value: 3}),  // 首个, 被采纳
		ok("dup", Result{Value: 3}),  // 内容一致的重复, 丢弃
		ok("conf", Result{Value: 1}), // 与下一条内容冲突, 无法核对
		ok("conf", Result{Value: 2}), // 冲突的重复, 整个分片按违反登记处理
	}
	ans := Merge(shards, Aggregation{Kind: AggCount}, resp)
	t.Logf("输入: shards=%+v resp=%+v", shards, resp)
	t.Logf("输出: %+v", ans)
	t.Logf("判定依据: Errors=1(err) Timeouts=1(slow) Violations=2(bad 超出行数上界, conf 返回冲突) " +
		"Duplicates=2(dup 与 conf 的第二次返回); 下界 = 5+3 = 8")
	if ans.Errors != 1 || ans.Timeouts != 1 || ans.Violations != 2 || ans.Duplicates != 2 {
		t.Fatalf("counts: got E=%d T=%d V=%d D=%d, want 1/1/2/2",
			ans.Errors, ans.Timeouts, ans.Violations, ans.Duplicates)
	}
	if ans.Succeeded != 2 {
		t.Fatalf("Succeeded: got %d, want 2", ans.Succeeded)
	}
	// 下界只含首个 dup 值 3。
	if ans.Lower != 8 {
		t.Fatalf("Lower: got %d, want 8 (一致重复只取首个)", ans.Lower)
	}
	// 上界 = 8 + 缺失(err,slow,bad,conf)行数上界之和 40。
	if ans.Upper != 48 {
		t.Fatalf("Upper: got %d, want 48", ans.Upper)
	}
	wantMissing := []string{"bad", "conf", "err", "slow"}
	if !reflect.DeepEqual(ans.Missing, wantMissing) {
		t.Fatalf("Missing: got %v, want %v", ans.Missing, wantMissing)
	}
}

// 无分片成功时为无结论。
func TestMergeInconclusive(t *testing.T) {
	resp := []ShardResponse{failed("s1", RespError), failed("s2", RespTimeout)}
	ans := Merge(twoShards, Aggregation{Kind: AggSum}, resp)
	t.Logf("输入: shards=%+v resp=%+v", twoShards, resp)
	t.Logf("输出: %+v", ans)
	t.Logf("判定依据: 成功分片数 = 0, 无法给出任何可信范围, 置 Inconclusive")
	if !ans.Inconclusive || ans.Succeeded != 0 {
		t.Fatalf("got %+v, want Inconclusive", ans)
	}
}

// 同一组响应以任意到达顺序合并，得到逐字段相同的结果。
func TestMergeOrderIndependence(t *testing.T) {
	shards := []ShardInfo{
		{Name: "s1", RowBound: 10, ValueBound: 100},
		{Name: "s2", RowBound: 10, ValueBound: 100},
		{Name: "s3", RowBound: 10, ValueBound: 100},
		{Name: "s4", RowBound: 10, ValueBound: 100},
	}
	base := []ShardResponse{
		ok("s1", Result{Value: 5, Values: []uint64{90, 40}, HasValue: true}),
		ok("s2", Result{Value: 7, Values: []uint64{80, 60}, HasValue: true}),
		failed("s3", RespError),
		ok("s4", Result{Value: 2, Values: []uint64{70}, HasValue: true}),
		ok("s1", Result{Value: 99, Values: []uint64{1}, HasValue: true}), // 与首个冲突, 该分片按违反登记处理
	}
	aggs := []Aggregation{
		{Kind: AggCount}, {Kind: AggSum}, {Kind: AggMin}, {Kind: AggMax},
		{Kind: AggTopK, K: 3},
	}
	for _, agg := range aggs {
		want := Merge(shards, agg, base)
		// 全部排列逐一验证。
		permute(len(base), func(p []int) {
			shuffled := make([]ShardResponse, len(base))
			for i, j := range p {
				shuffled[i] = base[j]
			}
			got := Merge(shards, agg, shuffled)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("agg=%v perm=%v:\n got %+v\nwant %+v", agg.Kind, p, got, want)
			}
		})
		t.Logf("[agg=%v] 输入: resp=%+v", agg.Kind, base)
		t.Logf("[agg=%v] 输出: %+v", agg.Kind, want)
		t.Logf("[agg=%v] 判定依据: 全部 %d! 种排列合并结果逐字段相同", agg.Kind, len(base))
	}
}

func permute(n int, f func([]int)) {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	var rec func(int)
	rec = func(i int) {
		if i == n {
			f(append([]int(nil), p...))
			return
		}
		for j := i; j < n; j++ {
			p[i], p[j] = p[j], p[i]
			rec(i + 1)
			p[i], p[j] = p[j], p[i]
		}
	}
	rec(0)
}
