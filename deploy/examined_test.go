package deploy

import "testing"

// buildIdleEnvs 构造 n 个环境，仅前 cascadeEnvs 个环境有 Running；
// 其余环境完全空闲，用于证明检视计数与环境总数无关。
func buildCascadeScenario(t *testing.T, n, cascadeEnvs int, chain int) *Coordinator {
	t.Helper()
	cfgs := make([]EnvConfig, n)
	for i := range cfgs {
		cfgs[i] = EnvConfig{Name: envN(i), K: 0, TTL: 1, T: 10}
	}
	c, err := New(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	alice := callerOf("alice", PermDeploy)
	for e := 0; e < cascadeEnvs; e++ {
		_, err := c.Request(0, envN(e), 1, false, alice)
		if err != nil {
			t.Fatal(err)
		}
		for v := 2; v <= chain; v++ {
			if _, err := c.Request(0, envN(e), int64(v), false, alice); err != nil {
				t.Fatal(err)
			}
		}
	}
	return c
}

func envN(i int) string {
	return "env" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestExaminedBoundTwoTiers：100 与 10000 环境两档对照。
// 同一次落地检视的 Running 记录数不超过落地数 + 1，且与环境总数无关。
func TestExaminedBoundTwoTiers(t *testing.T) {
	const chain = 8 // 每个有负载环境落地时沿队列级联 8 次
	for _, n := range []int{100, 10000} {
		c := buildCascadeScenario(t, n, 5, chain)
		// 首请求 start=0,due=10；其超时后以 g=10 授锁下一位（due=20），
		// 如此级联。Tick 到 1000 触发全部，检视数不应随 n 改变。
		if err := c.Tick(1000); err != nil {
			t.Fatal(err)
		}
		landed := c.landedLast
		examined := c.examinedLast
		if examined > landed+1 {
			t.Fatalf("n=%d examined=%d > landed+1=%d", n, examined, landed+1)
		}
		if examined >= n {
			t.Fatalf("n=%d examined=%d 随环境数增长，界被破坏", n, examined)
		}
		t.Logf("n=%d 环境：本次落地 %d 个 Running，检视 %d 个堆顶记录（界 landed+1=%d）",
			n, landed, examined, landed+1)
	}
}

// TestExaminedExtraPeek：无到期时也只检视一个堆顶记录（examined=1, landed=0）。
func TestExaminedExtraPeek(t *testing.T) {
	c := buildCascadeScenario(t, 100, 1, 2)
	if err := c.Tick(9); err != nil { // 早于 due=10，无落地
		t.Fatal(err)
	}
	if c.landedLast != 0 || c.examinedLast != 1 {
		t.Fatalf("landed=%d examined=%d, want 0/1", c.landedLast, c.examinedLast)
	}
}
