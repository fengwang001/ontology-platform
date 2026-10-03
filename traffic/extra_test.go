package traffic

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestScanBudget 用非导出计数器验证：选段扫描考察桶数不超过 B-H，且 Lookup 零扫描。
func TestScanBudget(t *testing.T) {
	for _, b := range []int{100, 10000} {
		h := b / 5
		t.Run(fmt.Sprintf("B=%d", b), func(t *testing.T) {
			o := mustNew(t, b, h, 3, 1)
			// 先用单桶实验从左到右占满可分配区，再隔桶 Release，
			// 得到“占用/他人冷却”交替布局：任何连续段长度至多 1。
			for i := h; i < b; i++ {
				if _, err := o.Claim(fmt.Sprintf("p%d", i), 1, int64(i-h+1)); err != nil {
					t.Fatalf("setup claim %d: %v", i, err)
				}
			}
			for i := h; i < b; i += 2 {
				if err := o.Release(fmt.Sprintf("p%d", i), int64(2*(b-h)+100)); err != nil {
					t.Fatalf("setup release %d: %v", i, err)
				}
			}
			// 全部冷却对他人不可用（r 很大），故长度 2 的 Claim 必从头扫到尾，一遍恰好 B-H 个桶。
			maxNow := o.maxNow + 1
			o.lastScanExamined = 0
			if _, err := o.Claim("too-big", 2, maxNow); !errIs(err, ErrCapacity) {
				t.Fatalf("want capacity, got %v", err)
			}
			if o.lastScanExamined != b-h {
				t.Fatalf("full scan examined %d, want exactly %d (single pass)", o.lastScanExamined, b-h)
			}
			// 占用实验扩大到 2：右侧紧邻的是他人冷却且不可用 -> 失败，只考察 1 个桶。
			id := fmt.Sprintf("p%d", h+1) // h+1 仍占用（h 已释放）
			o.lastScanExamined = 0
			if _, err := o.Resize(id, 2, maxNow+1); !errIs(err, ErrCapacity) {
				t.Fatalf("grow should hit other cooldown: %v", err)
			}
			if o.lastScanExamined != 1 {
				t.Fatalf("grow examined %d want 1", o.lastScanExamined)
			}
			// 对冷却桶的上一任本人而言，向右收回：h 冷却(owner=p_h)，其左侧无保留，
			// 本人重新 Claim 后右扩会遇到 h+1 的占用 -> 只考察 1 个桶即失败。
			if _, err := o.Claim(fmt.Sprintf("p%d", h), 1, maxNow+2); err != nil || o.exps[fmt.Sprintf("p%d", h)].start != h {
				t.Fatalf("owner reclaim own cooldown should land at %d", h)
			}
			o.lastScanExamined = 0
			if _, err := o.Resize(fmt.Sprintf("p%d", h), 2, maxNow+3); !errIs(err, ErrCapacity) {
				t.Fatalf("owner grow blocked by held bucket: %v", err)
			}
			if o.lastScanExamined != 1 {
				t.Fatalf("owner grow examined %d want 1", o.lastScanExamined)
			}
			// Lookup 绝不动扫描计数器。
			beforeScan := o.lastScanExamined
			for i := 0; i < 50; i++ {
				if _, err := o.Lookup(int64(i) * 7919); err != nil {
					t.Fatal(err)
				}
			}
			if o.lastScanExamined != beforeScan {
				t.Fatal("Lookup incremented scan counter")
			}
		})
	}
}

// TestDeterministicReplay：相同操作序列重放得到相同桶表、g、Lookup 结果。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	b, h := 23, 3
	ops := genOps(rng, b, h, 300)
	run := func() (string, []string, int64) {
		o := mustNew(t, b, h, 4, 3)
		for _, x := range ops {
			switch x.kind {
			case "claim":
				o.Claim(x.id, x.n, x.now)
			case "resize":
				o.Resize(x.id, x.n, x.now)
			case "release":
				o.Release(x.id, x.now)
			case "reshuffle":
				o.Reshuffle(x.now)
			}
		}
		var bk string
		for i := h; i < b; i++ {
			c := o.buckets[i]
			bk += fmt.Sprintf("%d:%s/%d/%d;", c.state, c.owner, c.rel, c.k)
		}
		lks := make([]string, 0, 64)
		for hv := int64(0); hv < 64; hv++ {
			r, err := o.Lookup(hv * 1234567)
			if err != nil {
				t.Fatal(err)
			}
			lks = append(lks, lookupKey(r, nil))
		}
		return bk, lks, o.g
	}
	bk1, lk1, g1 := run()
	bk2, lk2, g2 := run()
	if bk1 != bk2 {
		t.Fatalf("bucket table differs across replays:\n%s\n%s", bk1, bk2)
	}
	if g1 != g2 {
		t.Fatalf("generation differs: %d vs %d", g1, g2)
	}
	for i := range lk1 {
		if lk1[i] != lk2[i] {
			t.Fatalf("lookup %d differs: %q vs %q", i, lk1[i], lk2[i])
		}
	}
}

// TestConcurrentSerializability：高并发混合调用以 -race 验证；
// 不变量始终成立：区间互不相交、连续、不含保留桶；冷却 r <= maxNow。
func TestConcurrentSerializability(t *testing.T) {
	const B, H = 64, 4
	o := mustNew(t, B, H, 2, 1)
	var readerWG, writerWG sync.WaitGroup
	stop := make(chan struct{})

	// Lookup/Usage 读者：持续读取，观察不到中间状态即由 RWMutex 保证；race 检测器验证内存安全。
	for r := 0; r < 4; r++ {
		readerWG.Add(1)
		go func(seed int64) {
			defer readerWG.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := o.Lookup(rng.Int63n(int64(1) << 62)); err != nil {
					t.Errorf("lookup: %v", err)
					return
				}
				_ = o.Usage()
				_ = o.Generation()
			}
		}(int64(100 + r))
	}

	// 写者：唯一 id 空间按 worker 划分，避免“已存在”噪声；共享时钟推进者外都用较大 now。
	var clock int64 = 1
	var clockMu sync.Mutex
	for w := 0; w < 6; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			rng := rand.New(rand.NewSource(int64(w + 1)))
			for round := 0; round < 120; round++ {
				clockMu.Lock()
				clock += int64(1 + rng.Intn(3))
				now := clock
				clockMu.Unlock()
				id := fmt.Sprintf("w%dr%d", w, round)
				n := 1 + rng.Intn(4)
				if s, err := o.Claim(id, n, now); err == nil {
					defer func() {}()
					_ = s
					n2 := 1 + rng.Intn(6)
					if _, err := o.Resize(id, n2, now+1); err != nil {
						// 容量不足时直接释放
						_ = o.Release(id, now+1)
					} else {
						_ = o.Release(id, now+2)
					}
				}
				if rng.Intn(10) == 0 {
					o.Reshuffle(now + 3)
				}
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	// 终态不变量校验。
	seen := map[int]string{}
	for id, rng := range o.exps {
		if rng.start < H || rng.end > B || rng.end <= rng.start {
			t.Fatalf("range %s [%d,%d) violates bounds", id, rng.start, rng.end)
		}
		for i := rng.start; i < rng.end; i++ {
			if prev, dup := seen[i]; dup {
				t.Fatalf("bucket %d double owned by %s and %s", i, prev, id)
			}
			seen[i] = id
			if b := o.buckets[i]; b.state != StateHeld || b.owner != id {
				t.Fatalf("bucket %d not consistently held by %s: %+v", i, id, b)
			}
		}
	}
	for i := H; i < B; i++ {
		if b := o.buckets[i]; b.state == StateCooldown && b.rel > o.maxNow {
			t.Fatalf("cooldown r=%d > maxNow=%d", b.rel, o.maxNow)
		}
	}
	if u := o.Usage(); u.Free+u.Held+u.Cooldown != B-H {
		t.Fatalf("usage sums to %d, want %d", u.Free+u.Held+u.Cooldown, B-H)
	}
}
