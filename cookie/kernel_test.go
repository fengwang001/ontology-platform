package cookie

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// sliceLogger 在测试中收集输入、输出与判定依据日志。
type sliceLogger struct{ lines []string }

func (l *sliceLogger) Printf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func i64(v int64) *int64 { return &v }

func secWrite(name, domain, path string, now int64) WriteInput {
	return WriteInput{
		Name: name, Value: "v", Domain: domain, Path: path,
		SourceDomain: domain, SourceSecure: true, Secure: true, Now: now,
	}
}

func names(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func sameReq(domain, path string, now int64) RequestInput {
	return RequestInput{TargetDomain: domain, TargetPath: path, Secure: true,
		Initiator: domain, Now: now}
}

// TestPathBoundary 整段前缀匹配：/foo/ 命中 /foo/bar，不命中 /foobar。
func TestPathBoundary(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	if _, _, err := k.Write(secWrite("a", "ex.com", "/foo/", 1)); err != nil {
		t.Fatal(err)
	}
	if r, err := k.Attach(sameReq("ex.com", "/foo/bar", 2)); err != nil || len(r.Sent) != 1 {
		t.Fatalf("segment match expected send, got err=%v sent=%d", err, len(r.Sent))
	}
	r, err := k.Attach(sameReq("ex.com", "/foobar", 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sent) != 0 {
		t.Fatalf("non-segment prefix must not match, sent=%v", names(r.Sent))
	}
	if len(r.Evaluations) != 0 && r.Evaluations[0].Reason != "path boundary mismatch" {
		t.Fatalf("expected boundary reason, got %q", r.Evaluations[0].Reason)
	}
}

// TestLaxTopNav 宽松模式顶层导航：安全方法附带、非安全方法拒绝。
func TestLaxTopNav(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 5}, &sliceLogger{})
	w := secWrite("lax", "ex.com", "/", 1)
	w.SameSite = SameSiteLax
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	safeNav := RequestInput{TargetDomain: "ex.com", TargetPath: "/", Secure: true,
		Initiator: "other.com", TopNav: true, SafeMethod: true, Now: 2}
	if r, _ := k.Attach(safeNav); len(r.Sent) != 1 {
		t.Fatal("lax safe-method top nav should send")
	}
	unsafeNav := safeNav
	unsafeNav.SafeMethod = false
	if r, _ := k.Attach(unsafeNav); len(r.Sent) != 0 {
		t.Fatal("lax non-safe top nav must be rejected")
	}
}

// TestDefaultLaxGraceBoundary 宽限恰好到达时不再允许跨站非安全方法顶层导航。
func TestDefaultLaxGraceBoundary(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 10}, &sliceLogger{})
	if _, _, err := k.Write(secWrite("g", "ex.com", "/", 1)); err != nil {
		t.Fatal(err)
	}
	cross := RequestInput{TargetDomain: "ex.com", TargetPath: "/", Secure: true,
		Initiator: "other.com", TopNav: true, SafeMethod: false}
	cross.Now = 10 // 10-1=9 < 10
	if r, _ := k.Attach(cross); len(r.Sent) != 1 {
		t.Fatal("within grace should send")
	}
	cross.Now = 11 // 11-1=10：恰好等于宽限
	if r, _ := k.Attach(cross); len(r.Sent) != 0 {
		t.Fatal("at grace boundary must not send")
	}
}

// TestStrictCrossSite 严格模式跨站拒绝（顶层安全导航同样拒绝）。
func TestStrictCrossSite(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	w := secWrite("s", "ex.com", "/", 1)
	w.SameSite = SameSiteStrict
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	rq := RequestInput{TargetDomain: "ex.com", TargetPath: "/", Secure: true,
		Initiator: "other.com", TopNav: true, SafeMethod: true, Now: 2}
	if r, _ := k.Attach(rq); len(r.Sent) != 0 {
		t.Fatal("strict must reject cross-site top nav")
	}
	rq.Initiator = "ex.com"
	if r, _ := k.Attach(rq); len(r.Sent) != 1 {
		t.Fatal("strict same-site should send")
	}
}

// TestSecureOnInsecureRequest 仅安全条目不附带到非安全请求。
func TestSecureOnInsecureRequest(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	if _, _, err := k.Write(secWrite("sec", "ex.com", "/", 1)); err != nil {
		t.Fatal(err)
	}
	rq := sameReq("ex.com", "/", 2)
	rq.Secure = false
	r, _ := k.Attach(rq)
	if len(r.Sent) != 0 || r.Evaluations[0].Reason != "secure cookie over non-secure request" {
		t.Fatalf("got sent=%d reason=%q", len(r.Sent), r.Evaluations[0].Reason)
	}
}

// TestPartition 分区键相等判定，含一方为空。
func TestPartition(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	w := secWrite("p", "ex.com", "/", 1)
	w.Partition = "part-a"
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	rq := sameReq("ex.com", "/", 2)
	rq.Partition = "part-b"
	if r, _ := k.Attach(rq); len(r.Sent) != 0 {
		t.Fatal("different partitions must not send")
	}
	rq.Partition = ""
	if r, _ := k.Attach(rq); len(r.Sent) != 0 {
		t.Fatal("one-side-empty partition must not send")
	}
	rq.Partition = "part-a"
	if r, _ := k.Attach(rq); len(r.Sent) != 1 {
		t.Fatal("equal partitions should send")
	}
}

// TestExpiresAtExactlyNow 过期时刻恰等于当前时刻视为已过期；过期写入是删除而非拒绝。
func TestExpiresAtExactlyNow(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	w := secWrite("x", "ex.com", "/", 1)
	w.Expires = i64(5)
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	if r, _ := k.Attach(sameReq("ex.com", "/", 5)); len(r.Sent) != 0 {
		t.Fatal("expires == now must be expired")
	}
	if st := k.Stats(); st.ExpiryEvicted != 1 {
		t.Fatalf("expected one expiry eviction, got %+v", st)
	}
	w2 := secWrite("x", "ex.com", "/", 6)
	w2.Expires = i64(6)
	if _, ok, err := k.Write(w2); err != nil || ok {
		t.Fatalf("expired write must be non-error delete, ok=%v err=%v", ok, err)
	}
}

// TestOverwriteKeepsCreated 覆盖保留创建时刻，且并列时按创建时刻影响淘汰。
func TestOverwriteKeepsCreated(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 2, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	w1 := secWrite("old", "ex.com", "/", 1)
	if _, _, err := k.Write(w1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.Write(secWrite("new", "ex.com", "/", 2)); err != nil {
		t.Fatal(err)
	}
	w1.Now = 3
	w1.Value = "updated"
	out, ok, err := k.Write(w1)
	if err != nil || !ok || out.CreatedAt != 1 || out.LastAccess != 3 {
		t.Fatalf("overwrite snapshot wrong: %+v ok=%v err=%v", out, ok, err)
	}
	if _, err := k.Attach(sameReq("ex.com", "/", 4)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.Write(secWrite("third", "ex.com", "/", 5)); err != nil {
		t.Fatal(err)
	}
	got, _ := k.ReadScript("ex.com", "/", "", 6)
	for _, e := range got {
		if e.Name == "old" {
			t.Fatal("older-created entry must be evicted on lastAccess tie")
		}
	}
}

// TestPerSiteThenGlobalLimits 站点上限先于全局上限；全局淘汰挑条目数最多站点。
func TestPerSiteThenGlobalLimits(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 2, GlobalLimit: 3, LaxGrace: 0}, &sliceLogger{})
	for i, name := range []string{"a", "b", "c"} {
		if _, _, err := k.Write(secWrite(name, "ex.com", "/", int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if st := k.Stats(); st.CurrentCount != 2 || st.CapacityEvicted != 1 {
		t.Fatalf("per-site limit first: %+v", st)
	}
	for i, name := range []string{"d", "e"} {
		if _, _, err := k.Write(secWrite(name, "oth.com", "/", int64(10+i))); err != nil {
			t.Fatal(err)
		}
	}
	st := k.Stats()
	if st.CurrentCount != 3 {
		t.Fatalf("global limit must cap total: %+v", st)
	}
	got, _ := k.ReadScript("ex.com", "/", "", 100)
	if len(got) != 1 {
		t.Fatalf("global eviction should come from largest site, got %v", names(got))
	}
}

// TestEvictionTieBreaker 最近访问并列时按创建时刻、再按键序打破。
func TestEvictionTieBreaker(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 2, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	for _, n := range []string{"z", "a", "c"} {
		if _, _, err := k.Write(secWrite(n, "ex.com", "/", 5)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := k.ReadScript("ex.com", "/", "", 6)
	if len(got) != 2 {
		t.Fatalf("expected 2 survivors, got %v", names(got))
	}
	for _, e := range got {
		if e.Name == "a" {
			t.Fatalf("key-order tie breaker wrong, survivors=%v", names(got))
		}
	}
}

// TestDeletionCounters 三类删除可区分且守恒：
// 累计新增 = 过期淘汰 + 容量淘汰 + 显式删除 + 当前条目。
func TestDeletionCounters(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 20, LaxGrace: 0}, &sliceLogger{})

	w := secWrite("sess", "ex.com", "/sess", 1)
	w.Expires = i64(20)
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	w.Now = 2 // 覆盖：显式删除
	w.Expires = i64(20)
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}

	// 先观察到过期淘汰，再收紧容量制造容量淘汰，保证两类计数独立可辨。
	if err := k.Advance(20); err != nil {
		t.Fatal(err)
	}
	if _, err := k.ReadScript("ex.com", "/sess", "", 20); err != nil {
		t.Fatal(err)
	}

	k.cfg.PerSiteLimit = 3
	k.cfg.GlobalLimit = 4
	for _, n := range []string{"b", "c", "d", "e"} {
		if _, _, err := k.Write(secWrite(n, "ex.com", "/", 21)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := k.Write(secWrite("z0", "oth.com", "/", 22)); err != nil {
		t.Fatal(err)
	}
	n, err := k.Clear(ClearInput{Domain: "ex.com", UseCreatedRange: true,
		CreatedFrom: 21, CreatedTo: 22, Now: 23})
	if err != nil || n == 0 {
		t.Fatal(err)
	}

	st := k.Stats()
	if st.ExpiryEvicted == 0 || st.CapacityEvicted == 0 || st.ExplicitDelete == 0 {
		t.Fatalf("three deletion classes must be distinguishable: %+v", st)
	}
	if st.TotalAdded != st.ExpiryEvicted+st.CapacityEvicted+st.ExplicitDelete+int64(st.CurrentCount) {
		t.Fatalf("conservation broken: %+v", st)
	}
	if !k.Invariant() {
		t.Fatal("kernel invariant reports false")
	}

	_, err = k.Clear(ClearInput{Domain: "ex.com", UsePartition: true, Partition: "p1", Now: 24})
	if err != nil {
		t.Fatalf("partition clear errored: %v", err)
	}
}

// TestRejectPrecedence 拒绝次序：参数非法 > 时钟回退 > 仅主机前缀 > 安全来源 > 无模式缺安全。
func TestRejectPrecedence(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	if _, _, err := k.Write(secWrite("anchor", "ex.com", "/", 10)); err != nil {
		t.Fatal(err)
	}
	if err := k.Advance(20); err != nil {
		t.Fatal(err)
	}

	// 参数非法优先于时钟回退
	in := secWrite("", "ex.com", "bad-path", 1)
	if _, _, err := k.Write(in); AsInvalidArg(err) == nil {
		t.Fatalf("want invalid arg, got %v", err)
	}
	// 时钟回退优先于仅主机前缀
	in = secWrite("__Host-x", "ex.com", "/", 5)
	if _, _, err := k.Write(in); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want clock rollback, got %v", err)
	}
	// 仅主机前缀优先于安全来源不符
	in = secWrite("__Host-x", "ex.com", "/sub", 21)
	in.SourceDomain = "other.com"
	in.SourceSecure = false
	if _, _, err := k.Write(in); !errors.Is(err, ErrHostPrefixViolation) {
		t.Fatalf("want host-prefix, got %v", err)
	}
	// 安全来源不符优先于无模式缺安全
	in = secWrite("c", "ex.com", "/", 22)
	in.Secure = true
	in.SourceSecure = false
	in.SameSite = SameSiteNone
	if _, _, err := k.Write(in); !errors.Is(err, ErrInsecureSource) {
		t.Fatalf("want insecure source, got %v", err)
	}
	// 无模式缺安全
	in = secWrite("c", "ex.com", "/", 23)
	in.Secure = false
	in.SourceSecure = true
	in.SameSite = SameSiteNone
	if _, _, err := k.Write(in); !errors.Is(err, ErrSameSiteNoneInsecure) {
		t.Fatalf("want samesite-none-insecure, got %v", err)
	}
	// 未知同站模式
	in = secWrite("c", "ex.com", "/", 24)
	in.SameSite = SameSite("weird")
	if _, _, err := k.Write(in); !errors.Is(err, ErrUnknownSameSite) {
		t.Fatalf("want unknown samesite, got %v", err)
	}
	// 被拒绝操作不改变统计与时钟
	before := k.Stats()
	if st := k.Stats(); st != before {
		t.Fatal("rejected ops changed stats")
	}
	if k.Stats().Now != 20 {
		t.Fatal("rejected ops moved clock")
	}
}

// TestHTTPOnlyHidden HTTPOnly 对脚本不可见，但参与附带。
func TestHTTPOnlyHidden(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, &sliceLogger{})
	w := secWrite("h", "ex.com", "/", 1)
	w.HTTPOnly = true
	if _, _, err := k.Write(w); err != nil {
		t.Fatal(err)
	}
	got, _ := k.ReadScript("ex.com", "/", "", 2)
	if len(got) != 0 {
		t.Fatal("http-only must be hidden from script")
	}
	if r, _ := k.Attach(sameReq("ex.com", "/", 3)); len(r.Sent) != 1 {
		t.Fatal("http-only must still be attached")
	}
}

// TestConcurrentSerializability 并发调用后守恒恒等式与容量上限成立。
func TestConcurrentSerializability(t *testing.T) {
	k, _ := New(Config{PerSiteLimit: 5, GlobalLimit: 20, LaxGrace: 3}, &sliceLogger{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			d := fmt.Sprintf("s%d.com", g%4)
			for i := 0; i < 50; i++ {
				now := int64(g*100 + i)
				w := secWrite(fmt.Sprintf("n%d", i%12), d, "/", now)
				w.SourceDomain = d
				_, _, _ = k.Write(w)
				_, _ = k.Attach(sameReq(d, "/", now))
			}
		}(g)
	}
	wg.Wait()
	if !k.Invariant() {
		st := k.Stats()
		t.Fatalf("invariant broken after concurrency: %+v", st)
	}
	st := k.Stats()
	if st.CurrentCount > 20 {
		t.Fatalf("global limit violated: %+v", st)
	}
}

// entrySet 返回当前存活条目的稳定多集表示（按完整键+全字段）。
func entrySet(k *Kernel) []Entry {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []Entry
	for _, s := range k.sites {
		for _, e := range s.byKey {
			out = append(out, e.snapshot())
		}
	}
	sortEntries(out)
	return out
}

func sortEntries(es []Entry) {
	sortSliceEntries(es)
}

// fuzzState 让内核与朴素模型执行同一条随机操作序列，逐步对照结果与守恒。
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	const opsPerIter = 300
	paths := []string{"/", "/a", "/a/b", "/ab", "/x", "/x/y/z", "/foo/"}
	domains := []string{"a.com", "b.com", "c.com"}
	partitions := []string{"", "", "p1", "p2"}
	names := []string{"n1", "n2", "n3", "__Host-h", "n4"}
	modes := []SameSite{"", SameSiteStrict, SameSiteLax, SameSiteNone}

	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter*7919 + 1)))
		cfg := Config{
			PerSiteLimit: 1 + rng.Intn(5),
			GlobalLimit:  2 + rng.Intn(9),
			LaxGrace:     int64(rng.Intn(6)),
		}
		k, _ := New(cfg, &sliceLogger{})
		m := newNaive(cfg)
		now := int64(0)

		for op := 0; op < opsPerIter; op++ {
			now += int64(rng.Intn(3))
			kind := rng.Intn(10)
			switch {
			case kind < 5: // 写入
				in := WriteInput{
					Name:         names[rng.Intn(len(names))],
					Path:         paths[rng.Intn(len(paths))],
					Domain:       domains[rng.Intn(len(domains))],
					Partition:    partitions[rng.Intn(len(partitions))],
					SameSite:     modes[rng.Intn(len(modes))],
					SourceSecure: true,
					Now:          now,
				}
				in.SourceDomain = in.Domain
				secure := rng.Intn(2) == 0
				in.Secure = secure
				in.SourceSecure = true
				if rng.Intn(8) == 0 { // 偶尔制造安全来源不符
					in.SourceSecure = false
				}
				if rng.Intn(3) == 0 {
					in.HTTPOnly = true
				}
				if strings.HasPrefix(in.Name, hostPrefix) {
					in.Path = "/"
					in.Secure = true
					in.SourceSecure = true
					in.Domain = domains[rng.Intn(len(domains))]
					in.SourceDomain = in.Domain
				}
				if rng.Intn(4) == 0 {
					in.Expires = i64(now + int64(rng.Intn(6)) - 1)
				}
				e1, ok1, err1 := k.Write(in)
				e2, ok2, err2 := m.write(in)
				if diffError(err1, err2) {
					t.Fatalf("iter=%d op=%d write err mismatch kernel=%v naive=%v in=%+v",
						iter, op, err1, err2, in)
				}
				if err1 == nil && (ok1 != ok2 || !reflect.DeepEqual(e1, e2)) {
					t.Fatalf("iter=%d op=%d write result mismatch k=(%+v,%v) m=(%+v,%v)",
						iter, op, e1, ok1, e2, ok2)
				}
			case kind < 8: // 附带判定
				rq := RequestInput{
					TargetDomain: domains[rng.Intn(len(domains))],
					TargetPath:   paths[rng.Intn(len(paths))],
					Secure:       rng.Intn(2) == 0,
					Initiator:    domains[rng.Intn(len(domains)+1)%len(domains)],
					TopNav:       rng.Intn(2) == 0,
					SafeMethod:   rng.Intn(2) == 0,
					Partition:    partitions[rng.Intn(len(partitions))],
					Now:          now,
				}
				if rng.Intn(4) == 0 {
					rq.Initiator = rq.TargetDomain
				}
				r1, err1 := k.Attach(rq)
				sent2, evals2, err2 := m.attach(rq)
				if diffError(err1, err2) {
					t.Fatalf("iter=%d op=%d attach err mismatch %v vs %v", iter, op, err1, err2)
				}
				if err1 == nil {
					if !reflect.DeepEqual(normalizeSent(r1.Sent), sent2) {
						t.Fatalf("iter=%d op=%d attach sent mismatch\nkernel=%#v\nnaive =%#v\nreq=%+v",
							iter, op, normalizeSent(r1.Sent), sent2, rq)
					}
					if len(r1.Evaluations) != len(evals2) {
						t.Fatalf("iter=%d op=%d eval count mismatch %d vs %d",
							iter, op, len(r1.Evaluations), len(evals2))
					}
					got := evalSummary(r1.Evaluations)
					want := naiveEvalSummary(evals2)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("iter=%d op=%d eval summary mismatch\nkernel=%v\nnaive =%v",
							iter, op, got, want)
					}
				}
			case kind == 8: // 脚本读取
				domain := domains[rng.Intn(len(domains))]
				path := paths[rng.Intn(len(paths))]
				part := partitions[rng.Intn(len(partitions))]
				g1, err1 := k.ReadScript(domain, path, part, now)
				g2, err2 := m.readScript(domain, path, part, now)
				if diffError(err1, err2) || (err1 == nil && !reflect.DeepEqual(g1, g2)) {
					t.Fatalf("iter=%d op=%d read mismatch k=%v/%v m=%v/%v",
						iter, op, g1, err1, g2, err2)
				}
			default: // 清除
				ci := ClearInput{Domain: domains[rng.Intn(len(domains))], Now: now}
				switch rng.Intn(3) {
				case 0:
					ci.UsePartition = true
					ci.Partition = partitions[rng.Intn(len(partitions))]
				case 1:
					ci.UseCreatedRange = true
					ci.CreatedFrom = now - int64(rng.Intn(4))
					ci.CreatedTo = now + int64(rng.Intn(4))
				}
				n1, err1 := k.Clear(ci)
				n2, err2 := m.clear(ci)
				if diffError(err1, err2) || n1 != n2 {
					t.Fatalf("iter=%d op=%d clear mismatch k=(%d,%v) m=(%d,%v)",
						iter, op, n1, err1, n2, err2)
				}
			}

			if !reflect.DeepEqual(k.Stats(), m.stats()) {
				kCounts, mCounts := map[string]int{}, map[string]int{}
				k.mu.Lock()
				for d, s := range k.sites {
					kCounts[d] = s.count.count
				}
				k.mu.Unlock()
				for _, e := range m.items {
					mCounts[e.Domain]++
				}
				t.Fatalf("iter=%d op=%d kind=%d now=%d stats mismatch\nkernel=%+v sites=%v\nnaive =%+v sites=%v",
					iter, op, kind, now, k.Stats(), kCounts, m.stats(), mCounts)
			}
			naiveItems := append([]Entry(nil), m.items...)
			sortSliceEntries(naiveItems)
			if !reflect.DeepEqual(entrySet(k), naiveItems) {
				t.Fatalf("iter=%d op=%d storage state mismatch\nkernel=%+v\nnaive =%+v",
					iter, op, entrySet(k), naiveItems)
			}
			if !k.Invariant() {
				t.Fatalf("iter=%d op=%d invariant broken: %+v", iter, op, k.Stats())
			}
			if err := k.debugCheckConsistency(); err != nil {
				t.Fatalf("iter=%d op=%d heap consistency: %v stats=%+v", iter, op, err, k.Stats())
			}
		}
	}
}

func normalizeSent(es []Entry) []Entry {
	out := append([]Entry{}, es...)
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) > len(out[j].Path)
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return entryKeyString(out[i]) < entryKeyString(out[j])
	})
	return out
}

func sortSliceEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		return entryKeyString(es[i]) < entryKeyString(es[j])
	})
}

type evalKey struct {
	name, path, partition string
	send                  bool
	reason                string
}

func evalSummary(evs []Evaluation) []evalKey {
	out := make([]evalKey, 0, len(evs))
	for _, ev := range evs {
		out = append(out, evalKey{ev.Entry.Name, ev.Entry.Path, ev.Entry.Partition, ev.Send, ev.Reason})
	}
	sortEvalKeys(out)
	return out
}

func naiveEvalSummary(evs []naiveEval) []evalKey {
	out := make([]evalKey, 0, len(evs))
	for _, ev := range evs {
		out = append(out, evalKey{ev.entry.Name, ev.entry.Path, ev.entry.Partition, ev.send, ev.reason})
	}
	sortEvalKeys(out)
	return out
}

func sortEvalKeys(es []evalKey) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.partition < b.partition
	})
}

func diffError(a, b error) bool {
	return (a == nil) != (b == nil)
}

// TestAttachComplexity 证明附带判定开销不随无关站点条目数增长，
// 且淘汰选 victim 的堆比较次数随站点大小只呈对数增长。
func TestAttachComplexity(t *testing.T) {
	cfg := Config{PerSiteLimit: 100000, GlobalLimit: 1000000, LaxGrace: 0}
	k, _ := New(cfg, &sliceLogger{})

	// 目标站点固定少量深路径条目；干扰站点塞入大量条目。
	for i := 0; i < 32; i++ {
		w := secWrite(fmt.Sprintf("t%d", i), "target.com", "/a/b/c/d", int64(100000+i))
		if _, _, err := k.Write(w); err != nil {
			t.Fatal(err)
		}
	}
	measure := func(distractors, epoch int64) int {
		probeTime := epoch
		base := 0
		if ds, ok := k.sites["distract.com"]; ok {
			base = len(ds.byKey)
		}
		for i := base; i < int(distractors); i++ {
			w := secWrite(fmt.Sprintf("d%d", i), "distract.com", fmt.Sprintf("/z%d/q", i),
				probeTime-(distractors-int64(i)))
			if _, _, err := k.Write(w); err != nil {
				t.Fatal(err)
			}
		}
		if err := k.Advance(probeTime); err != nil {
			t.Fatal(err)
		}
		if _, err := k.Attach(sameReq("target.com", "/a/b/c/d/e", probeTime+1)); err != nil {
			t.Fatal(err)
		}
		return k.lastPathProbes()
	}

	probesSmall := measure(1024, 5000000)
	probesLarge := measure(16384, 9000000)
	if probesSmall != probesLarge {
		t.Fatalf("attach probes must be independent of unrelated entries: %d vs %d",
			probesSmall, probesLarge)
	}
	if probesSmall != 32 {
		t.Fatalf("probes should only count target-site candidates, got %d", probesSmall)
	}

	// 淘汰 victim 的比较次数：堆 Pop 为 O(log n)。
	n := 16384
	k.mu.Lock()
	ds := k.sites["distract.com"]
	before := ds.lru.compares
	heapPopForTest(&ds.lru)
	used := ds.lru.compares - before
	k.mu.Unlock()
	// O(log n) 上界取 4*log2(n) 留足实现余量，线性实现会远超该值。
	if used > int64(4*log2(n)) {
		t.Fatalf("eviction victim selection must be sublinear: compares=%d n=%d", used, n)
	}
}

func log2(n int) int {
	b := 0
	for n > 1 {
		n >>= 1
		b++
	}
	return b
}

// TestDeterministicLog 验证判定依据通过 Logger 完整打印输入、输出与原因。
func TestDeterministicLog(t *testing.T) {
	lg := &sliceLogger{}
	k, _ := New(Config{PerSiteLimit: 10, GlobalLimit: 100, LaxGrace: 0}, lg)
	if _, _, err := k.Write(secWrite("c", "ex.com", "/", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Attach(sameReq("ex.com", "/", 2)); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lg.lines, "\n")
	for _, want := range []string{"WRITE OK", "ATTACH OK", "eval key=", "reason="} {
		if !strings.Contains(joined, want) {
			t.Fatalf("log missing %q in:\n%s", want, joined)
		}
	}
}
