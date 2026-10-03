package rollout

import (
	"testing"
)

func exampleConfig() Config {
	return Config{N: 10, P: []int64{10, 50, 100}, S: 10, K: 2, Tol: 20, Ef: 2, Nmin: 100, R: 2, H: 30, M: 2}
}

func mustNew(t *testing.T, cfg Config) *Reviewer {
	t.Helper()
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) 被拒绝: %v", cfg, err)
	}
	return r
}

func mustStart(t *testing.T, r *Reviewer, now int64) {
	t.Helper()
	if err := r.Start(now); err != nil {
		t.Fatalf("Start(%d) 被拒绝: %v", now, err)
	}
}

func mustObserve(t *testing.T, r *Reviewer, now, dRn, dEn, dRb, dEb int64) Verdict {
	t.Helper()
	v, err := r.Observe(now, dRn, dEn, dRb, dEb)
	if err != nil {
		t.Fatalf("Observe(now=%d, %d,%d,%d,%d) 被拒绝: %v", now, dRn, dEn, dRb, dEb, err)
	}
	return v
}

func checkStatus(t *testing.T, r *Reviewer, want Status) {
	t.Helper()
	got := r.Status()
	if got != want {
		t.Fatalf("Status() = %+v, 期望 %+v", got, want)
	}
}

// TestExample 逐步复现题目给出的完整示例。
func TestExample(t *testing.T) {
	r := mustNew(t, exampleConfig())
	mustStart(t, r, 0)
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 0})

	if v := mustObserve(t, r, 5, 100, 1, 450, 4); v != VerdictSoaking {
		t.Fatalf("t=5 判定 = %s, 期望 浸泡中", v)
	}
	if v := mustObserve(t, r, 10, 100, 1, 450, 5); v != VerdictPass {
		t.Fatalf("t=10 判定 = %s, 期望 通过", v)
	}
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 0, Rn: 200, En: 2, Rb: 900, Eb: 9, Ps: 1})

	if v := mustObserve(t, r, 11, 0, 0, 0, 0); v != VerdictPass {
		t.Fatalf("t=11 判定 = %s, 期望 通过", v)
	}
	checkStatus(t, r, Status{State: StatePublishing, Batch: 1, Instances: 5, St: 11})

	if v := mustObserve(t, r, 21, 200, 6, 800, 8); v != VerdictFail {
		t.Fatalf("t=21 判定 = %s, 期望 失败", v)
	}
	checkStatus(t, r, Status{State: StatePublishing, Batch: 1, Instances: 5, St: 11, Rn: 200, En: 6, Rb: 800, Eb: 8, Fs: 1})

	if v := mustObserve(t, r, 22, 0, 0, 0, 0); v != VerdictFail {
		t.Fatalf("t=22 判定 = %s, 期望 失败", v)
	}
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 52, Rollbacks: 1})

	if v := mustObserve(t, r, 60, 50, 0, 50, 0); v != VerdictSoaking {
		t.Fatalf("t=60 判定 = %s, 期望 浸泡中", v)
	}
	if v := mustObserve(t, r, 62, 60, 0, 60, 0); v != VerdictPass {
		t.Fatalf("t=62 判定 = %s, 期望 通过", v)
	}
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 52, Rn: 110, En: 0, Rb: 110, Eb: 0, Ps: 1, Rollbacks: 1})
}

// TestCumDedupeAndLastBatch 验证累计实例数相同的批被删去（保留先出现者），
// 去重后重新编号，且最后一批累计恰为 N。
func TestCumDedupeAndLastBatch(t *testing.T) {
	cases := []struct {
		n    int64
		p    []int64
		want []int64
	}{
		{10, []int64{10, 50, 100}, []int64{1, 5, 10}},
		{10, []int64{5, 10, 50, 100}, []int64{1, 5, 10}}, // cum 1,1,5,10 去重
		{3, []int64{34, 67, 100}, []int64{2, 3}},         // cum 2,3,3 去重
		{7, []int64{15, 100}, []int64{2, 7}},             // ⌈1.05⌉=2
		{100, []int64{1, 2, 100}, []int64{1, 2, 100}},    // 无重复
		{1, []int64{1, 100}, []int64{1}},                 // cum 1,1 去重后只剩 1 批
		{1_000_000, []int64{100}, []int64{1_000_000}},
	}
	for _, c := range cases {
		r := mustNew(t, Config{N: c.n, P: c.p, S: 0, K: 1, Tol: 0, Ef: 0, Nmin: 0, R: 1, H: 0, M: 1})
		if len(r.cums) != len(c.want) {
			t.Fatalf("N=%d P=%v: cums=%v, 期望 %v", c.n, c.p, r.cums, c.want)
		}
		for i := range c.want {
			if r.cums[i] != c.want[i] {
				t.Fatalf("N=%d P=%v: cums=%v, 期望 %v", c.n, c.p, r.cums, c.want)
			}
		}
		if r.cums[len(r.cums)-1] != c.n {
			t.Fatalf("N=%d P=%v: 最后一批累计 %d != N", c.n, c.p, r.cums[len(r.cums)-1])
		}
	}
}

// TestSoakBoundary 验证 now 恰等于 st+S 开始检视，差 1 仍为浸泡中。
func TestSoakBoundary(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin = 10, 100, 0
	r := mustNew(t, cfg)
	mustStart(t, r, 5)
	if v := mustObserve(t, r, 14, 1, 0, 1, 0); v != VerdictSoaking {
		t.Fatalf("now=st+S-1 判定 = %s, 期望 浸泡中", v)
	}
	if v := mustObserve(t, r, 15, 0, 0, 0, 0); v != VerdictPass {
		t.Fatalf("now=st+S 判定 = %s, 期望 通过", v)
	}
	if got := r.Status().Ps; got != 1 {
		t.Fatalf("ps = %d, 期望 1（st+S-1 不计检视）", got)
	}
}

// TestNminBoundary 验证 rn 恰等于 Nmin 参与判定，差 1 为样本不足。
func TestNminBoundary(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef = 0, 100, 100, 1000
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	if v := mustObserve(t, r, 0, 99, 0, 0, 0); v != VerdictInsufficientSample {
		t.Fatalf("rn=Nmin-1 判定 = %s, 期望 样本不足", v)
	}
	if v := mustObserve(t, r, 1, 1, 0, 0, 0); v != VerdictPass {
		t.Fatalf("rn=Nmin 判定 = %s, 期望 通过", v)
	}
}

// TestEfBoundary 验证 en 恰等于 Ef 才可能失败，差 1 必通过。
func TestEfBoundary(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef, cfg.Tol = 0, 100, 1, 5, 0
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	// en=Ef-1=4，错误率远超基线，仍判通过。
	if v := mustObserve(t, r, 0, 100, 4, 100, 0); v != VerdictPass {
		t.Fatalf("en=Ef-1 判定 = %s, 期望 通过", v)
	}
	// en=Ef=5，且 5×100×100 > 0×101×100，判失败。
	if v := mustObserve(t, r, 1, 1, 1, 0, 0); v != VerdictFail {
		t.Fatalf("en=Ef 判定 = %s, 期望 失败", v)
	}
}

// TestGateEquality 验证不等式恰相等时判通过。
func TestGateEquality(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef, cfg.Tol = 0, 100, 1, 1, 20
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	// en=6,rb=5,rn=6,eb=1: 左=6×5×100=3000，右=1×6×120=720，失败。
	if v := mustObserve(t, r, 0, 6, 6, 5, 1); v != VerdictFail {
		t.Fatalf("左>右 判定 = %s, 期望 失败", v)
	}
	// 构造恰相等：en=6,rb=100,rn=100,eb=5,tol=20: 左=6×100×100=60000，右=5×100×120=60000。
	r2 := mustNew(t, cfg)
	mustStart(t, r2, 0)
	if v := mustObserve(t, r2, 0, 100, 6, 100, 5); v != VerdictPass {
		t.Fatalf("左=右 判定 = %s, 期望 通过", v)
	}
}

// TestGateBigProduct 验证乘积超过 int64 时仍按大整数精确判定。
func TestGateBigProduct(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef, cfg.Tol = 0, 100, 1, 1, 1000
	// 左=1e9×1e9×100=1e20，右=1e9×1e9×1100=1.1e21，左<右，通过。
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	if v := mustObserve(t, r, 0, 1_000_000_000, 1_000_000_000, 1_000_000_000, 1_000_000_000); v != VerdictPass {
		t.Fatalf("大数左<右 判定 = %s, 期望 通过", v)
	}
	// 左=1e20，右=1×1e9×1100=1.1e12，左>右，失败。
	r2 := mustNew(t, cfg)
	mustStart(t, r2, 0)
	if v := mustObserve(t, r2, 0, 1_000_000_000, 1_000_000_000, 1_000_000_000, 1); v != VerdictFail {
		t.Fatalf("大数左>右 判定 = %s, 期望 失败", v)
	}
}

// TestRbZero 验证 rb 为 0 时左端为 0，必判通过。
func TestRbZero(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef, cfg.Tol = 0, 100, 1, 1, 0
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	if v := mustObserve(t, r, 0, 100, 100, 0, 0); v != VerdictPass {
		t.Fatalf("rb=0 判定 = %s, 期望 通过", v)
	}
}

// TestPassThenFailClearsPs 验证通过后失败使 ps 清零。
func TestPassThenFailClearsPs(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef, cfg.Tol, cfg.R = 0, 3, 1, 1, 0, 100
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	mustObserve(t, r, 0, 10, 0, 10, 1)                           // ps=1
	mustObserve(t, r, 1, 0, 0, 0, 0)                             // ps=2
	if v := mustObserve(t, r, 2, 5, 5, 0, 0); v != VerdictFail { // en=5≥Ef，左=5000>右=1500
		t.Fatalf("判定 = %s, 期望 失败", v)
	}
	st := r.Status()
	if st.Ps != 0 || st.Fs != 1 {
		t.Fatalf("失败后 ps=%d fs=%d, 期望 ps=0 fs=1", st.Ps, st.Fs)
	}
	// 累计为 (15,5,10,1)：左=5×10×100=5000 > 右=1×15×100=1500，继续失败。
	if v := mustObserve(t, r, 3, 0, 0, 0, 0); v != VerdictFail {
		t.Fatalf("判定 = %s, 期望 失败（累计未清零）", v)
	}
	// 补基线 (4,4) 后 右=5×15×100=7500 ≥ 左=5×14×100=7000，重新累计连击。
	if v := mustObserve(t, r, 4, 0, 0, 4, 4); v != VerdictPass {
		t.Fatalf("判定 = %s, 期望 通过", v)
	}
	mustObserve(t, r, 5, 0, 0, 0, 0) // ps=2
	mustObserve(t, r, 6, 0, 0, 0, 0) // ps=3=K，晋级第 1 批
	checkStatus(t, r, Status{State: StatePublishing, Batch: 1, Instances: 5, St: 6})
}

// TestInsufficientKeepsPsFs 验证样本不足不改变 ps 与 fs。
func TestInsufficientKeepsPsFs(t *testing.T) {
	cfg := exampleConfig()
	cfg.S, cfg.K, cfg.Nmin, cfg.Ef = 0, 100, 100, 0
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	// 浸泡结束但 rn<Nmin：样本不足，ps/fs 保持 0，累计值照常累加。
	if v := mustObserve(t, r, 0, 40, 1, 40, 0); v != VerdictInsufficientSample {
		t.Fatalf("判定 = %s, 期望 样本不足", v)
	}
	if v := mustObserve(t, r, 1, 50, 1, 50, 0); v != VerdictInsufficientSample {
		t.Fatalf("判定 = %s, 期望 样本不足", v)
	}
	st := r.Status()
	if st.Ps != 0 || st.Fs != 0 {
		t.Fatalf("样本不足后 ps=%d fs=%d, 期望均为 0", st.Ps, st.Fs)
	}
	if st.Rn != 90 || st.En != 2 || st.Rb != 90 {
		t.Fatalf("样本不足后累计 = (%d,%d,%d,%d), 期望 (90,2,90,0)", st.Rn, st.En, st.Rb, st.Eb)
	}
	// 累计达 Nmin 后正常检视：Ef=0 且 eb=0，左=2×100×100>0=右，判失败。
	if v := mustObserve(t, r, 2, 10, 0, 10, 0); v != VerdictFail {
		t.Fatalf("判定 = %s, 期望 失败", v)
	}
	if st := r.Status(); st.Fs != 1 {
		t.Fatalf("检视后 fs=%d, 期望 1", st.Fs)
	}
}

// TestPromoteAtKAndDone 验证 ps 恰达 K 晋级，最后一批达 K 完成。
func TestPromoteAtKAndDone(t *testing.T) {
	cfg := exampleConfig() // 3 批，K=2，S=10，Nmin=100
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	mustObserve(t, r, 10, 100, 0, 100, 0) // ps=1，未达 K，不晋级
	if st := r.Status(); st.Batch != 0 || st.Ps != 1 {
		t.Fatalf("ps=1 时 batch=%d ps=%d, 期望 batch=0 ps=1", st.Batch, st.Ps)
	}
	mustObserve(t, r, 11, 0, 0, 0, 0) // ps=2=K，晋级第 1 批
	checkStatus(t, r, Status{State: StatePublishing, Batch: 1, Instances: 5, St: 11})
	mustObserve(t, r, 21, 100, 0, 100, 0)
	mustObserve(t, r, 22, 0, 0, 0, 0) // 晋级第 2 批
	checkStatus(t, r, Status{State: StatePublishing, Batch: 2, Instances: 10, St: 22})
	mustObserve(t, r, 32, 100, 0, 100, 0)
	mustObserve(t, r, 33, 0, 0, 0, 0) // 最后一批达 K，完成
	checkStatus(t, r, Status{State: StateDone, Batch: 2, Instances: 10, St: 22, Rn: 100, Rb: 100, Ps: 2})
}

// TestRollbackAtR 验证 fs 恰达 R 才回退，回退后累计与 ps/fs 清零、回退次数保留。
func TestRollbackAtR(t *testing.T) {
	cfg := exampleConfig() // R=2，H=30
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	mustObserve(t, r, 10, 100, 0, 100, 0)
	mustObserve(t, r, 11, 0, 0, 0, 0) // 晋级第 1 批
	if v := mustObserve(t, r, 21, 200, 6, 800, 8); v != VerdictFail {
		t.Fatalf("判定 = %s, 期望 失败", v)
	}
	if st := r.Status(); st.Batch != 1 || st.Fs != 1 || st.Rollbacks != 0 {
		t.Fatalf("fs=1 时 batch=%d fs=%d rb=%d, 期望未回退", st.Batch, st.Fs, st.Rollbacks)
	}
	mustObserve(t, r, 22, 0, 0, 0, 0) // fs=2=R，回退第 0 批
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 52, Rollbacks: 1})
}

// TestAbortAtBatch0 验证第 0 批回退即中止（实例数 0），且中止优先于冻结。
func TestAbortAtBatch0(t *testing.T) {
	cfg := exampleConfig()
	cfg.R, cfg.M = 1, 1 // 回退次数上限 M=1，第 0 批回退应中止而非冻结
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	if v := mustObserve(t, r, 10, 100, 5, 100, 0); v != VerdictFail {
		t.Fatalf("判定 = %s, 期望 失败", v)
	}
	checkStatus(t, r, Status{State: StateAborted, Batch: 0, Instances: 0, St: 0, Rn: 100, En: 5, Rb: 100, Fs: 1, Rollbacks: 1})
	// 中止后 Observe 与 Start 均被拒绝。
	if _, err := r.Observe(11, 0, 0, 0, 0); !IsReason(err, ReasonInvalidState) {
		t.Fatalf("中止后 Observe err=%v, 期望 状态不符", err)
	}
	if err := r.Start(11); !IsReason(err, ReasonInvalidState) {
		t.Fatalf("中止后 Start err=%v, 期望 状态不符", err)
	}
}

// TestRollbackSoak 验证回退后 st=now+H，重新计时浸泡。
func TestRollbackSoak(t *testing.T) {
	cfg := exampleConfig()
	cfg.R = 1
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	mustObserve(t, r, 10, 100, 0, 100, 0)
	mustObserve(t, r, 11, 0, 0, 0, 0)     // 晋级第 1 批，st=11
	mustObserve(t, r, 21, 200, 6, 800, 8) // 失败，回退第 0 批，st=21+30=51
	if st := r.Status(); st.St != 51 {
		t.Fatalf("回退后 st=%d, 期望 51", st.St)
	}
	if v := mustObserve(t, r, 60, 100, 0, 100, 0); v != VerdictSoaking {
		t.Fatalf("now=60<st+S=61 判定 = %s, 期望 浸泡中", v)
	}
	if v := mustObserve(t, r, 61, 0, 0, 0, 0); v != VerdictPass {
		t.Fatalf("now=61=st+S 判定 = %s, 期望 通过", v)
	}
}

// TestFreezeAtM 验证回退次数恰达 M 且未中止时冻结，冻结后拒绝 Observe。
func TestFreezeAtM(t *testing.T) {
	cfg := exampleConfig() // M=2，R=2
	r := mustNew(t, cfg)
	mustStart(t, r, 0)
	mustObserve(t, r, 10, 100, 0, 100, 0)
	mustObserve(t, r, 11, 0, 0, 0, 0)     // 晋级第 1 批
	mustObserve(t, r, 21, 200, 6, 800, 8) // 失败 fs=1
	mustObserve(t, r, 22, 0, 0, 0, 0)     // 失败 fs=2，回退第 0 批，rollbacks=1
	if st := r.Status(); st.State != StatePublishing || st.Rollbacks != 1 {
		t.Fatalf("state=%s rollbacks=%d, 期望 发布中/1", st.State, st.Rollbacks)
	}
	mustObserve(t, r, 62, 100, 0, 100, 0) // st=52，浸泡至 62
	mustObserve(t, r, 63, 0, 0, 0, 0)     // 晋级第 1 批
	mustObserve(t, r, 73, 200, 6, 800, 8) // 失败 fs=1
	mustObserve(t, r, 74, 0, 0, 0, 0)     // 失败 fs=2，回退第 0 批，rollbacks=2=M，冻结
	checkStatus(t, r, Status{State: StateFrozen, Batch: 0, Instances: 1, St: 104, Rollbacks: 2})
	if _, err := r.Observe(200, 0, 0, 0, 0); !IsReason(err, ReasonInvalidState) {
		t.Fatalf("冻结后 Observe err=%v, 期望 状态不符", err)
	}
	checkStatus(t, r, Status{State: StateFrozen, Batch: 0, Instances: 1, St: 104, Rollbacks: 2})
}

// TestRejectPriorityAndNoSideEffect 验证拒绝原因按 参数非法>状态不符>时钟回退
// 只报第一个，且被拒绝的操作不改变任何状态与最大 now。
func TestRejectPriorityAndNoSideEffect(t *testing.T) {
	cfg := exampleConfig()
	cfg.S = 0
	r := mustNew(t, cfg)

	// 未开始 + 增量非法（dEn>dRn）：报参数非法而非状态不符。
	if _, err := r.Observe(0, 5, 10, 0, 0); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法", err)
	}
	// 未开始 + 时钟回退不可能（maxNow=0），仅状态不符。
	if _, err := r.Observe(0, 0, 0, 0, 0); !IsReason(err, ReasonInvalidState) {
		t.Fatalf("err=%v, 期望 状态不符", err)
	}
	// now 越界：参数非法。
	if err := r.Start(1_000_000_000_000_001); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法", err)
	}
	if err := r.Start(-1); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法", err)
	}
	// 上述拒绝均未改变状态：Start 仍可用，且 maxNow 仍为 0。
	mustStart(t, r, 100)
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 100})

	// 重复 Start + now 回退：状态不符优先于时钟回退。
	if err := r.Start(50); !IsReason(err, ReasonInvalidState) {
		t.Fatalf("err=%v, 期望 状态不符", err)
	}
	// 增量越界 + now 回退：参数非法优先。
	if _, err := r.Observe(50, 1_000_000_001, 0, 0, 0); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法", err)
	}
	// 纯时钟回退。
	if _, err := r.Observe(99, 0, 0, 0, 0); !IsReason(err, ReasonClockRollback) {
		t.Fatalf("err=%v, 期望 时钟回退", err)
	}
	// 时钟回退被拒绝不改变 maxNow：now=100 仍被接受。
	if v := mustObserve(t, r, 100, 100, 0, 100, 0); v != VerdictPass {
		t.Fatalf("判定 = %s, 期望 通过", v)
	}
	// 累计值越界：参数非法，且不改变累计。
	if _, err := r.Observe(101, 1_000_000_000, 0, 0, 0); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法（累计越界）", err)
	}
	st := r.Status()
	if st.Rn != 100 || st.Rb != 100 {
		t.Fatalf("被拒绝后累计 = (%d,%d), 期望 (100,100)", st.Rn, st.Rb)
	}
	// 增量错误数大于请求数：参数非法。
	if _, err := r.Observe(102, 0, 1, 0, 0); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法（dEn>dRn）", err)
	}
	if _, err := r.Observe(102, 0, 0, 3, 4); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法（dEb>dRb）", err)
	}
	// 负增量：参数非法。
	if _, err := r.Observe(102, -1, 0, 0, 0); !IsReason(err, ReasonInvalidParam) {
		t.Fatalf("err=%v, 期望 参数非法（负增量）", err)
	}
	// 全部被拒绝后状态不变。
	checkStatus(t, r, Status{State: StatePublishing, Batch: 0, Instances: 1, St: 100, Rn: 100, Rb: 100, Ps: 1})
}

// TestNewValidation 验证构造参数校验。
func TestNewValidation(t *testing.T) {
	valid := exampleConfig()
	bad := []Config{
		{N: 0, P: []int64{100}, S: 0, K: 1, R: 1, M: 1},
		{N: 1_000_001, P: []int64{100}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: nil, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{10, 20, 30, 40, 50, 60, 70, 80, 100}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{0, 100}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{101}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{50, 50, 100}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{100, 50}, S: 0, K: 1, R: 1, M: 1},
		{N: 1, P: []int64{50}, S: 0, K: 1, R: 1, M: 1},
	}
	with := func(mut func(*Config)) Config {
		c := valid
		mut(&c)
		return c
	}
	bad = append(bad,
		with(func(c *Config) { c.S = -1 }),
		with(func(c *Config) { c.S = 1_000_000_001 }),
		with(func(c *Config) { c.K = 0 }),
		with(func(c *Config) { c.K = 101 }),
		with(func(c *Config) { c.Tol = -1 }),
		with(func(c *Config) { c.Tol = 1001 }),
		with(func(c *Config) { c.Ef = -1 }),
		with(func(c *Config) { c.Ef = 1_000_000_001 }),
		with(func(c *Config) { c.Nmin = -1 }),
		with(func(c *Config) { c.Nmin = 1_000_000_001 }),
		with(func(c *Config) { c.R = 0 }),
		with(func(c *Config) { c.R = 101 }),
		with(func(c *Config) { c.H = -1 }),
		with(func(c *Config) { c.H = 1_000_000_001 }),
		with(func(c *Config) { c.M = 0 }),
		with(func(c *Config) { c.M = 101 }),
	)
	for i, cfg := range bad {
		if _, err := New(cfg); !IsReason(err, ReasonInvalidParam) {
			t.Fatalf("bad[%d] New(%+v) err=%v, 期望 参数非法", i, cfg, err)
		}
	}
	good := []Config{
		{N: 1, P: []int64{100}, S: 0, K: 1, Tol: 0, Ef: 0, Nmin: 0, R: 1, H: 0, M: 1},
		{N: 1_000_000, P: []int64{5, 10, 20, 35, 50, 65, 80, 100}, S: 1_000_000_000, K: 100, Tol: 1000, Ef: 1_000_000_000, Nmin: 1_000_000_000, R: 100, H: 1_000_000_000, M: 100},
		valid,
	}
	for i, cfg := range good {
		if _, err := New(cfg); err != nil {
			t.Fatalf("good[%d] New(%+v) 被误拒: %v", i, cfg, err)
		}
	}
}

// TestConcurrency 验证并发调用等价于某个串行顺序（配合 -race）。
func TestConcurrency(t *testing.T) {
	cfg := Config{N: 4, P: []int64{50, 100}, S: 0, K: 100, Tol: 0, Ef: 1_000_000_000, Nmin: 0, R: 100, H: 0, M: 100}
	r := mustNew(t, cfg)
	mustStart(t, r, 0)

	const workers = 100
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			// 所有 goroutine 使用相同 now=1，均可被接受，结果与顺序无关。
			if _, err := r.Observe(1, 1, 0, 1, 0); err != nil {
				t.Errorf("Observe 被误拒: %v", err)
			}
			_ = r.Status()
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	// 100 次通过恰达 K=100，晋级第 1 批，计数清零。
	checkStatus(t, r, Status{State: StatePublishing, Batch: 1, Instances: 4, St: 1})

	// 并发 Start 竞争：恰有一个成功。
	r2 := mustNew(t, cfg)
	errs := make(chan error, 2)
	go func() { errs <- r2.Start(3) }()
	go func() { errs <- r2.Start(3) }()
	e1, e2 := <-errs, <-errs
	ok := 0
	for _, e := range []error{e1, e2} {
		if e == nil {
			ok++
		} else if !IsReason(e, ReasonInvalidState) {
			t.Fatalf("Start err=%v, 期望 nil 或 状态不符", e)
		}
	}
	if ok != 1 {
		t.Fatalf("并发 Start 成功数 = %d, 期望 1", ok)
	}
	if st := r2.Status(); st.State != StatePublishing || st.St != 3 {
		t.Fatalf("Status = %+v, 期望 发布中 st=3", st)
	}
}
