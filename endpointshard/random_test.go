package endpointshard

import (
	"fmt"
	"math/rand"
	"testing"
)

// 与独立朴素模型在大量随机同步序列上逐步对照：
// 每个操作的变更报告、查询结果与完整快照都必须一致。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{1619, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomizedAgainstNaiveModel(t, seed, 2000)
		})
	}
}

func runRandomizedAgainstNaiveModel(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	m := NewManager()
	model := newNaiveModel()
	names := []string{"svc-a", "svc-b", "svc-c"}
	for _, name := range names {
		capPerShard := 1 + rng.Intn(6)
		mustCreate(t, m, name, capPerShard)
		model.create(name, capPerShard)
	}

	idPool := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		idPool = append(idPool, fmt.Sprintf("e%02d", i))
	}
	regions := []string{"cn", "us", "eu"}
	lastDesired := map[string][]Endpoint{}

	randomDesired := func() []Endpoint {
		var out []Endpoint
		for _, id := range idPool {
			if rng.Intn(100) >= 45 {
				continue
			}
			out = append(out, Endpoint{
				ID:          id,
				Region:      regions[rng.Intn(len(regions))],
				Healthy:     rng.Intn(100) < 75,
				Terminating: rng.Intn(100) < 25,
			})
		}
		return out
	}

	checkView := func(step int, name string) {
		t.Helper()
		got := mustDescribe(t, m, name)
		want := model.describe(name)
		if !serviceViewEqual(got, want) {
			t.Fatalf("step %d: view mismatch on %s\ngot  %+v\nwant %+v", step, name, got, want)
		}
	}

	for step := 0; step < steps; step++ {
		name := names[rng.Intn(len(names))]
		switch x := rng.Intn(100); {
		case x < 50: // 同步（含 10% 概率重复上一次期望集，即空操作同步）
			desired, ok := lastDesired[name]
			if !ok || rng.Intn(10) != 0 {
				desired = randomDesired()
			}
			repGot, err := m.Sync(name, desired)
			if err != nil {
				t.Fatalf("step %d: sync: %v", step, err)
			}
			repWant := model.sync(name, desired)
			lastDesired[name] = desired
			if !reportEqual(repGot, repWant) {
				t.Fatalf("step %d: report mismatch\ngot  %+v\nwant %+v", step, repGot, repWant)
			}
			t.Logf("step %d 输入: sync(%s, %d 端点); 实际输出: %s; 判定依据: 与朴素模型报告及快照一致", step, name, len(desired), reportSummary(repGot))
			checkView(step, name)
		case x < 65: // 调整容量
			newM := 1 + rng.Intn(10)
			repGot, err := m.Resize(name, newM)
			if err != nil {
				t.Fatalf("step %d: resize: %v", step, err)
			}
			repWant := model.resize(name, newM)
			if !reportEqual(repGot, repWant) {
				t.Fatalf("step %d: resize report mismatch\ngot  %+v\nwant %+v", step, repGot, repWant)
			}
			t.Logf("step %d 输入: resize(%s, %d); 实际输出: %s; 判定依据: 与朴素模型报告及快照一致", step, name, newM, reportSummary(repGot))
			checkView(step, name)
		case x < 80: // 查询
			region := regions[rng.Intn(len(regions))]
			got, err := m.Query(name, region)
			if err != nil {
				t.Fatalf("step %d: query: %v", step, err)
			}
			want := model.query(name, region)
			if !queryEqual(got, want) {
				t.Fatalf("step %d: query mismatch\ngot  %+v\nwant %+v", step, got, want)
			}
			t.Logf("step %d 输入: query(%s, %s); 实际输出: %d 端点 fallback=%v; 判定依据: 与朴素模型一致", step, name, region, len(got.Endpoints), got.Fallback)
		case x < 85: // 删除并重建
			if err := m.DeleteService(name); err != nil {
				t.Fatalf("step %d: delete: %v", step, err)
			}
			model.delete(name)
			capPerShard := 1 + rng.Intn(6)
			mustCreate(t, m, name, capPerShard)
			model.create(name, capPerShard)
			delete(lastDesired, name)
			t.Logf("step %d 输入: delete+create(%s, M=%d); 实际输出: 全新服务; 判定依据: 快照一致", step, name, capPerShard)
			checkView(step, name)
		default: // 非法操作：错误类别正确且状态不变
			before := mustDescribe(t, m, name)
			var err error
			var wantKind Kind
			switch rng.Intn(5) {
			case 0:
				_, err = m.Sync(name, []Endpoint{readyEp("dup", "cn"), readyEp("dup", "cn")})
				wantKind = KindInvalidArgument
			case 1:
				_, err = m.Sync(name, []Endpoint{ep("", "cn", true, false)})
				wantKind = KindInvalidArgument
			case 2:
				_, err = m.Resize(name, 0)
				wantKind = KindInvalidArgument
			case 3:
				err = m.CreateService(name, 4)
				wantKind = KindServiceAlreadyExists
			case 4:
				_, err = m.Sync("ghost", nil)
				wantKind = KindServiceNotFound
			}
			if k, ok := KindOf(err); !ok || k != wantKind {
				t.Fatalf("step %d: error kind = %v, want %v (err=%v)", step, k, wantKind, err)
			}
			after := mustDescribe(t, m, name)
			if !serviceViewEqual(before, after) {
				t.Fatalf("step %d: rejected op changed state\nbefore %+v\nafter  %+v", step, before, after)
			}
			t.Logf("step %d 输入: 非法操作; 实际输出: %v; 判定依据: 类别 %v 且状态不变", step, err, wantKind)
		}
	}
	// 全部服务最终快照对照。
	for _, name := range names {
		checkView(steps, name)
	}
}

func serviceViewEqual(a, b ServiceView) bool {
	if a.M != b.M || a.LastShardID != b.LastShardID || len(a.Shards) != len(b.Shards) {
		return false
	}
	for i := range a.Shards {
		if !shardViewEqual(a.Shards[i], b.Shards[i]) {
			return false
		}
	}
	return true
}

func reportEqual(a, b ChangeReport) bool {
	if len(a.Shards) != len(b.Shards) {
		return false
	}
	for i := range a.Shards {
		x, y := a.Shards[i], b.Shards[i]
		if x.ShardID != y.ShardID || x.Kind != y.Kind || x.Generation != y.Generation {
			return false
		}
		if !endpointsEqual(x.Endpoints, y.Endpoints) {
			return false
		}
	}
	return true
}

func queryEqual(a, b QueryResult) bool {
	return a.Fallback == b.Fallback && endpointsEqual(a.Endpoints, b.Endpoints)
}

// endpointsEqual 把 nil 与空切片视为相等。
func endpointsEqual(a, b []Endpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func reportSummary(rep ChangeReport) string {
	if rep.Empty() {
		return "空报告"
	}
	out := ""
	for i, c := range rep.Shards {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("#%d:%s:g%d", c.ShardID, c.Kind, c.Generation)
	}
	return out
}
