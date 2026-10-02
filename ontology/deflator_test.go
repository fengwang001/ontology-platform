package ontology

import (
	"errors"
	"testing"
)

func exampleCfg() Config {
	return Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1000, W: 3, ThU: 25, ThD: 50, Kc: 2,
		C0: 2, Pool: 65536, H: 3,
	}
}

func TestSpecExampleFirstHalf(t *testing.T) {
	d, err := NewDeflator(exampleCfg())
	if err != nil {
		t.Fatal(err)
	}

	r1, err := d.Sample(100000, 1000)
	if err != nil || r1.Action != ActionPending || r1.R != 100000 || r1.Cand != 32768 || r1.Cur != 4096 || r1.Streak != 1 {
		t.Fatalf("sample1: %+v %v", r1, err)
	}
	r2, err := d.Sample(100000, 1000)
	if err != nil || r2.Action != ActionApplied || r2.Cur != 32768 || r2.R != 100000 || r2.Cand != 32768 {
		t.Fatalf("sample2: %+v %v", r2, err)
	}
	r3, err := d.Sample(1, 1)
	if err != nil || r3.Action != ActionSkipped || r3.Cur != 32768 {
		t.Fatalf("sample3: %+v %v", r3, err)
	}
	st := d.State()
	if st.Polluted != false || st.Streak != 0 || st.WindowLen != 2 || st.Gap != 0 || st.LastDir != DirUp {
		t.Fatalf("post-skip state: %+v", st)
	}

	r4, err := d.Sample(20000, 1000)
	if err != nil || r4.Action != ActionHold || r4.R != 73333 || r4.Cand != 32768 {
		t.Fatalf("sample4: %+v %v", r4, err)
	}
	r5, err := d.Sample(0, 1000)
	if err != nil || r5.Action != ActionHold || r5.R != 40000 || r5.Cand != 19456 {
		t.Fatalf("sample5: %+v %v", r5, err)
	}
	r6, err := d.Sample(0, 1000)
	if err != nil || r6.Action != ActionApplied || r6.R != 6666 || r6.Cand != 3072 || r6.Cur != 3072 {
		t.Fatalf("sample6: %+v %v", r6, err)
	}
	st = d.State()
	if st.Damp != 3 || st.Gap != 0 || st.LastDir != DirDown || st.AppliedCount != 2 {
		t.Fatalf("post-sample6 state: %+v", st)
	}

	a, cur, err := d.SetChannels(8)
	if err != nil || a != ActionOK || cur != 3072 {
		t.Fatalf("setch8: %v %d %v", a, cur, err)
	}
	if beff(32768, 65536, 8, 1024) != 8192 {
		t.Fatalf("beff(8)")
	}
	rs, err := d.Sample(1, 1)
	if err != nil || rs.Action != ActionSkipped {
		t.Fatalf("skip after setch: %+v %v", rs, err)
	}

	r7, err := d.Sample(100000, 1000)
	if err != nil || r7.Action != ActionPending || r7.Cand != 4096 || r7.Streak != 1 || r7.R != 33333 {
		t.Fatalf("sample7: %+v %v", r7, err)
	}
	if d.State().Damp != 2 {
		t.Fatalf("damp after sample7: %d", d.State().Damp)
	}
	r8, err := d.Sample(100000, 1000)
	if err != nil || r8.Action != ActionPending || r8.Cand != 8192 || r8.Streak != 2 || r8.R != 66666 {
		t.Fatalf("sample8: %+v %v", r8, err)
	}
	r9, err := d.Sample(100000, 1000)
	if err != nil || r9.Action != ActionPending || r9.Cand != 8192 || r9.Streak != 3 || r9.R != 100000 {
		t.Fatalf("sample9: %+v %v", r9, err)
	}
	if d.State().Damp != 0 {
		t.Fatalf("damp after sample9: %d", d.State().Damp)
	}
	r10, err := d.Sample(100000, 1000)
	if err != nil || r10.Action != ActionApplied || r10.Cur != 8192 || r10.Streak != 0 {
		t.Fatalf("sample10: %+v %v", r10, err)
	}
	st = d.State()
	if st.Damp != 0 || st.LastDir != DirUp || st.Gap != 0 {
		t.Fatalf("post-sample10 state: %+v", st)
	}
}

func TestSpecSetChannels(t *testing.T) {
	d, _ := NewDeflator(exampleCfg())
	d.Sample(100000, 1000)
	d.Sample(100000, 1000) // Applied 32768
	d.Sample(1, 1)         // Skipped
	d.Sample(20000, 1000)  // Hold
	d.Sample(0, 1000)      // Hold
	d.Sample(0, 1000)      // Applied 3072, damp=3

	// 空操作：不改任何状态
	before := d.State()
	a, cur, err := d.SetChannels(2)
	if err != nil || a != ActionNoop || cur != 3072 {
		t.Fatalf("noop: %v %d %v", a, cur, err)
	}
	after := d.State()
	if before.Gap != after.Gap || before.Damp != after.Damp || before.Streak != after.Streak {
		t.Fatal("noop changed state")
	}

	a, cur, err = d.SetChannels(8)
	if err != nil || a != ActionOK || cur != 3072 {
		t.Fatalf("setch8: %v %d %v", a, cur, err)
	}
	if d.State().Streak != 0 {
		t.Fatal("SetChannels must reset streak")
	}
	// damp 与 lastDir 不被 SetChannels 改变
	if d.State().Damp != 3 || d.State().LastDir != DirDown {
		t.Fatal("SetChannels changed damp/lastDir")
	}

	a, cur, err = d.SetChannels(32)
	if err != nil || a != ActionForced || cur != 2048 {
		t.Fatalf("setch32: %v %d %v", a, cur, err)
	}
	st := d.State()
	if !st.Polluted || st.ForcedCount != 1 || st.LastDir != DirDown || st.Gap != 0 {
		t.Fatalf("forced state: %+v", st)
	}
	// 同向（缩）不触发新的抑制：damp 仍为 3
	if st.Damp != 3 {
		t.Fatalf("same-dir forced changed damp: %d", st.Damp)
	}
	// 强制收缩后下一个未拒绝 Sample 必为 Skipped
	rs, _ := d.Sample(10, 10)
	if rs.Action != ActionSkipped {
		t.Fatalf("post-forced not skipped: %+v", rs)
	}

	a, _, err = d.SetChannels(65) // beff=0 < Bmin
	if !errors.Is(err, ErrCapacity) || a != ActionRejected {
		t.Fatalf("setch65: %v %v", a, err)
	}
	if d.State().Channels != 32 || d.State().Cur != 2048 {
		t.Fatal("rejected SetChannels changed state")
	}
	if _, _, err := d.SetChannels(0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("setch0: %v", err)
	}
	if _, _, err := d.SetChannels(10001); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("setch10001: %v", err)
	}
}

func TestWindowRoundingBeforeAndAfterFull(t *testing.T) {
	cfg := exampleCfg()
	cfg.B0 = 1024
	d, _ := NewDeflator(cfg)
	r, _ := d.Sample(10, 3) // floor(10000/3)=3333
	if r.R != 3333 {
		t.Fatalf("s1 %+v", r)
	}
	r, _ = d.Sample(11, 3) // floor(21000/6)=3500
	if r.R != 3500 {
		t.Fatalf("s2 %+v", r)
	}
	r, _ = d.Sample(12, 3) // floor(33000/9)=3666
	if r.R != 3666 || d.State().WindowLen != 3 {
		t.Fatalf("s3 %+v", r)
	}
	r, _ = d.Sample(13, 3) // 挤出最旧：(11+12+13)*1000/9=4000
	if r.R != 4000 {
		t.Fatalf("s4 %+v", r)
	}
	ws := d.State().WindowBytes
	if len(ws) != 3 || ws[0] != 11 || ws[2] != 13 {
		t.Fatalf("window order: %v", ws)
	}
}

func TestCeilDtPerAndGranularity(t *testing.T) {
	// R=333,T=1 => Dt=ceil(333/1000)=1
	cfg := Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 1024, T: 1, W: 5,
		ThU: 0, ThD: 0, Kc: 1, C0: 1, Pool: 1 << 30, H: 0}
	d, _ := NewDeflator(cfg)
	r, _ := d.Sample(1, 3)
	if r.R != 333 || r.Cand != 1024 {
		t.Fatalf("ceil dt: %+v", r)
	}
	// R=333,T=10000 => Dt=3330,C=10000 => per=ceil(3330/10000)=1 => Bmin
	d2, _ := NewDeflator(Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 1024,
		T: 10000, W: 5, ThU: 0, ThD: 0, Kc: 1, C0: 10000, Pool: 1 << 30, H: 0})
	r, _ = d2.Sample(1, 3)
	if r.Cand != 1024 {
		t.Fatalf("per ceil: %+v", r)
	}
	// R=2000,T=1000,C=3 => Dt=2000,per=667,floor/G=0 => Bmin=1024
	d3, _ := NewDeflator(Config{Bmin: 1024, Bmax: 1 << 30, G: 1024, B0: 2048,
		T: 1000, W: 5, ThU: 0, ThD: 100, Kc: 1, C0: 3, Pool: 1 << 30, H: 0})
	r, _ = d3.Sample(2, 1)
	if r.Cand != 1024 {
		t.Fatalf("granularity floor: %+v", r)
	}
}

func g1Cfg(thU, thD uint64) Config {
	return Config{Bmin: 1, Bmax: 1 << 30, G: 1, B0: 100, T: 1000, W: 1,
		ThU: thU, ThD: thD, Kc: 1, C0: 1, Pool: 1 << 30, H: 0}
}

func TestUpThresholdEqualityAndOffByOne(t *testing.T) {
	// cur=100,thU=25：恰等 cand=125 => 25*100==100*25 Applied；
	// 差 1 cand=124 => 24*100<2500 Hold。
	d, _ := NewDeflator(g1Cfg(25, 0))
	r, _ := d.Sample(124, 1000)
	if r.Cand != 124 || r.Action != ActionHold {
		t.Fatalf("up off-by-one should hold: %+v", r)
	}
	r, _ = d.Sample(125, 1000)
	if r.Cand != 125 || r.Action != ActionApplied {
		t.Fatalf("up equal should apply: %+v", r)
	}
}

func TestDownThresholdEqualityAndOffByOne(t *testing.T) {
	// cur=100,thD=50：恰等 cand=50 Applied；差 1 cand=51 Hold。
	d, _ := NewDeflator(g1Cfg(0, 50))
	r, _ := d.Sample(51, 1000)
	if r.Cand != 51 || r.Action != ActionHold {
		t.Fatalf("down off-by-one should hold: %+v", r)
	}
	r, _ = d.Sample(50, 1000)
	if r.Cand != 50 || r.Action != ActionApplied {
		t.Fatalf("down equal should apply: %+v", r)
	}
}

func TestEffAndBminBypassThreshold(t *testing.T) {
	// cand==Bmin 绕过缩小阈值：Bmin=1024, cur=1025(G=1), thD=100
	cfg := Config{Bmin: 1024, Bmax: 1 << 30, G: 1, B0: 1025, T: 1000, W: 1,
		ThU: 0, ThD: 100, Kc: 1, C0: 1, Pool: 1 << 30, H: 0}
	d, _ := NewDeflator(cfg)
	r, _ := d.Sample(1024, 1000) // 缩小 1，远小于 100%，但 cand==Bmin
	if r.Action != ActionApplied || r.Cur != 1024 {
		t.Fatalf("bmin bypass: %+v", r)
	}
	// cand==Beff 绕过增大阈值，但仍须 Kc 次确认
	cfg2 := exampleCfg()
	cfg2.C0 = 1
	cfg2.Bmax = 43008
	cfg2.Pool = 1 << 30
	cfg2.W = 1
	cfg2.Kc = 3
	cfg2.ThU = 1000
	d2, _ := NewDeflator(cfg2)
	r1, _ := d2.Sample(43008, 1000)
	if r1.Cand != 43008 || r1.Action != ActionPending || r1.Streak != 1 {
		t.Fatalf("eff bypass pending1: %+v", r1)
	}
	r2, _ := d2.Sample(43008, 1000)
	if r2.Action != ActionPending || r2.Streak != 2 {
		t.Fatalf("eff bypass pending2: %+v", r2)
	}
	r3, _ := d2.Sample(43008, 1000)
	if r3.Action != ActionApplied || r3.Cur != 43008 {
		t.Fatalf("eff bypass applied at Kc: %+v", r3)
	}
}

func TestStreakInterruptedByHoldAndDown(t *testing.T) {
	// C=1, W=2, Kc=3, thU=10：cand 超 4505增幅 >=10%。
	cfg := Config{Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1000, W: 2, ThU: 10, ThD: 50, Kc: 3,
		C0: 1, Pool: 65536, H: 0}
	d, _ := NewDeflator(cfg)
	r, _ := d.Sample(6000, 1000) // R=6000 cand=5120，增 50% => Pending 1
	if r.Action != ActionPending || r.Streak != 1 || r.Cand != 5120 {
		t.Fatalf("p1: %+v", r)
	}
	r, _ = d.Sample(2000, 1000) // R=4000 cand=4096 == cur => Hold，streak 清零
	if r.Action != ActionHold || r.Streak != 0 {
		t.Fatalf("interrupt hold: %+v", r)
	}
	r, _ = d.Sample(6000, 1000) // 窗口 (2000,6000) R=4000 => Hold
	if r.Action != ActionHold {
		t.Fatalf("hold2: %+v", r)
	}
	r, _ = d.Sample(6000, 1000) // 窗口 (6000,6000) R=6000 cand=5120 => streak 重新从 1
	if r.Action != ActionPending || r.Streak != 1 {
		t.Fatalf("restart streak: %+v", r)
	}

	// 缩小立即生效同样把 streak 清零：R=3000 cand=2048 缩 50%，满足 thD=50。
	d3, _ := NewDeflator(cfg)
	d3.Sample(6000, 1000)
	r3, _ := d3.Sample(0, 1000)
	if r3.Action != ActionApplied || r3.Cur != 2048 || r3.Streak != 0 {
		t.Fatalf("down interrupt: %+v", r3)
	}
}

func TestResumeKeepsPollutionGapDampAndClearsWindow(t *testing.T) {
	d, _ := NewDeflator(exampleCfg())
	d.Sample(100000, 1000)
	d.Sample(100000, 1000) // Applied up, pol=true
	d.Pause()
	if _, err := d.Sample(1, 1); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused sample: %v", err)
	}
	// 参数非法优先于暂停的顺序由 TestRejectOrder 覆盖。
	d.Resume()
	st := d.State()
	if st.Paused || st.WindowLen != 0 || st.Streak != 0 {
		t.Fatalf("resume clear: %+v", st)
	}
	if !st.Polluted || st.LastDir != DirUp || st.Damp != 0 || st.Gap != 0 {
		t.Fatalf("resume must keep pol/gap/damp/lastDir: %+v", st)
	}
	// pol 保留 => Resume 后第一个未拒绝 Sample 是 Skipped
	r, _ := d.Sample(1, 1)
	if r.Action != ActionSkipped {
		t.Fatalf("post-resume skipped: %+v", r)
	}
	if d.State().WindowLen != 0 {
		t.Fatal("skipped must not enter window")
	}

	// Pause 也不清除 pol
	d2, _ := NewDeflator(exampleCfg())
	d2.Sample(100000, 1000)
	d2.Sample(100000, 1000)
	d2.Pause()
	d2.Resume()
	if r, _ := d2.Sample(0, 1); r.Action != ActionSkipped {
		t.Fatalf("pause/resume keeps pol: %+v", r)
	}
}

func TestRejectOrderAndNoStateChange(t *testing.T) {
	d, _ := NewDeflator(exampleCfg())
	// Sample：参数非法先于暂停
	d.Pause()
	if _, err := d.Sample(1<<30+1, 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("arg before pause: %v", err)
	}
	if _, err := d.Sample(0, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("dt=0: %v", err)
	}
	if _, err := d.Sample(0, 1_000_001); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("dt too large: %v", err)
	}
	st := d.State()
	if st.WindowLen != 0 || st.AppliedCount != 0 {
		t.Fatal("rejected changed state")
	}
	d.Resume()

	// SetChannels：参数非法先于容量不足
	if _, _, err := d.SetChannels(0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("setch arg: %v", err)
	}
	if d.State().Channels != 2 {
		t.Fatal("rejected changed channels")
	}
}

func TestGapBoundaryH(t *testing.T) {
	// G=1 精确表达 cand==cur±k。C=1,W=10,Kc=1。
	// up Applied 后：Skipped 不推进 gap；随后 H 次 cand==cur 的 Hold；
	// 再用 cand==Bmin 的立即缩小（绕过缩小阈值）作为反向改变。
	build := func(h int) *Deflator {
		cfg := Config{Bmin: 50, Bmax: 1 << 30, G: 1, B0: 100, T: 1000, W: 1,
			ThU: 0, ThD: 100, Kc: 1, C0: 1, Pool: 1 << 30, H: h}
		d, err := NewDeflator(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := build(2)
	d.Sample(101, 1000)        // thU=0，增 1 即 Applied up
	d.Sample(1, 1)             // Skipped（gap 仍 0）
	d.Sample(100, 1000)        // Hold，gap=1
	r, _ := d.Sample(50, 1000) // 本样本先 gap++ => 2=H；cand==Bmin 立即缩 => damp=2
	if r.Action != ActionApplied || r.Cur != 50 || d.State().Damp != 2 {
		t.Fatalf("gap==H should damp: %+v damp=%d", r, d.State().Damp)
	}

	d2 := build(1)
	d2.Sample(101, 1000) // Applied up
	d2.Sample(1, 1)
	d2.Sample(100, 1000) // Hold gap=1
	d2.Sample(100, 1000) // Hold gap=2；下一缩小样本自增到 3
	r, _ = d2.Sample(50, 1000)
	if r.Action != ActionApplied || d2.State().Damp != 0 {
		t.Fatalf("gap==H+1 must not damp: %+v damp=%d", r, d2.State().Damp)
	}
}

func TestDampDoublesNeedAndNoDecayOnSetSample(t *testing.T) {
	cfg := exampleCfg()
	cfg.W = 1
	cfg.Kc = 2
	cfg.H = 3
	cfg.C0 = 1
	cfg.Pool = 65536
	d, _ := NewDeflator(cfg)
	// 先 up 到 32768
	d.Sample(32768, 1000)         // Pending
	r, _ := d.Sample(32768, 1000) // Applied up, damp=0（lastDir 此前为 none）
	if r.Action != ActionApplied {
		t.Fatalf("up apply: %+v", r)
	}
	d.Sample(1, 1) // Skipped（清 pol，不推进 gap/damp）
	// 立即反向下缩：gap=0<=H => damp=3（设置样本不递减）
	r, _ = d.Sample(1024, 1000) // cand=Bmin 立即缩
	if r.Action != ActionApplied || d.State().Damp != 3 || d.State().LastDir != DirDown {
		t.Fatalf("reverse sets damp: %+v", r)
	}
	d.Sample(1, 1) // Skipped，damp 不变
	if d.State().Damp != 3 {
		t.Fatal("skipped must not decay damp")
	}
	// damp>0 期间 need=2*Kc=4：streak 达到 1,2,3 都 Pending，damp 逐样本衰减
	for i, wantStreak := range []int{1, 2, 3} {
		r, _ := d.Sample(32768, 1000)
		if r.Action != ActionPending || r.Streak != wantStreak {
			t.Fatalf("damped pending %d: %+v", i, r)
		}
	}
	if d.State().Damp != 0 {
		t.Fatalf("damp decayed to 0 expected, got %d", d.State().Damp)
	}
	// damp 已为 0，need=Kc=2；当前 streak=3 >= 2 => Applied
	r, _ = d.Sample(32768, 1000)
	if r.Action != ActionApplied || r.Cur != 32768 || r.Streak != 0 {
		t.Fatalf("applied once damp over: %+v", r)
	}

	// Resume 与 SetChannels 不改 damp
	d2, _ := NewDeflator(cfg)
	d2.Sample(32768, 1000)
	d2.Sample(32768, 1000) // Applied up
	d2.Sample(1, 1)        // skip
	d2.Sample(1024, 1000)  // Applied down, damp=3
	d2.Pause()
	d2.Resume()
	if d2.State().Damp != 3 {
		t.Fatal("resume changed damp")
	}
	d2.Sample(1, 1) // clear pol
	d2.SetChannels(2)
	if d2.State().Damp != 3 {
		t.Fatal("setchannels changed damp")
	}
}

func TestForcedShrinkOscillation(t *testing.T) {
	// 强制收缩方向为缩：lastDir 为增且 gap<=H 时应触发抑制。
	cfg := exampleCfg()
	cfg.W = 1
	cfg.Kc = 1
	cfg.H = 5
	cfg.C0 = 1
	cfg.Pool = 65536
	d, _ := NewDeflator(cfg)
	d.Sample(32768, 1000) // Applied up
	d.Sample(1, 1)        // Skipped
	d.Sample(32768, 1000) // Hold gap=1
	// SetChannels(32): beff=min(32768, floor(65536/32/1024)*1024)=2048，强制收缩
	// lastDir=up gap=1<=H => damp=5
	a, cur, err := d.SetChannels(32)
	if err != nil || a != ActionForced || cur != 2048 {
		t.Fatalf("forced: %v %d %v", a, cur, err)
	}
	if d.State().Damp != 5 || d.State().Gap != 0 || d.State().LastDir != DirDown {
		t.Fatalf("forced triggered damp: %+v", d.State())
	}
	// 再次同向强制收缩不触发新抑制，damp 保留
	a, _, err = d.SetChannels(64) // beff=1024
	if err != nil || a != ActionForced {
		t.Fatalf("forced2: %v %v", a, err)
	}
	if d.State().Damp != 5 {
		t.Fatalf("same-dir forced changed damp: %d", d.State().Damp)
	}
	// gap>H 时反向强制收缩不抑制
	d2, _ := NewDeflator(cfg)
	d2.Sample(32768, 1000) // up
	d2.Sample(1, 1)
	for i := 0; i < 6; i++ {
		d2.Sample(32768, 1000) // Holds, gap=1..6
	}
	if a, _, _ := d2.SetChannels(32); a != ActionForced || d2.State().Damp != 0 {
		t.Fatalf("forced beyond H must not damp: %v damp=%d", a, d2.State().Damp)
	}
}
