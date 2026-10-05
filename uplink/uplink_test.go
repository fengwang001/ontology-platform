package uplink

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/profile"
)

// step 为表驱动场景中的一步操作。
type step struct {
	op      string // "P"=SetProfile, "S"=Sample, "T"=Tick
	point   string
	t       int64
	v       int64 // Sample 的取值
	params  [5]int64
	wantErr error
	want    []string
}

func fmtOuts(outs []Output) []string {
	if len(outs) == 0 {
		return nil
	}
	s := make([]string, len(outs))
	for i, o := range outs {
		s[i] = o.String()
	}
	return s
}

func runSteps(t *testing.T, e *Engine, steps []step) {
	t.Helper()
	for i, s := range steps {
		var outs []Output
		var err error
		switch s.op {
		case "P":
			outs, err = e.SetProfile(s.point, s.t, s.params[0], s.params[1], s.params[2], s.params[3], s.params[4])
		case "S":
			outs, err = e.Sample(s.point, s.t, s.v)
		case "T":
			outs, err = e.Tick(s.t)
		}
		if !errors.Is(err, s.wantErr) {
			t.Fatalf("step %d (%s %s t=%d): err=%v, want %v", i, s.op, s.point, s.t, err, s.wantErr)
		}
		if got := fmtOuts(outs); !reflect.DeepEqual(got, toStringSlice(s.want)) {
			t.Fatalf("step %d (%s %s t=%d): got %v, want %v", i, s.op, s.point, s.t, got, s.want)
		}
	}
}

func toStringSlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func mustNew(t *testing.T, wn, u, q int64) *Engine {
	t.Helper()
	e, err := New(wn, u, q)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", wn, u, q, err)
	}
	return e
}

// 规格正文示例的完整回放。
func TestSpecExample(t *testing.T) {
	e := mustNew(t, 1000, 2, 2)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 30, v: 56},                            // 越死区，dc=max(100,30)=100，暂不报
		{op: "S", point: "a", t: 60, v: 55},                            // 差恰为 5，待报消失
		{op: "S", point: "a", t: 90, v: 58},                            // dc=100
		{op: "T", t: 100, want: []string{"Report(a,100,58,Trailing)"}}, // 窗口 0 用满
		{op: "S", point: "a", t: 250, v: 70},                           // 窗口 0 已满，defer=1000
		{op: "S", point: "a", t: 400, v: 60},                           // 差 2，待报消失，defer 保留
		{op: "T", t: 1000},                                             // 无输出
		{op: "T", t: 1100, want: []string{"Report(a,1100,60,Heartbeat)"}},
		{op: "S", point: "a", t: 1150, v: 65},  // 基准已移到 60，差恰为 5 不报
		{op: "S", point: "a", t: 1200, v: 150}, // 无效，bad=1
		{op: "S", point: "a", t: 1210, v: -1, want: []string{"Fault(a,1210)"}},
		{op: "S", point: "a", t: 1300, v: 65, want: []string{"Recover(a,1300)", "Report(a,1300,65,Recover)"}},
	})
	if e.Throttled != 1 {
		t.Fatalf("Throttled=%d, want 1", e.Throttled)
	}
}

// Tick 任意切分等价：先 Tick(t1) 再 Tick(t2) 与直接 Tick(t2) 输出相同。
func TestTickSplittingEquivalence(t *testing.T) {
	setup := []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "P", point: "b", t: 0, params: [5]int64{0, 50, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "b", t: 0, v: 20, want: []string{"Report(b,0,20,First)"}},
		{op: "S", point: "a", t: 30, v: 90},
		{op: "S", point: "b", t: 40, v: 80},
	}
	var want []string
	e1 := mustNew(t, 1000, 2, 2)
	runSteps(t, e1, setup)
	outs, err := e1.Tick(2000)
	if err != nil {
		t.Fatal(err)
	}
	want = fmtOuts(outs)

	e2 := mustNew(t, 1000, 2, 2)
	runSteps(t, e2, setup)
	var got []string
	for _, ts := range []int64{40, 41, 57, 100, 100, 258, 999, 1000, 1500, 2000} {
		outs, err := e2.Tick(ts)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, fmtOuts(outs)...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split ticks: got %v, want %v", got, want)
	}
}

// 故障进入使待报作废；恢复绕过额度（窗口已满仍发 Recover+Report）。
func TestFaultRecoverBypassQuota(t *testing.T) {
	e := mustNew(t, 1000, 1, 2)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{0, 1, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 10, want: []string{"Report(a,0,10,First)"}}, // 窗口 0 用满
		{op: "S", point: "a", t: 50, v: 90},                                        // dc=50，窗口 0 满，defer=1000，待报挂起
		{op: "S", point: "a", t: 100, v: 200},                                      // 无效，bad=1
		{op: "S", point: "a", t: 200, v: 300, want: []string{"Fault(a,200)"}},      // bad=2，进入故障，待报作废
		{op: "T", t: 1500}, // 故障期间无任何事件
		// 恢复：Recover 与 Report(Recover) 不占也不受额度限制（窗口 1 的额度未被消耗）。
		{op: "S", point: "a", t: 1600, v: 50, want: []string{"Recover(a,1600)", "Report(a,1600,50,Recover)"}},
		// 故障前的待报（值 90）已作废：之后只有心跳（值 50）。
		{op: "T", t: 2600, want: []string{"Report(a,2600,50,Heartbeat)"}},
	})
	if e.Throttled != 1 {
		t.Fatalf("Throttled=%d, want 1", e.Throttled)
	}
}

// 故障阈值为 Q：bad 恰达到 Q 才故障，恢复后 bad 重新计数。
func TestFaultThreshold(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{0, 1, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 10, want: []string{"Report(a,0,10,First)"}},
		{op: "S", point: "a", t: 10, v: -1},
		{op: "S", point: "a", t: 20, v: -1},
		// 有效样本使 bad 清零；db=0 且 minI=1，立即报 Change。
		{op: "S", point: "a", t: 30, v: 50, want: []string{"Report(a,30,50,Change)"}},
		{op: "S", point: "a", t: 40, v: -1},
		{op: "S", point: "a", t: 50, v: -1},
		{op: "S", point: "a", t: 60, v: -1, want: []string{"Fault(a,60)"}},
		{op: "S", point: "a", t: 70, v: -1}, // 故障期间不再发 Fault
	})
}

// 热更新后立即到期：新 minI/maxI 使事件时刻落在过去，SetProfile 的第二次 Tick 立即弹出。
func TestHotUpdateImmediateExpiry(t *testing.T) {
	e := mustNew(t, 100000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{0, 100000, 1000000, 0, 1000}},
		{op: "S", point: "a", t: 0, v: 10, want: []string{"Report(a,0,10,First)"}},
		{op: "S", point: "a", t: 100, v: 50}, // dc=max(100000,100)=100000，遥远的未来
		// 热更新 minI=10、maxI=1000：dc=max(10,100)=100 立即到期，随后心跳链式追上时钟。
		{op: "P", point: "a", t: 5000, params: [5]int64{0, 10, 1000, 0, 1000},
			want: []string{
				"Report(a,100,50,Change)",
				"Report(a,1100,50,Heartbeat)",
				"Report(a,2100,50,Heartbeat)",
				"Report(a,3100,50,Heartbeat)",
				"Report(a,4100,50,Heartbeat)",
			}},
	})
}

// 热更新保留运行态：lastV/lastAt/cur/bad/故障/defer 不变，已登记样本不按新量程追溯。
func TestHotUpdateKeepsState(t *testing.T) {
	e := mustNew(t, 100000, 10, 2)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 10, v: 90}, // dc=110，量程 [0,100] 内有效
		// 热更新收窄量程为 [0,60]：已登记的 cur=(10,90) 不追溯判定，待报仍在。
		{op: "P", point: "a", t: 20, params: [5]int64{5, 100, 1000, 0, 60}},
		{op: "T", t: 110, want: []string{"Report(a,100,90,Trailing)"}},
		// 死区基准与心跳随新参数继续工作。
		{op: "T", t: 1110, want: []string{"Report(a,1100,90,Heartbeat)"}},
	})
}

// 错误优先级：ErrInvalid > ErrClockBack > ErrNoPoint；被拒操作不改状态。
func TestErrorPrecedence(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 100, v: 60, want: []string{"Report(a,100,60,Change)"}},
		// t 越界（参数非法）优先于测点不存在。
		{op: "S", point: "zz", t: -1, v: 0, wantErr: profile.ErrInvalid},
		{op: "S", point: "zz", t: profile.MaxT + 1, v: 0, wantErr: profile.ErrInvalid},
		// 取值越界为参数非法。
		{op: "S", point: "a", t: 200, v: profile.MaxAbsV + 1, wantErr: profile.ErrInvalid},
		// 单次推进超过 1e7 为参数非法。
		{op: "T", t: 100 + profile.MaxStep + 1, wantErr: profile.ErrInvalid},
		// 时钟回退优先于测点不存在。
		{op: "S", point: "zz", t: 50, v: 0, wantErr: profile.ErrClockBack},
		{op: "T", t: 99, wantErr: profile.ErrClockBack},
		// 测点不存在。
		{op: "S", point: "zz", t: 200, v: 0, wantErr: profile.ErrNoPoint},
		// SetProfile 参数非法。
		{op: "P", point: "a", t: 200, params: [5]int64{5, 1000, 1000, 0, 100}, wantErr: profile.ErrInvalid},
		{op: "P", point: "a", t: 200, params: [5]int64{5, 100, 999, 0, 100}, wantErr: profile.ErrInvalid},
		{op: "P", point: "a", t: 200, params: [5]int64{5, 100, 1000, 50, 40}, wantErr: profile.ErrInvalid},
		// SetProfile 时钟回退。
		{op: "P", point: "a", t: 50, params: [5]int64{5, 100, 1000, 0, 100}, wantErr: profile.ErrClockBack},
	})
	// 被拒操作未改任何状态：行为与未发生这些操作时完全一致。
	e2 := mustNew(t, 1000, 10, 3)
	runSteps(t, e2, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 100, v: 60, want: []string{"Report(a,100,60,Change)"}},
	})
	for _, ts := range []int64{500, 1500, 2500} {
		o1, err1 := e.Tick(ts)
		o2, err2 := e2.Tick(ts)
		if err1 != nil || err2 != nil {
			t.Fatalf("Tick(%d): %v %v", ts, err1, err2)
		}
		if !reflect.DeepEqual(fmtOuts(o1), fmtOuts(o2)) {
			t.Fatalf("Tick(%d): rejected ops changed state: %v vs %v", ts, o1, o2)
		}
	}
}

// New 的全局参数校验。
func TestNewInvalid(t *testing.T) {
	for _, g := range [][3]int64{
		{0, 1, 1}, {1_000_000_001, 1, 1},
		{1, 0, 1}, {1, 10_001, 1},
		{1, 1, 0}, {1, 1, 101},
	} {
		if _, err := New(g[0], g[1], g[2]); !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("New%v: err=%v, want ErrInvalid", g, err)
		}
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Errorf("New(1,1,1): %v", err)
	}
}

// popped 不变量：一次 Tick 弹出的堆条目数不超过上报数+推迟数+1，与测点总数无关。
// 测点 100 与 10000 两档、同样产生 3 条上报，popped 应相同。
func TestPoppedInvariant(t *testing.T) {
	poppedAt := map[int]int64{}
	for _, n := range []int{100, 10000} {
		e := mustNew(t, 100, 10_000, 2)
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("p%05d", i)
			if _, err := e.SetProfile(name, 0, 0, 100, 1_000_000_000, -1000, 1000); err != nil {
				t.Fatal(err)
			}
			// 每测点一条 First（t=0，窗口 0 额度恰好够用），心跳事件在 1e9 处入堆。
			if _, err := e.Sample(name, 0, int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := e.Sample(fmt.Sprintf("p%05d", i), 50, 500+int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		outs, err := e.Tick(100)
		if err != nil {
			t.Fatal(err)
		}
		if len(outs) != 3 {
			t.Fatalf("n=%d: Tick(100) 产生 %d 条输出, want 3", n, len(outs))
		}
		reports, throttled := int64(3), e.Throttled
		popped := e.Popped()
		if popped > reports+throttled+1 {
			t.Fatalf("n=%d: popped=%d > reports(%d)+throttled(%d)+1", n, popped, reports, throttled)
		}
		poppedAt[n] = popped
		t.Logf("n=%d: reports=3 throttled=%d popped=%d (<= %d)", n, throttled, popped, reports+throttled+1)
	}
	if poppedAt[100] != poppedAt[10000] {
		t.Fatalf("popped 随测点数变化: n=100 -> %d, n=10000 -> %d", poppedAt[100], poppedAt[10000])
	}
}

// 并发调用等价于某个串行顺序（在 -race 下运行以检测数据竞争）。
func TestConcurrent(t *testing.T) {
	e := mustNew(t, 100, 1_000, 3)
	for i := 0; i < 8; i++ {
		if _, err := e.SetProfile(fmt.Sprintf("p%d", i), 0, 1, 10, 1000, -100, 100); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := fmt.Sprintf("p%d", w)
			for i := 1; i <= 200; i++ {
				_, _ = e.Sample(name, int64(i), int64((i*37+w)%200-100))
				_, _ = e.Tick(int64(i))
			}
		}(w)
	}
	wg.Wait()
}

// 变体一：t=250 之后不再有样本，Tick(1000) 发出 Trailing（a=1000 大于 cur.ts=250）。
func TestTrailingAtWindowStart(t *testing.T) {
	e := mustNew(t, 1000, 2, 2)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 30, v: 56},
		{op: "S", point: "a", t: 90, v: 58},
		{op: "T", t: 100, want: []string{"Report(a,100,58,Trailing)"}},
		{op: "S", point: "a", t: 250, v: 70}, // 窗口 0 已满，defer=1000
		{op: "T", t: 1000, want: []string{"Report(a,1000,70,Trailing)"}},
	})
}

// 变体二：待报已消失而 defer=1000 时，样本恰在窗口起点到达，原因为 Change。
func TestDeferChangeAtWindowStart(t *testing.T) {
	e := mustNew(t, 1000, 2, 2)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, 0, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 30, v: 56},
		{op: "S", point: "a", t: 90, v: 58},
		{op: "T", t: 100, want: []string{"Report(a,100,58,Trailing)"}},
		{op: "S", point: "a", t: 250, v: 70}, // defer=1000
		{op: "S", point: "a", t: 400, v: 60}, // 待报消失，defer 保留
		{op: "S", point: "a", t: 1000, v: 70, want: []string{"Report(a,1000,70,Change)"}},
	})
}

// 差恰等 db 不报，差 db+1 才报。
func TestDeadbandEdge(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 1, 1000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 10, v: 55}, // 差恰为 5，不报
		{op: "S", point: "a", t: 20, v: 45}, // 反方向差恰为 5，不报
		{op: "T", t: 500},
		{op: "S", point: "a", t: 600, v: 56, want: []string{"Report(a,600,56,Change)"}},
	})
}

// 间隔恰等 minI：样本恰在 lastAt+minI 到达，dc==cur.ts，立即报 Change。
func TestMinIntervalEdge(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 50, v: 80}, // dc=100，抑制期内
		// Sample 先 Tick(100)：恰在 lastAt+minI 时刻弹出上一样本的尾随。
		{op: "S", point: "a", t: 100, v: 90, want: []string{"Report(a,100,80,Trailing)"}},
		// 间隔恰等 minI 且 dc==cur.ts：Change。
		{op: "S", point: "a", t: 200, v: 95, want: []string{"Report(a,200,95,Change)"}},
	})
}

// 尾随取抑制期内最后一个样本。
func TestTrailingTakesLastSample(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 10, v: 80},
		{op: "S", point: "a", t: 20, v: 90},
		{op: "S", point: "a", t: 30, v: 70},
		{op: "T", t: 100, want: []string{"Report(a,100,70,Trailing)"}},
	})
}

// 回到死区内则不尾随。
func TestBackWithinDeadbandNoTrailing(t *testing.T) {
	e := mustNew(t, 1000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}},
		{op: "S", point: "a", t: 10, v: 80}, // 越死区，dc=100
		{op: "S", point: "a", t: 20, v: 53}, // 回到死区内，待报消失
		{op: "T", t: 500},                   // 无尾随
		{op: "T", t: 1000, want: []string{"Report(a,1000,53,Heartbeat)"}},
	})
}

// 心跳移动死区基准：心跳后 lastV 变为心跳值。
func TestHeartbeatMovesBaseline(t *testing.T) {
	e := mustNew(t, 10000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 1000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 58, want: []string{"Report(a,0,58,First)"}},
		{op: "S", point: "a", t: 400, v: 60},                                              // 差 2，留在死区内
		{op: "T", t: 1000, want: []string{"Report(a,1000,60,Heartbeat)"}},                 // lastV 移到 60
		{op: "S", point: "a", t: 1150, v: 65},                                             // 相对 60 差恰为 5，不报
		{op: "T", t: 1500},                                                                // 无输出（若基准仍是 58 则已报）
		{op: "S", point: "a", t: 1600, v: 66, want: []string{"Report(a,1600,66,Change)"}}, // 相对 60 差 6
	})
}

// 额度推迟与 defer 保留：待报消失后 defer 仍作用于心跳。
func TestThrottleDeferRetained(t *testing.T) {
	e := mustNew(t, 1000, 1, 3)
	runSteps(t, e, []step{
		{op: "P", point: "a", t: 0, params: [5]int64{5, 100, 2000, -100, 100}},
		{op: "S", point: "a", t: 0, v: 50, want: []string{"Report(a,0,50,First)"}}, // 窗口 0 用满
		{op: "S", point: "a", t: 200, v: 90},                                       // dc=200，窗口 0 满，defer=1000
		{op: "S", point: "a", t: 300, v: 52},                                       // 回死区，待报消失，defer=1000 保留
		{op: "T", t: 1000},                                                         // 心跳 dh=max(2000,1000)=2000，无输出
		{op: "T", t: 2000, want: []string{"Report(a,2000,52,Heartbeat)"}},
	})
	if e.Throttled != 1 {
		t.Fatalf("Throttled=%d, want 1", e.Throttled)
	}
}

// 同刻多测点按名字字节序；同测点同刻待报先于心跳。
func TestSameTimeOrdering(t *testing.T) {
	e := mustNew(t, 10000, 10, 3)
	runSteps(t, e, []step{
		{op: "P", point: "b", t: 0, params: [5]int64{0, 100, 1000, -1000, 1000}},
		{op: "P", point: "a", t: 0, params: [5]int64{0, 100, 1000, -1000, 1000}},
		{op: "S", point: "b", t: 0, v: 20, want: []string{"Report(b,0,20,First)"}},
		{op: "S", point: "a", t: 0, v: 10, want: []string{"Report(a,0,10,First)"}},
		{op: "S", point: "b", t: 50, v: 200},
		{op: "S", point: "a", t: 50, v: 100},
		// 两测点 dc 同为 100：a 先于 b（字节序）。
		{op: "T", t: 100, want: []string{"Report(a,100,100,Trailing)", "Report(b,100,200,Trailing)"}},
	})
}

// 同测点同刻待报先于心跳：defer 使 dc==dh==1000，弹出的是待报而非心跳。
func TestSamePointReportBeforeHeartbeat(t *testing.T) {
	e := mustNew(t, 1000, 1, 3)
	runSteps(t, e, []step{
		{op: "P", point: "c", t: 0, params: [5]int64{0, 100, 1000, -1000, 1000}},
		{op: "S", point: "c", t: 0, v: 5, want: []string{"Report(c,0,5,First)"}}, // 窗口 0 用满
		{op: "S", point: "c", t: 100, v: 50},                                     // dc=100 被推迟，defer=1000
		// 此刻 dc=max(100,100,1000)=1000，dh=max(1000,1000)=1000，待报优先。
		{op: "T", t: 1000, want: []string{"Report(c,1000,50,Trailing)"}},
	})
	if e.Throttled != 1 {
		t.Fatalf("Throttled=%d, want 1", e.Throttled)
	}
}
