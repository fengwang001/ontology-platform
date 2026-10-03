package mirror

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/diff"
)

const errNone = ErrKind(-1)

type step struct {
	op      string // "mirror" | "primary" | "done"
	reqID   string
	method  string
	bodyLen int64
	headers map[string]string
	id      int64
	resp    *diff.Response
	now     int64

	wantReason  SkipReason
	wantID      int64
	wantHeaders map[string]string
	wantErr     ErrKind // errNone 表示期望成功
}

func runSteps(t *testing.T, m *Mirror, steps []step) {
	t.Helper()
	for i, st := range steps {
		switch st.op {
		case "mirror":
			got, err := m.Mirror(st.reqID, st.method, st.bodyLen, st.headers, st.now)
			logStep(t, i, st, got, err)
			checkErr(t, i, err, st.wantErr)
			if st.wantErr != errNone {
				continue
			}
			if got.Reason != st.wantReason {
				t.Fatalf("step %d: reason=%s, 期望 %s", i, got.Reason, st.wantReason)
			}
			if st.wantReason == SkipNone {
				if got.ID != st.wantID {
					t.Fatalf("step %d: id=%d, 期望 %d", i, got.ID, st.wantID)
				}
				if !reflect.DeepEqual(got.Headers, st.wantHeaders) {
					t.Fatalf("step %d: headers=%v, 期望 %v", i, got.Headers, st.wantHeaders)
				}
			}
		case "primary":
			err := m.Primary(st.reqID, *st.resp)
			logStep(t, i, st, Result{}, err)
			checkErr(t, i, err, st.wantErr)
		case "done":
			err := m.Done(st.id, st.resp, st.now)
			logStep(t, i, st, Result{}, err)
			checkErr(t, i, err, st.wantErr)
		}
	}
}

func logStep(t *testing.T, i int, st step, got Result, err error) {
	t.Helper()
	t.Logf("step %d: op=%s req=%q method=%s bodyLen=%d headers=%v id=%d resp=%+v now=%d -> reason=%s id=%d outHeaders=%v err=%v",
		i, st.op, st.reqID, st.method, st.bodyLen, st.headers, st.id, st.resp, st.now,
		got.Reason, got.ID, got.Headers, err)
}

func checkErr(t *testing.T, i int, err error, want ErrKind) {
	t.Helper()
	if want == errNone {
		if err != nil {
			t.Fatalf("step %d: 意外错误 %v", i, err)
		}
		return
	}
	me, ok := err.(*Error)
	if !ok {
		t.Fatalf("step %d: 期望错误类别 %v, 得到 %v", i, want, err)
	}
	if me.Kind != want {
		t.Fatalf("step %d: 错误类别=%v, 期望 %v", i, me.Kind, want)
	}
}

func baseCfg() Config {
	return Config{K: 2, Cm: 1, Bm: 10, E: 2, P: 100}
}

func get(reqID string, now int64, want SkipReason) step {
	return step{op: "mirror", reqID: reqID, method: "GET", now: now, wantReason: want, wantErr: errNone}
}

// 规格示例：k=2、Cm=1 时 GET 序列 未采中/分派/未采中/繁忙。
func TestSamplingAndBusy(t *testing.T) {
	m, err := New(baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	runSteps(t, m, []step{
		get("r1", 0, SkipNotSampled), // c=1
		{op: "mirror", reqID: "r2", method: "GET", now: 1, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}, // c=2 采中，在途 1
		get("r3", 2, SkipNotSampled), // c=3
		get("r4", 3, SkipBusy),       // c=4 采中但在途 1>=Cm，繁忙丢样本
	})
	st := m.Stats()
	if st.MirrorCalls != 4 || st.Dispatched != 1 || st.InFlight != 1 ||
		st.SkippedNotSampled != 2 || st.SkippedBusy != 1 || st.Qualified != 4 {
		t.Fatalf("stats=%+v", st)
	}
	// 繁忙丢失的样本不补发：在途释放后新请求按 c 继续计数。
	runSteps(t, m, []step{
		{op: "done", id: 1, resp: &diff.Response{Status: 200}, now: 4, wantErr: errNone},
		get("r5", 5, SkipNotSampled), // c=5
		{op: "mirror", reqID: "r6", method: "GET", now: 6, wantReason: SkipNone, wantID: 2,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}, // c=6 采中
	})
	if st := m.Stats(); st.Dispatched != 2 || st.Completed != 1 || st.InFlight != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

// 不安全/体过大/暂停不推进合格计数；体大小恰等于 Bm 放行。
func TestFiltersDoNotAdvanceCount(t *testing.T) {
	cfg := baseCfg()
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runSteps(t, m, []step{
		{op: "mirror", reqID: "u1", method: "POST", now: 0, wantReason: SkipUnsafe, wantErr: errNone},
		{op: "mirror", reqID: "b1", method: "GET", bodyLen: 11, now: 1, wantReason: SkipBodyTooLarge, wantErr: errNone},
		// 以上不推进 c；下一次 GET c=1 未采中。
		get("g1", 2, SkipNotSampled),
		// bodyLen == Bm 放行，c=2 采中。
		{op: "mirror", reqID: "g2", method: "GET", bodyLen: 10, now: 3, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone},
	})
	if st := m.Stats(); st.Qualified != 2 || st.SkippedUnsafe != 1 || st.SkippedTooLarge != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

// E=2、P=100：错误于 10、20 到达令 pausedUntil=120；now=119 暂停不推进 c，now=120 恢复。
func TestPauseLifecycle(t *testing.T) {
	m, err := New(baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	runSteps(t, m, []step{
		get("a", 0, SkipNotSampled), // c=1
		{op: "mirror", reqID: "b", method: "GET", now: 1, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}, // c=2 分派
		{op: "done", id: 1, resp: nil, now: 10, wantErr: errNone}, // s=1
		get("c", 11, SkipNotSampled),                              // c=3
		{op: "mirror", reqID: "d", method: "GET", now: 12, wantReason: SkipNone, wantID: 2,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}, // c=4 分派
		{op: "done", id: 2, resp: nil, now: 20, wantErr: errNone}, // s=2 → pausedUntil=120, s=0
		get("e", 119, SkipPaused),                                 // 暂停，c 不推进
		get("f", 119, SkipPaused),                                 // 仍暂停
		get("g", 120, SkipNotSampled),                             // 恰在 pausedUntil 恢复，c=5
		{op: "mirror", reqID: "h", method: "GET", now: 121, wantReason: SkipNone, wantID: 3,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}, // c=6 分派
	})
	st := m.Stats()
	if st.Qualified != 6 || st.PausedUntil != 120 || st.ConsecFails != 0 ||
		st.SkippedPaused != 2 || st.Dispatched != 3 || st.MirrorErrors != 2 ||
		st.Completed != 2 || st.InFlight != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

// 暂停期间的镜像错误不累计连续失败；成功在暂停期间不清零。
func TestPauseWindowErrorNotCounted(t *testing.T) {
	cfg := baseCfg()
	cfg.Cm = 3
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := func(reqID string, now, wantID int64) step {
		return step{op: "mirror", reqID: reqID, method: "GET", now: now, wantReason: SkipNone,
			wantID: wantID, wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}
	}
	runSteps(t, m, []step{
		get("s1", 0, SkipNotSampled),
		dispatch("m1", 1, 1), // c=2
		get("s2", 2, SkipNotSampled),
		dispatch("m2", 3, 2), // c=4
		get("s3", 4, SkipNotSampled),
		dispatch("m3", 5, 3),                                      // c=6
		{op: "done", id: 1, resp: nil, now: 10, wantErr: errNone}, // s=1
		{op: "done", id: 2, resp: nil, now: 20, wantErr: errNone}, // s=2 → pausedUntil=120
		{op: "done", id: 3, resp: nil, now: 50, wantErr: errNone}, // 暂停期间，s 不变
	})
	st := m.Stats()
	if st.ConsecFails != 0 || st.PausedUntil != 120 || st.MirrorErrors != 3 {
		t.Fatalf("stats=%+v", st)
	}
	// 恢复后错误重新累计。
	runSteps(t, m, []step{
		get("s4", 120, SkipNotSampled),
		dispatch("m4", 121, 4),
		{op: "done", id: 4, resp: nil, now: 122, wantErr: errNone}, // s=1
	})
	if st := m.Stats(); st.ConsecFails != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

// 成功 Done 在非暂停期清零连续失败。
func TestSuccessClearsFailures(t *testing.T) {
	cfg := baseCfg()
	cfg.E = 3
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := func(reqID string, now, wantID int64) step {
		return step{op: "mirror", reqID: reqID, method: "GET", now: now, wantReason: SkipNone,
			wantID: wantID, wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone}
	}
	runSteps(t, m, []step{
		get("s1", 0, SkipNotSampled),
		dispatch("m1", 1, 1),
		{op: "done", id: 1, resp: nil, now: 2, wantErr: errNone}, // s=1
		get("s2", 3, SkipNotSampled),
		dispatch("m2", 4, 2),
		{op: "done", id: 2, resp: nil, now: 5, wantErr: errNone}, // s=2
		get("s3", 6, SkipNotSampled),
		dispatch("m3", 7, 3),
		{op: "done", id: 3, resp: &diff.Response{Status: 200}, now: 8, wantErr: errNone}, // s=0
	})
	if st := m.Stats(); st.ConsecFails != 0 || st.CompletedOK != 1 || st.MirrorErrors != 2 {
		t.Fatalf("stats=%+v", st)
	}
}

func classifyCfg() Config {
	return Config{K: 1, Cm: 10, Bm: 0, E: 1, P: 1000, IgnoreField: map[string]bool{"date": true}}
}

func dispatchOne(t *testing.T, m *Mirror, reqID string, now int64) {
	t.Helper()
	got, err := m.Mirror(reqID, "GET", 0, nil, now)
	if err != nil || got.Reason != SkipNone {
		t.Fatalf("分派 %s 失败: reason=%s err=%v", reqID, got.Reason, err)
	}
}

// 忽略字段只影响比较：规格示例 兼容/破坏/相同。
func TestClassificationWithIgnore(t *testing.T) {
	cases := []struct {
		name    string
		primary diff.Response
		shadow  diff.Response
		want    func(Stats) int64 // 取期望为 1 的分类计数
	}{
		{"兼容", diff.Response{Status: 200, Fields: map[string]string{"a": "1", "date": "x"}},
			diff.Response{Status: 200, Fields: map[string]string{"a": "1", "b": "2", "date": "y"}},
			func(s Stats) int64 { return s.Compatible }},
		{"破坏", diff.Response{Status: 200, Fields: map[string]string{"a": "1", "date": "x"}},
			diff.Response{Status: 200, Fields: map[string]string{"a": "2"}},
			func(s Stats) int64 { return s.Breaking }},
		{"相同", diff.Response{Status: 200, Fields: map[string]string{"a": "1", "date": "x"}},
			diff.Response{Status: 200, Fields: map[string]string{"a": "1"}},
			func(s Stats) int64 { return s.Same }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New(classifyCfg())
			if err != nil {
				t.Fatal(err)
			}
			dispatchOne(t, m, "r", 0)
			if err := m.Primary("r", tc.primary); err != nil {
				t.Fatal(err)
			}
			if err := m.Done(1, &tc.shadow, 1); err != nil {
				t.Fatal(err)
			}
			st := m.Stats()
			t.Logf("primary=%+v shadow=%+v -> same=%d compatible=%d breaking=%d",
				tc.primary, tc.shadow, st.Same, st.Compatible, st.Breaking)
			if got := tc.want(st); got != 1 {
				t.Fatalf("stats=%+v, 期望分类计数为 1", st)
			}
		})
	}
}

// Done 先于 Primary 到达：Primary 到达那一刻才分类并计数。
func TestDoneBeforePrimary(t *testing.T) {
	m, err := New(classifyCfg())
	if err != nil {
		t.Fatal(err)
	}
	dispatchOne(t, m, "r", 0)
	if err := m.Done(1, &diff.Response{Status: 200, Fields: map[string]string{"a": "1"}}, 1); err != nil {
		t.Fatal(err)
	}
	st := m.Stats()
	if st.Same != 0 || st.AwaitingPrimary != 1 || st.CompletedOK != 1 {
		t.Fatalf("Done 后不应分类: stats=%+v", st)
	}
	if err := m.Primary("r", diff.Response{Status: 200, Fields: map[string]string{"a": "1"}}); err != nil {
		t.Fatal(err)
	}
	st = m.Stats()
	if st.Same != 1 || st.AwaitingPrimary != 0 {
		t.Fatalf("Primary 后应分类为相同: stats=%+v", st)
	}
}

// 错误的 Done 之后到达的 Primary 不分类也不计数。
func TestErrorDoneThenPrimary(t *testing.T) {
	m, err := New(classifyCfg())
	if err != nil {
		t.Fatal(err)
	}
	dispatchOne(t, m, "r", 0)
	if err := m.Done(1, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Primary("r", diff.Response{Status: 200}); err != nil {
		t.Fatal(err)
	}
	st := m.Stats()
	if st.Same+st.Compatible+st.Breaking != 0 || st.MirrorErrors != 1 || st.AwaitingPrimary != 0 {
		t.Fatalf("错误 Done 后不应分类: stats=%+v", st)
	}
}

// 头剥离不区分大小写，x-shadow 覆盖同名头。
func TestHeaderStripping(t *testing.T) {
	cases := []struct {
		name    string
		sh      map[string]bool
		in      map[string]string
		want    map[string]string
		wantErr ErrKind
	}{
		{
			name:    "敏感头剥离",
			sh:      map[string]bool{"authorization": true, "cookie": true},
			in:      map[string]string{"Authorization": "a", "Cookie": "c", "X-Token": "t"},
			want:    map[string]string{"x-token": "t", "x-shadow": "1"},
			wantErr: errNone,
		},
		{
			name:    "剥离不区分大小写且输出小写",
			sh:      map[string]bool{"authorization": true},
			in:      map[string]string{"AUTHORIZATION": "a", "X-CustOM": "v"},
			want:    map[string]string{"x-custom": "v", "x-shadow": "1"},
			wantErr: errNone,
		},
		{
			name:    "x-shadow 被覆盖",
			in:      map[string]string{"X-Shadow": "0"},
			want:    map[string]string{"x-shadow": "1"},
			wantErr: errNone,
		},
		{
			name:    "大小写不同的同名头非法",
			in:      map[string]string{"X-A": "1", "x-a": "2"},
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "空头名非法",
			in:      map[string]string{"": "v"},
			wantErr: ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := classifyCfg()
			cfg.SensitiveHeader = tc.sh
			m, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.Mirror("r", "GET", 0, tc.in, 0)
			t.Logf("headers=%v SH=%v -> out=%v err=%v", tc.in, tc.sh, got.Headers, err)
			if tc.wantErr != errNone {
				checkErr(t, 0, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Headers, tc.want) {
				t.Fatalf("out=%v, 期望 %v", got.Headers, tc.want)
			}
		})
	}
}

// 拒绝顺序：参数非法 > 时间非法 > 时钟回退 > 重复/未知；被拒绝的操作不改变任何状态。
func TestRejectionOrderAndAtomicity(t *testing.T) {
	m, err := New(baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	// 先建立一个已分派的 reqID 与 maxNow=5。
	runSteps(t, m, []step{
		get("s1", 4, SkipNotSampled),
		{op: "mirror", reqID: "dup", method: "GET", now: 5, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone},
	})
	before := m.Stats()

	cases := []struct {
		name string
		call func() error
		want ErrKind
	}{
		{"参数非法优先于时间非法", func() error {
			_, err := m.Mirror("", "BAD", -1, nil, 1e15+1)
			return err
		}, ErrInvalidArgument},
		{"时间非法优先于回退", func() error {
			_, err := m.Mirror("x", "GET", 0, nil, 1e15+1)
			return err
		}, ErrInvalidTime},
		{"负时间非法", func() error {
			_, err := m.Mirror("x", "GET", 0, nil, -1)
			return err
		}, ErrInvalidTime},
		{"时钟回退", func() error {
			_, err := m.Mirror("x", "GET", 0, nil, 4)
			return err
		}, ErrClockRegression},
		{"重复 reqID", func() error {
			_, err := m.Mirror("dup", "GET", 0, nil, 6)
			return err
		}, ErrDuplicate},
		{"Done 未知编号", func() error { return m.Done(99, nil, 6) }, ErrUnknown},
		{"Done 时间非法优先于未知", func() error { return m.Done(99, nil, -1) }, ErrInvalidTime},
		{"Done 回退优先于未知", func() error { return m.Done(99, nil, 4) }, ErrClockRegression},
		{"Primary 空 reqID 非法", func() error {
			return m.Primary("", diff.Response{Status: 200})
		}, ErrInvalidArgument},
		{"Primary 未知 reqID", func() error {
			return m.Primary("nobody", diff.Response{Status: 200})
		}, ErrUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			t.Logf("%s -> err=%v", tc.name, err)
			checkErr(t, 0, err, tc.want)
			after := m.Stats()
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("被拒绝的操作改变了状态:\n前=%+v\n后=%+v", before, after)
			}
		})
	}
}

// 重复登记：Primary 重复报重复；Done 重复报重复；跳过的 reqID 可再次提交。
func TestDuplicatesAndResubmit(t *testing.T) {
	m, err := New(baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	runSteps(t, m, []step{
		get("skipped", 0, SkipNotSampled), // c=1，被跳过
		// 被跳过的 reqID 可再次提交，不报重复。
		{op: "mirror", reqID: "skipped", method: "GET", now: 1, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone},
		// 已分派后再提交报重复。
		{op: "mirror", reqID: "skipped", method: "GET", now: 2, wantErr: ErrDuplicate},
		{op: "primary", reqID: "skipped", resp: &diff.Response{Status: 200}, wantErr: errNone},
		{op: "primary", reqID: "skipped", resp: &diff.Response{Status: 200}, wantErr: ErrDuplicate},
		{op: "done", id: 1, resp: &diff.Response{Status: 200}, now: 3, wantErr: errNone},
		{op: "done", id: 1, resp: &diff.Response{Status: 200}, now: 4, wantErr: ErrDuplicate},
	})
	if st := m.Stats(); st.Same != 1 || st.Completed != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

// 允许非安全方法时 POST 进入采样。
func TestAllowUnsafe(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowUnsafe = true
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runSteps(t, m, []step{
		{op: "mirror", reqID: "p1", method: "POST", now: 0, wantReason: SkipNotSampled, wantErr: errNone},
		{op: "mirror", reqID: "p2", method: "DELETE", now: 1, wantReason: SkipNone, wantID: 1,
			wantHeaders: map[string]string{"x-shadow": "1"}, wantErr: errNone},
	})
}

// 构造参数校验。
func TestNewValidation(t *testing.T) {
	good := baseCfg()
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"K 为零", func(c *Config) { c.K = 0 }},
		{"K 超界", func(c *Config) { c.K = 1e6 + 1 }},
		{"Cm 为零", func(c *Config) { c.Cm = 0 }},
		{"Bm 为负", func(c *Config) { c.Bm = -1 }},
		{"Bm 超界", func(c *Config) { c.Bm = 1e9 + 1 }},
		{"E 为零", func(c *Config) { c.E = 0 }},
		{"P 为零", func(c *Config) { c.P = 0 }},
		{"P 超界", func(c *Config) { c.P = 1e9 + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.mut(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("期望构造失败")
			}
		})
	}
	if _, err := New(good); err != nil {
		t.Fatalf("合法配置构造失败: %v", err)
	}
}

// 并发调用：结果等价于某个串行顺序，且统计不变量恒成立。
func TestConcurrentUse(t *testing.T) {
	cfg := Config{K: 3, Cm: 8, Bm: 100, E: 3, P: 50, AllowUnsafe: true}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	dispatched := map[string]int64{}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(i)
				reqID := fmt.Sprintf("w%d-r%d", w, i)
				got, err := m.Mirror(reqID, "GET", 0, nil, now)
				if err != nil {
					continue // 时钟回退等拒绝属预期
				}
				if got.Reason != SkipNone {
					continue
				}
				mu.Lock()
				dispatched[reqID] = got.ID
				mu.Unlock()
				if i%3 == 0 {
					_ = m.Done(got.ID, nil, now)
				} else {
					_ = m.Done(got.ID, &diff.Response{Status: 200}, now)
					_ = m.Primary(reqID, diff.Response{Status: 200})
				}
			}
		}(w)
	}
	wg.Wait()
	st := m.Stats()
	t.Logf("并发后统计: %+v", st)
	assertInvariants(t, cfg, st)
}

// assertInvariants 校验规格要求的全部统计不变量。
func assertInvariants(t *testing.T, cfg Config, st Stats) {
	t.Helper()
	skips := st.SkippedUnsafe + st.SkippedTooLarge + st.SkippedPaused +
		st.SkippedNotSampled + st.SkippedBusy
	if st.MirrorCalls != skips+st.Dispatched {
		t.Fatalf("MirrorCalls=%d != 跳过和 %d + 分派 %d", st.MirrorCalls, skips, st.Dispatched)
	}
	if st.SkippedNotSampled != st.Qualified-st.Qualified/cfg.K {
		t.Fatalf("未采中=%d != c-floor(c/k)=%d", st.SkippedNotSampled, st.Qualified-st.Qualified/cfg.K)
	}
	if st.Dispatched != st.InFlight+st.Completed {
		t.Fatalf("分派=%d != 在途 %d + 完成 %d", st.Dispatched, st.InFlight, st.Completed)
	}
	if st.CompletedOK != st.Same+st.Compatible+st.Breaking+st.AwaitingPrimary {
		t.Fatalf("成功完成=%d != 分类和 %d + 待主响应 %d",
			st.CompletedOK, st.Same+st.Compatible+st.Breaking, st.AwaitingPrimary)
	}
	if st.InFlight > cfg.Cm {
		t.Fatalf("在途 %d > Cm %d", st.InFlight, cfg.Cm)
	}
}
