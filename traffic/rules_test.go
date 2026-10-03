package traffic

import (
	"strconv"
	"testing"
)

func diffBuckets(a, b []Bucket) string {
	for i := range a {
		if a[i] != b[i] {
			return "bucket " + strconv.Itoa(i) + " differs"
		}
	}
	return ""
}

// TestReshuffleClearsKAndCooldown：Reshuffle 清空冷却、k 清零、占用不变，之后倍数重新从 1 起。
func TestReshuffleClearsKAndCooldown(t *testing.T) {
	o := mustNew(t, 6, 1, 10, 2)
	if _, err := o.Claim("a", 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Claim("b", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := o.Release("a", 5); err != nil {
		t.Fatal(err)
	}
	if g, err := o.Reshuffle(6); err != nil || g != 1 {
		t.Fatalf("Reshuffle = %d,%v want 1,nil", g, err)
	}
	for i := 1; i < 6; i++ {
		if o.buckets[i].k != 0 {
			t.Fatalf("bucket %d k=%d want 0", i, o.buckets[i].k)
		}
	}
	for _, i := range []int{1, 2} {
		b := o.buckets[i]
		if b.state != StateFree || b.owner != "" || b.rel != 0 {
			t.Fatalf("bucket %d not cleared: %+v", i, b)
		}
	}
	if rng := o.exps["b"]; rng.start != 3 || rng.end != 4 {
		t.Fatalf("held range changed: %+v", rng)
	}
	if u := o.Usage(); u != (Usage{Free: 4, Held: 1, Cooldown: 0}) {
		t.Fatalf("usage = %+v", u)
	}
	if s, err := o.Claim("a", 2, 6); err != nil || s != 1 {
		t.Fatalf("a re-claim = %d,%v want start 1", s, err)
	}
	if err := o.Release("a", 7); err != nil {
		t.Fatal(err)
	}
	if b := o.buckets[1]; b.k != 1 || b.rel != 7 {
		t.Fatalf("k should restart at 1 after reshuffle, got %+v", b)
	}

	o.g = MaxGeneration
	g0, max0, u0 := o.g, o.maxNow, o.Usage()
	if _, err := o.Reshuffle(8); !errIs(err, ErrGenerationExhausted) {
		t.Fatalf("Reshuffle at max g err = %v", err)
	}
	if o.g != g0 || o.maxNow != max0 || o.Usage() != u0 {
		t.Fatal("rejected reshuffle mutated state")
	}
}

// TestResizeRules：缩小只冷却尾部；扩容仅向右、遇占用/他人冷却/越界失败且不搬迁；空操作推进时钟。
func TestResizeRules(t *testing.T) {
	o := mustNew(t, 10, 0, 100, 1)
	if _, err := o.Claim("a", 4, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Claim("b", 2, 0); err != nil {
		t.Fatal(err) // b=[4,6)
	}
	if _, err := o.Resize("a", 2, 10); err != nil {
		t.Fatal(err)
	}
	if rng := o.exps["a"]; rng.start != 0 || rng.end != 2 {
		t.Fatalf("a = %+v want [0,2)", rng)
	}
	for _, i := range []int{2, 3} {
		b := o.buckets[i]
		if b.state != StateCooldown || b.rel != 10 || b.k != 1 {
			t.Fatalf("bucket %d = %+v want tail cooldown r=10 k=1", i, b)
		}
	}
	if o.buckets[0].state != StateHeld || o.buckets[1].state != StateHeld {
		t.Fatal("shrink must keep the head buckets held")
	}
	if _, err := o.Resize("a", 3, 11); err != nil {
		t.Fatalf("grow into own cooldown: %v", err)
	}
	if b := o.buckets[2]; b.state != StateHeld || b.owner != "a" || b.k != 1 {
		t.Fatalf("bucket2 = %+v, k must survive reclaim", b)
	}
	snapBefore := snap(o)
	if _, err := o.Resize("a", 5, 12); !errIs(err, ErrCapacity) {
		t.Fatalf("grow across held bucket err = %v", err)
	}
	if rng := o.exps["a"]; rng.start != 0 || rng.end != 3 {
		t.Fatalf("failed grow mutated range: %+v", rng)
	}
	if d := diffBuckets(snapBefore, snap(o)); d != "" {
		t.Fatalf("failed grow mutated buckets: %s", d)
	}
	if err := o.Release("b", 13); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Resize("a", 9, 14); !errIs(err, ErrCapacity) {
		t.Fatalf("grow past B-1 err = %v", err)
	}
	// 右侧他人冷却 4、5（b，r=13，至 113）；6 空闲可用，c 落在 6。
	if s, err := o.Claim("c", 1, 15); err != nil || s != 6 {
		t.Fatalf("c = %d,%v want 6", s, err)
	}
	if _, err := o.Resize("a", 8, 17); !errIs(err, ErrCapacity) {
		t.Fatalf("grow into other's cooldown err = %v", err)
	}
	// 恰等 113 时 4、5 对 a 可用；但 6 被 c 占用，所以 a 最多扩到 6 个桶。
	if _, err := o.Resize("a", 6, 113); err != nil {
		t.Fatalf("grow at exact others-available time: %v", err)
	}
	for i := 0; i < 6; i++ {
		if o.buckets[i].state != StateHeld || o.buckets[i].owner != "a" {
			t.Fatalf("bucket %d not held by a", i)
		}
	}
	if _, err := o.Resize("a", 6, 200); err != nil {
		t.Fatal(err)
	}
	if o.maxNow != 200 {
		t.Fatalf("noop resize must advance clock, maxNow=%d", o.maxNow)
	}
}

// TestExpiredCooldownStillCounted：过期但未重新分配的冷却桶仍计入 Cooldown 且保留 o、r。
func TestExpiredCooldownStillCounted(t *testing.T) {
	o := mustNew(t, 4, 0, 3, 1)
	if _, err := o.Claim("x", 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := o.Release("x", 10); err != nil {
		t.Fatal(err)
	}
	if u := o.Usage(); u != (Usage{Free: 2, Held: 0, Cooldown: 2}) {
		t.Fatalf("usage = %+v", u)
	}
	for _, i := range []int{0, 1} {
		if b := o.buckets[i]; b.owner != "x" || b.rel != 10 {
			t.Fatalf("bucket %d lost o/r: %+v", i, b)
		}
	}
	if s, err := o.Claim("y", 2, 100); err != nil || s != 0 {
		t.Fatalf("expired cooldown reclaim = %d,%v want 0", s, err)
	}
	if u := o.Usage(); u != (Usage{Free: 2, Held: 2, Cooldown: 0}) {
		t.Fatalf("usage after reclaim = %+v", u)
	}
}

// TestReleaseAndReclaimSameID：Release 后同一 id 可重新 Claim，k 继续累加。
func TestReleaseAndReclaimSameID(t *testing.T) {
	o := mustNew(t, 3, 0, 10, 1)
	for i := 1; i <= 3; i++ {
		if _, err := o.Claim("same", 1, int64(i*10)); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if o.exps["same"].start != 0 {
			t.Fatalf("should reclaim own bucket 0, got %d", o.exps["same"].start)
		}
		if err := o.Release("same", int64(i*10+1)); err != nil {
			t.Fatal(err)
		}
		if o.buckets[0].k != i {
			t.Fatalf("k = %d want %d", o.buckets[0].k, i)
		}
	}
	if err := o.Release("ghost", 100); !errIs(err, ErrExperimentNotFound) {
		t.Fatalf("release missing err = %v", err)
	}
}

// TestLookupGeneration：映射随 g 变化；H 边界对照；冷却返回 o 与对他人可用时刻。
func TestLookupGeneration(t *testing.T) {
	o := mustNew(t, 5, 2, 7, 3) // bucket(h,g)=(h+3g)%5
	if r, err := o.Lookup(0); err != nil || r.Bucket != 0 || !r.Control || r.Found || r.Cooldown {
		t.Fatalf("h=0 g0: %+v", r)
	}
	if r, _ := o.Lookup(1); !r.Control || r.Bucket != 1 {
		t.Fatalf("h=1: %+v", r)
	}
	if r, _ := o.Lookup(2); r.Control || r.Bucket != 2 || r.Found || r.Cooldown {
		t.Fatalf("h=2: %+v", r)
	}
	if _, err := o.Claim("e", 1, 0); err != nil || o.exps["e"].start != 2 {
		t.Fatal("e should get bucket 2")
	}
	if _, err := o.Claim("f", 1, 0); err != nil || o.exps["f"].start != 3 {
		t.Fatal(err)
	}
	if _, err := o.Claim("q", 1, 0); err != nil || o.exps["q"].start != 4 {
		t.Fatal(err)
	}
	if r, _ := o.Lookup(4); !r.Found || r.Experiment != "q" {
		t.Fatalf("h=4: %+v", r)
	}
	if g, err := o.Reshuffle(1); err != nil || g != 1 {
		t.Fatal(err)
	}
	if r, _ := o.Lookup(1); !r.Found || r.Experiment != "q" || r.Bucket != 4 {
		t.Fatalf("h=1 g1: %+v", r)
	}
	if r, _ := o.Lookup(0); !r.Found || r.Experiment != "f" || r.Bucket != 3 {
		t.Fatalf("h=0 g1: %+v", r)
	}
	if r, _ := o.Lookup(3); !r.Control || r.Bucket != 1 {
		t.Fatalf("h=3 g1 should hit control: %+v", r)
	}
	if err := o.Release("q", 5); err != nil {
		t.Fatal(err)
	}
	if r, _ := o.Lookup(1); !r.Cooldown || r.Found || r.PrevOwner != "q" || r.AvailableAt != 12 {
		t.Fatalf("cooldown lookup: %+v want o=q avail=12", r)
	}
	if _, err := o.Lookup(-1); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("h=-1 err=%v", err)
	}
	if _, err := o.Lookup(MaxHash + 1); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("h=2^62 err=%v", err)
	}
}

// TestRejectionPriority：按序只报第一个，且任何拒绝都不改状态。
func TestRejectionPriority(t *testing.T) {
	o := mustNew(t, 6, 1, 5, 1)
	if _, err := o.Claim("a", 2, 10); err != nil {
		t.Fatal(err)
	}

	// 1) 参数非法优先于“已存在”“时钟回退”“容量”。
	if _, err := o.Claim("", 1, 5); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("empty id err=%v", err)
	}
	// 非法 n 与时钟回退同时出现时，参数非法仍最优先。
	if _, err := o.Claim("a", 0, 1); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("invalid-n beats exists, err=%v", err)
	}
	if _, err := o.Claim("z", 0, 5); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("n=0 err=%v", err)
	}
	if _, err := o.Claim("z", 6, 5); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("n>B-H=5 err=%v", err)
	}
	if _, err := o.Claim("z", 1, -1); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("now=-1 err=%v", err)
	}
	if _, err := o.Claim("z", 1, MaxNow+1); !errIs(err, ErrInvalidArgument) {
		t.Fatalf("now>1e15 err=%v", err)
	}
	// 2) 已存在优先于时钟回退与容量。
	if _, err := o.Claim("a", 5, 1); !errIs(err, ErrExperimentExists) {
		t.Fatalf("exists priority err=%v", err)
	}
	// 3) Resize/Release：不存在优先于时钟回退。
	if _, err := o.Resize("ghost", 1, 1); !errIs(err, ErrExperimentNotFound) {
		t.Fatalf("resize missing err=%v", err)
	}
	if err := o.Release("ghost", 1); !errIs(err, ErrExperimentNotFound) {
		t.Fatalf("release missing err=%v", err)
	}
	// 4) 时钟回退优先于容量。
	if _, err := o.Claim("z", 5, 9); !errIs(err, ErrClockRewound) {
		t.Fatalf("rewind before capacity err=%v", err)
	}
	if _, err := o.Resize("a", 3, 9); !errIs(err, ErrClockRewound) {
		t.Fatalf("resize rewind err=%v", err)
	}
	if err := o.Release("a", 9); !errIs(err, ErrClockRewound) {
		t.Fatalf("release rewind err=%v", err)
	}
	if _, err := o.Reshuffle(9); !errIs(err, ErrClockRewound) {
		t.Fatalf("reshuffle rewind err=%v", err)
	}
	// 5) 世代耗尽优先于…… Reshuffle 独有的容量无关检查。
	if _, err := o.Reshuffle(10); err != nil {
		t.Fatal(err)
	}
	o.g = MaxGeneration
	if _, err := o.Reshuffle(10); !errIs(err, ErrGenerationExhausted) {
		t.Fatalf("generation exhausted err=%v", err)
	}
	// 6) 容量不足：全部桶被占满时新 Claim。
	o.g = 1
	if _, err := o.Claim("z", 5, 20); !errIs(err, ErrCapacity) {
		t.Fatalf("capacity err=%v", err)
	}

	if o.g != 1 || o.maxNow != 10 {
		t.Fatalf("state changed by rejected ops: g=%d maxNow=%d", o.g, o.maxNow)
	}
	if u := o.Usage(); u != (Usage{Free: 3, Held: 2, Cooldown: 0}) {
		t.Fatalf("usage = %+v", u)
	}
	if rng := o.exps["a"]; rng.start != 1 || rng.end != 3 {
		t.Fatalf("a = %+v", rng)
	}
}

// TestConstructorValidation 覆盖构造参数边界。
func TestConstructorValidation(t *testing.T) {
	valid := [][4]int{{2, 0, 0, 1}, {1_000_000, 999_999, 1_000_000_000, 1_000_000}}
	for _, c := range valid {
		if _, err := New(c[0], c[1], int64(c[2]), int64(c[3])); err != nil {
			t.Fatalf("New%v valid but err=%v", c, err)
		}
	}
	invalid := [][4]int{
		{1, 0, 0, 1}, {1_000_001, 0, 0, 1},
		{10, -1, 0, 1}, {10, 10, 0, 1}, // H 必须 <= B-1
		{10, 0, -1, 1}, {10, 0, 1_000_000_001, 1},
		{10, 0, 0, 0}, {10, 0, 0, 1_000_001},
	}
	for _, c := range invalid {
		if _, err := New(c[0], c[1], int64(c[2]), int64(c[3])); !errIs(err, ErrInvalidArgument) {
			t.Fatalf("New%v err=%v want invalid", c, err)
		}
	}
}
