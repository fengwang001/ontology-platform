package vegas

import "testing"

// TestAmortizedExamination 在 1k 与 100k 次操作规模下，用非导出计数器
// 验证“累计考察窗口项数 ≤ 成功样本总数的常数倍、累计考察令牌数 ≤ 放行总数的常数倍”。
// 这里常数取 2：每个入队元素至多从队首/队尾被移除一次。
func TestAmortizedExamination(t *testing.T) {
	for _, total := range []int{1000, 100_000} {
		l := mustNew(t, Config{L0: 1_000_000, Lmin: 1, Lmax: 1_000_000,
			Alpha: 2, Beta: 4, Tmo: 1_000_000_000, Cooldown: 0, Wm: 1_000_000_000})
		rtt := int64(50)
		for i := 0; i < total; i++ {
			tk, err := l.Acquire(int64(i))
			if err != nil {
				t.Fatalf("acquire %d: %v", i, err)
			}
			if i%7 == 0 {
				rtt = int64(40 + (i/7)%50)
			}
			if err := l.Release(tk.Seq, Success, rtt, int64(i)); err != nil {
				t.Fatalf("release %d: %v", i, err)
			}
		}
		tokExam, sampExam := l.examined()
		granted, success := l.totals()
		if tokExam > 2*granted || sampExam > 2*success {
			t.Fatalf("total=%d examined(tok=%d samp=%d) > 2*(grant=%d succ=%d)",
				total, tokExam, sampExam, granted, success)
		}
		if success != int64(total) || granted != int64(total) {
			t.Fatalf("total=%d granted=%d success=%d", total, granted, success)
		}
		t.Logf("total=%d tokenExamined=%d (grant=%d ratio=%.3f) sampleExamined=%d (succ=%d ratio=%.3f)",
			total, tokExam, granted, float64(tokExam)/float64(granted),
			sampExam, success, float64(sampExam)/float64(success))
	}
}

// TestAmortizedWindowExpiry 让一次操作使大量旧样本同时过期，验证累计考察数
// 仍为成功样本总数的常数倍：每个样本在整个生命周期内只被从队首移除一次。
func TestAmortizedWindowExpiry(t *testing.T) {
	l := mustNew(t, Config{L0: 1_000_000, Lmin: 1, Lmax: 1_000_000,
		Alpha: 2, Beta: 4, Tmo: 1_000_000_000, Cooldown: 0, Wm: 100})
	const n = 50_000
	for i := 0; i < n; i++ {
		tk, _ := l.Acquire(int64(i))
		if err := l.Release(tk.Seq, Success, int64(100+i%1000), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	tk, _ := l.Acquire(n + 100000)
	if err := l.Release(tk.Seq, Success, 5000, int64(n+100000)); err != nil {
		t.Fatal(err)
	}
	_, sampExam := l.examined()
	_, success := l.totals()
	if sampExam > 2*success {
		t.Fatalf("sampleExamined=%d > 2*success=%d", sampExam, success)
	}
	t.Logf("expiry burst: sampleExamined=%d success=%d ratio=%.3f",
		sampExam, success, float64(sampExam)/float64(success))
}

// TestSingleOperationBounded 在大在途数与长窗口下验证单次操作的额外考察数
// 不随规模线性增长：放行不扫描令牌表；成功归还不做与窗口长度成正比的扫描。
func TestSingleOperationBounded(t *testing.T) {
	for _, size := range []int{1000, 100_000} {
		l := mustNew(t, Config{L0: 2*int64(size) + 10, Lmin: 1, Lmax: 2*int64(size) + 10,
			Alpha: 2, Beta: 4, Tmo: 1_000_000_000, Cooldown: 0, Wm: 1_000_000_000})
		for i := 0; i < size; i++ {
			tk, err := l.Acquire(int64(i))
			if err != nil {
				t.Fatalf("size=%d acquire %d: %v", size, i, err)
			}
			_ = tk
		}
		for i := 0; i < size; i++ {
			tk, err := l.Acquire(2*int64(size) + int64(i))
			if err != nil {
				t.Fatalf("size=%d sample acquire %d: %v", size, i, err)
			}
			if err := l.Release(tk.Seq, Success, int64(100+(i%50)), 2*int64(size)+int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		tok0, samp0 := l.examined()
		tk, err := l.Acquire(4*int64(size) + 1)
		if err != nil {
			t.Fatalf("size=%d final acquire: %v", size, err)
		}
		tok1, samp1 := l.examined()
		if d := tok1 - tok0; d != 0 {
			t.Fatalf("size=%d single acquire examined %d tokens, want 0", size, d)
		}
		if d := samp1 - samp0; d != 0 {
			t.Fatalf("size=%d acquire examined %d samples, want 0", size, d)
		}
		// 新的最大 rtt 不触发队尾淘汰；旧样本未到期，队首不淘汰，增量为 0。
		if err := l.Release(tk.Seq, Success, 1_000_000, 4*int64(size)+2); err != nil {
			t.Fatal(err)
		}
		tok2, samp2 := l.examined()
		if d := samp2 - samp1; d != 0 {
			t.Fatalf("size=%d single release examined %d window entries, want 0", size, d)
		}
		if d := tok2 - tok1; d != 0 {
			t.Fatalf("size=%d single release examined %d tokens, want 0", size, d)
		}
		t.Logf("size=%d single-op extra examinations: token=%d window=%d", size, tok2-tok0, samp2-samp0)
	}
}
