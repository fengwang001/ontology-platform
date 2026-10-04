package offline

import (
	"sync"
	"testing"

	"ontology/playback"
)

// TestTouchedBoundDownloadAndPlay 验证有效许可统计只触碰本账号记录，
// 其他账号 100 条与 10000 条两档对本账号操作的触碰数无影响。
func TestTouchedBoundDownloadAndPlay(t *testing.T) {
	for _, other := range []int{100, 10000} {
		m := NewManager(100, 100, 10000, 10000, 10000)
		must(t, m.AddAccount(0, "me"))
		must(t, m.AddAccount(0, "other"))
		must(t, m.AddTitle(0, "T", 1e12))
		if other == 100 {
			// 100 台设备各持 1 条许可（dmax=100）。
			for i := 0; i < 100; i++ {
				od := devName("o", i)
				must(t, m.Register(0, "other", od))
				must(t, m.Download(0, "other", od, "T"))
			}
		} else {
			// 单台设备对 10000 部影片各持 1 条许可（omax=10000），
			// 与 100 条档形成“他账号 100 vs 10000”的对照。
			must(t, m.Register(0, "other", "host"))
			for i := 0; i < 10000; i++ {
				otn := "OT" + itoa(i)
				must(t, m.AddTitle(0, otn, 1e12))
				must(t, m.Download(0, "other", "host", otn)) //nolint:gosec
			}
		}
		must(t, m.Register(1, "me", "d0"))
		must(t, m.Download(1, "me", "d0", "T"))

		// 续期路径只 Get 1 条。
		_, before := m.Touched()
		must(t, m.Download(2, "me", "d0", "T"))
		_, after := m.Touched()
		if delta := after - before; delta != 1 {
			t.Fatalf("other=%d renew touched delta=%d want 1", other, delta)
		}

		// Play 恰好触碰 1 条许可。
		_, before = m.Touched()
		must(t, m.Play(3, "me", "d0", "T"))
		_, after = m.Touched()
		if delta := after - before; delta != 1 {
			t.Fatalf("other=%d play touched delta=%d want 1", other, delta)
		}

		// 新签发路径：Get 1 + 遍历本账号记录（1 条），≤ 本账号记录数 + 1。
		must(t, m.AddTitle(4, "MINE", 1e12))
		must(t, m.Register(4, "me", "d1"))
		_, before = m.Touched()
		must(t, m.Download(4, "me", "d1", "MINE"))
		_, after = m.Touched()
		if delta := after - before; delta > 2+1 {
			t.Fatalf("other=%d new-issue touched delta=%d want <= 3", other, delta)
		}
	}
}

// TestTouchedBoundRegister 验证 Register 触碰的冷却记录数
// 不超过该账号尚未释放的冷却名额数 + 1。
func TestTouchedBoundRegister(t *testing.T) {
	m := NewManager(100, 1_000_000, 1000, 50, 10)
	must(t, m.AddAccount(0, "a"))
	const n = 50
	for i := 0; i < n; i++ {
		must(t, m.Register(0, "a", devName("d", i)))
	}
	for i := 0; i < n; i++ {
		must(t, m.Deregister(1, "a", devName("d", i)))
	}
	before, _ := m.Touched()
	// dX 自身不在冷却中，走满额统计：n 个在册 0、n 个未释放冷却，满（n<100 不满）。
	must(t, m.Register(2, "a", "fresh"))
	after, _ := m.Touched()
	if delta := after - before; delta > n+1 {
		t.Fatalf("register touched delta=%d want <= %d", delta, n+1)
	}

	// 再加满到 dmax：deregister 其余设备造成 100 个冷却，注册被拒也只扫一遍。
	m2 := NewManager(2, 1_000_000, 1000, 50, 10)
	must(t, m2.AddAccount(0, "a"))
	must(t, m2.Register(0, "a", "x"))
	must(t, m2.Register(0, "a", "y"))
	must(t, m2.Deregister(1, "a", "x"))
	b2, _ := m2.Touched()
	wantIs(t, m2.Register(2, "a", "z"), ErrDeviceFull)
	a2, _ := m2.Touched()
	if delta := a2 - b2; delta > 1+1 {
		t.Fatalf("full register touched delta=%d want <= 2", delta)
	}
}

func TestConcurrentSerialEquivalenceInvariants(t *testing.T) {
	m := NewManager(3, 1000, 100000, 100000, 2)
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 1e12))

	const workers = 16
	var wg sync.WaitGroup
	// 并发注册：最终在册设备数不得超过 dmax（其余应得到已满/已注册）。
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = m.Register(1, "a", devName("c", i))
		}(i)
	}
	wg.Wait()
	registered := 0
	for i := 0; i < workers; i++ {
		// 设备在册时 Status 要么放行要么报“无许可”；未在册报“设备未注册”。
		if _, err := m.Status("a", devName("c", i), "T", 2); err == nil || err == ErrNoLicense {
			registered++
		}
	}
	if registered > 3 {
		t.Fatalf("registered %d exceeds dmax=3", registered)
	}

	// 并发 Download/Play：每个已注册设备对 T 下载，有效许可数不得超过 omax。
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := devName("c", i)
			if err := m.Download(3, "a", d, "T"); err == nil {
				_ = m.Play(4, "a", d, "T")
			}
		}(i)
	}
	wg.Wait()
	valid := 0
	for i := 0; i < workers; i++ {
		info, err := m.Status("a", devName("c", i), "T", 5)
		if err == nil && (info.State == playback.StateNotPlayed || info.State == playback.StatePlaying) {
			valid++
		}
	}
	if valid > 2 {
		t.Fatalf("valid licenses %d exceed omax=2", valid)
	}
}

func devName(prefix string, i int) string {
	return prefix + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
