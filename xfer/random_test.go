package xfer

import (
	"fmt"
	"math/rand"
	"testing"
)

// 本文件实现一个按题目规则逐步写成的朴素模拟（model），
// 并与 Service 在 1500 组随机操作序列上逐操作对照。

type mBucket struct {
	owner string
	bytes int64
	open  int64
}

type mOffer struct {
	from     string
	to       string
	deadline int64
}

// model 是朴素模拟：直接用 map 逐步应用规则，不做任何优化。
type model struct {
	maxB    int
	buckets map[string]*mBucket
	counts  map[string]int
	usage   map[string]int64
	limit   map[string]int64
	offers  map[string]mOffer
	lastNow int64
	crashed bool
	crashAt int
	logLen  int

	hasPend    bool
	pendBucket string
	pendOffer  mOffer
	pendBytes  int64
	pendStep   int
}

func newModel(maxB int) *model {
	return &model{
		maxB:    maxB,
		buckets: make(map[string]*mBucket),
		counts:  make(map[string]int),
		usage:   make(map[string]int64),
		limit:   make(map[string]int64),
		offers:  make(map[string]mOffer),
		crashAt: -1,
	}
}

func (m *model) frozen(bucket string, now int64) bool {
	o, ok := m.offers[bucket]
	return ok && now < o.deadline
}

func (m *model) clearExpired(bucket string, now int64) {
	if o, ok := m.offers[bucket]; ok && now >= o.deadline {
		delete(m.offers, bucket)
	}
}

func (m *model) createBucket(t, bucket string, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if t == "" || bucket == "" || now < 0 || now > maxNow {
		return ErrBadParam, "参数非法: 空名或 now 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	if _, ok := m.buckets[bucket]; ok {
		return ErrBucketExists, "桶已存在"
	}
	if m.counts[t] >= m.maxB {
		return ErrTooManyBuckets, fmt.Sprintf("桶数超限: %s 已有 %d 个", t, m.counts[t])
	}
	m.lastNow = now
	m.buckets[bucket] = &mBucket{owner: t}
	m.counts[t]++
	return nil, "建桶成功"
}

func (m *model) setLimit(t string, q int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if t == "" || q < 0 || q > maxQuota {
		return ErrBadParam, "参数非法: 空租户或 q 越界"
	}
	m.limit[t] = q
	return nil, "额度已设置"
}

func (m *model) put(by, bucket string, size, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if by == "" || bucket == "" || now < 0 || now > maxNow || size < 1 || size > maxSize {
		return ErrBadParam, "参数非法: 空名/now/size 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	if b.owner != by {
		return ErrNotOwner, "非属主"
	}
	if m.frozen(bucket, now) {
		return ErrFrozen, "桶被冻结"
	}
	if m.usage[by]+size > m.limit[by] {
		return ErrQuota, fmt.Sprintf("额度不足: %d+%d>%d", m.usage[by], size, m.limit[by])
	}
	m.lastNow = now
	m.clearExpired(bucket, now)
	b.bytes += size
	m.usage[by] += size
	return nil, "写入成功"
}

func (m *model) del(by, bucket string, size, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if by == "" || bucket == "" || now < 0 || now > maxNow || size < 1 || size > maxSize {
		return ErrBadParam, "参数非法: 空名/now/size 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	if b.owner != by {
		return ErrNotOwner, "非属主"
	}
	if m.frozen(bucket, now) {
		return ErrFrozen, "桶被冻结"
	}
	if size > b.bytes {
		return ErrBadParam, fmt.Sprintf("参数非法: size=%d 超过桶字节数 %d", size, b.bytes)
	}
	m.lastNow = now
	m.clearExpired(bucket, now)
	b.bytes -= size
	m.usage[by] -= size
	return nil, "删除成功"
}

func (m *model) beginUpload(by, bucket string, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if by == "" || bucket == "" || now < 0 || now > maxNow {
		return ErrBadParam, "参数非法: 空名或 now 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	if b.owner != by {
		return ErrNotOwner, "非属主"
	}
	if m.frozen(bucket, now) {
		return ErrFrozen, "桶被冻结"
	}
	m.lastNow = now
	m.clearExpired(bucket, now)
	b.open++
	return nil, "开启上传"
}

func (m *model) endUpload(by, bucket string, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if by == "" || bucket == "" || now < 0 || now > maxNow {
		return ErrBadParam, "参数非法: 空名或 now 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	if b.open == 0 {
		return ErrBadParam, "参数非法: 开启上传数为 0"
	}
	m.lastNow = now
	m.clearExpired(bucket, now)
	b.open--
	return nil, "结束上传"
}

func (m *model) offer(bucket, from, to string, ttl, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if bucket == "" || from == "" || to == "" || now < 0 || now > maxNow || ttl < 1 || ttl > maxTTL || to == from {
		return ErrBadParam, "参数非法: 空名/now/ttl 越界或 to==from"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	if b.owner != from {
		return ErrNotOwner, "非属主"
	}
	if o, ok := m.offers[bucket]; ok && now < o.deadline {
		return ErrHasOffer, "已有要约"
	}
	m.lastNow = now
	m.offers[bucket] = mOffer{from: from, to: to, deadline: now + ttl}
	return nil, fmt.Sprintf("要约已登记 截止=%d", now+ttl)
}

func (m *model) cancel(bucket, by string, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if bucket == "" || by == "" || now < 0 || now > maxNow {
		return ErrBadParam, "参数非法: 空名或 now 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	if _, ok := m.buckets[bucket]; !ok {
		return ErrNoBucket, "桶不存在"
	}
	o, ok := m.offers[bucket]
	if !ok {
		return ErrNoOffer, "无要约"
	}
	if now >= o.deadline {
		return ErrExpired, "已到期"
	}
	if by != o.from && by != o.to {
		return ErrNotParty, "非当事人"
	}
	m.lastNow = now
	delete(m.offers, bucket)
	return nil, "要约已撤销"
}

func (m *model) accept(bucket, by string, now int64) (error, string) {
	if m.crashed {
		return ErrNeedRecovery, "崩溃未恢复"
	}
	if bucket == "" || by == "" || now < 0 || now > maxNow {
		return ErrBadParam, "参数非法: 空名或 now 越界"
	}
	if now < m.lastNow {
		return ErrClock, fmt.Sprintf("时钟回退: now=%d < last=%d", now, m.lastNow)
	}
	b, ok := m.buckets[bucket]
	if !ok {
		return ErrNoBucket, "桶不存在"
	}
	o, ok := m.offers[bucket]
	if !ok {
		return ErrNoOffer, "无要约"
	}
	if now >= o.deadline {
		return ErrExpired, "已到期"
	}
	if by != o.to {
		return ErrNotParty, "非当事人"
	}
	if b.open > 0 {
		return ErrInFlight, "有在途上传"
	}
	if m.counts[o.to] >= m.maxB {
		return ErrTooManyBuckets, "桶数超限"
	}
	if m.usage[o.to]+b.bytes > m.limit[o.to] {
		return ErrQuota, fmt.Sprintf("额度不足: %d+%d>%d", m.usage[o.to], b.bytes, m.limit[o.to])
	}
	m.lastNow = now
	// 通过校验即推进时钟，随后按固定步骤执行。
	if m.crashAt == 0 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 0 步（未做任何事）"
	}
	m.hasPend = true
	m.pendBucket = bucket
	m.pendOffer = o
	m.pendBytes = b.bytes
	m.pendStep = 1
	m.logLen++ // BEGIN
	if m.crashAt == 1 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 1 步（BEGIN 已写）"
	}
	m.usage[o.from] -= b.bytes
	m.pendStep = 2
	m.logLen++
	if m.crashAt == 2 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 2 步（from 已减）"
	}
	m.usage[o.to] += b.bytes
	m.pendStep = 3
	m.logLen++
	if m.crashAt == 3 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 3 步（to 已加）"
	}
	m.counts[b.owner]--
	b.owner = o.to
	m.counts[o.to]++
	delete(m.offers, bucket)
	m.pendStep = 4
	m.logLen++
	if m.crashAt == 4 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 4 步（属主已改）"
	}
	m.hasPend = false
	m.logLen++ // END
	if m.crashAt == 5 {
		m.crashed, m.crashAt = true, -1
		return ErrNeedRecovery, "崩溃于第 5 步（END 已写）"
	}
	return nil, "划转成功"
}

func (m *model) injectCrash(k int) {
	m.crashAt = k
}

func (m *model) recover() {
	if m.hasPend {
		o := m.pendOffer
		if m.pendStep < 2 {
			m.usage[o.from] -= m.pendBytes
			m.logLen++
		}
		if m.pendStep < 3 {
			m.usage[o.to] += m.pendBytes
			m.logLen++
		}
		if m.pendStep < 4 {
			b := m.buckets[m.pendBucket]
			m.counts[b.owner]--
			b.owner = o.to
			m.counts[o.to]++
			delete(m.offers, m.pendBucket)
			m.logLen++
		}
		m.logLen++ // END
		m.hasPend = false
	}
	m.crashed = false
}

// checkAgainstModel 逐字段对照 Service 与朴素模拟的状态。
func checkAgainstModel(t *testing.T, s *Service, m *model, tenants, buckets []string, ctx string) {
	t.Helper()
	for _, tn := range tenants {
		if got, want := s.Usage(tn), m.usage[tn]; got != want {
			t.Fatalf("%s: Usage(%s)=%d, model=%d", ctx, tn, got, want)
		}
		if got, want := s.Limit(tn), m.limit[tn]; got != want {
			t.Fatalf("%s: Limit(%s)=%d, model=%d", ctx, tn, got, want)
		}
		if got, want := s.BucketCount(tn), m.counts[tn]; got != want {
			t.Fatalf("%s: BucketCount(%s)=%d, model=%d", ctx, tn, got, want)
		}
	}
	for _, b := range buckets {
		owner, ok := s.Owner(b)
		mb, mok := m.buckets[b]
		if ok != mok {
			t.Fatalf("%s: 桶 %s 存在性=%v, model=%v", ctx, b, ok, mok)
		}
		if ok {
			if owner != mb.owner {
				t.Fatalf("%s: Owner(%s)=%s, model=%s", ctx, b, owner, mb.owner)
			}
			if got, _ := s.BucketBytes(b); got != mb.bytes {
				t.Fatalf("%s: BucketBytes(%s)=%d, model=%d", ctx, b, got, mb.bytes)
			}
			if got, _ := s.OpenUploads(b); got != mb.open {
				t.Fatalf("%s: OpenUploads(%s)=%d, model=%d", ctx, b, got, mb.open)
			}
		}
		so, sok := s.OfferOf(b)
		mo, mook := m.offers[b]
		if sok != mook {
			t.Fatalf("%s: 桶 %s 要约存在性=%v, model=%v", ctx, b, sok, mook)
		}
		if sok && (so.From != mo.from || so.To != mo.to || so.Deadline != mo.deadline) {
			t.Fatalf("%s: 桶 %s 要约=%+v, model=%+v", ctx, b, so, mo)
		}
	}
	if s.crashed != m.crashed {
		t.Fatalf("%s: crashed=%v, model=%v", ctx, s.crashed, m.crashed)
	}
	if got := len(s.Log()); got != m.logLen {
		t.Fatalf("%s: 日志长度=%d, model=%d", ctx, got, m.logLen)
	}
}

// TestRandomSimulation 1500 组随机操作序列与朴素模拟逐操作对照。
func TestRandomSimulation(t *testing.T) {
	const sequences = 1500
	tenants := []string{"A", "B", "C"}
	bucketNames := []string{"b1", "b2", "b3", "b4"}

	pickTenant := func(rng *rand.Rand) string {
		if rng.Intn(100) < 2 {
			return "" // 偶发非法参数
		}
		return tenants[rng.Intn(len(tenants))]
	}
	pickBucket := func(rng *rand.Rand) string {
		if rng.Intn(100) < 2 {
			return ""
		}
		return bucketNames[rng.Intn(len(bucketNames))]
	}
	pickSize := func(rng *rand.Rand) int64 {
		switch rng.Intn(100) {
		case 0:
			return 0
		case 1:
			return maxSize + 1
		default:
			return 1 + rng.Int63n(50)
		}
	}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		maxB := 1 + rng.Intn(3)
		s := New(maxB)
		m := newModel(maxB)
		now := int64(0)
		ops := 30 + rng.Intn(30)
		for i := 0; i < ops; i++ {
			// 推进时钟：多数前进，偶尔不动或回退，偶发越界。
			switch r := rng.Intn(100); {
			case r < 3:
				now = -1
			case r < 5:
				now = maxNow + 1
			case r < 12:
				now -= int64(rng.Intn(3))
				if now < 0 {
					now = 0
				}
			case r < 20:
				// 不动
			default:
				now += int64(rng.Intn(4))
			}
			tn, tn2, bk := pickTenant(rng), pickTenant(rng), pickBucket(rng)
			size := pickSize(rng)

			var got error
			var want error
			var reason string
			var input string
			choice := rng.Intn(24)
			if m.crashed && choice != 23 && rng.Intn(100) < 50 {
				choice = 23 // 崩溃后提高 Recover 概率
			}
			switch choice {
			case 0, 1:
				input = fmt.Sprintf("CreateBucket(%q,%q,%d)", tn, bk, now)
				got = s.CreateBucket(tn, bk, now)
				want, reason = m.createBucket(tn, bk, now)
			case 2, 3:
				q := int64(rng.Intn(4)) * 25
				if rng.Intn(100) < 2 {
					q = maxQuota + 1
				}
				input = fmt.Sprintf("SetLimit(%q,%d)", tn, q)
				got = s.SetLimit(tn, q)
				want, reason = m.setLimit(tn, q)
			case 4, 5, 6, 7, 8:
				input = fmt.Sprintf("Put(%q,%q,%d,%d)", tn, bk, size, now)
				got = s.Put(tn, bk, size, now)
				want, reason = m.put(tn, bk, size, now)
			case 9, 10:
				input = fmt.Sprintf("Delete(%q,%q,%d,%d)", tn, bk, size, now)
				got = s.Delete(tn, bk, size, now)
				want, reason = m.del(tn, bk, size, now)
			case 11, 12:
				input = fmt.Sprintf("BeginUpload(%q,%q,%d)", tn, bk, now)
				got = s.BeginUpload(tn, bk, now)
				want, reason = m.beginUpload(tn, bk, now)
			case 13, 14:
				input = fmt.Sprintf("EndUpload(%q,%q,%d)", tn, bk, now)
				got = s.EndUpload(tn, bk, now)
				want, reason = m.endUpload(tn, bk, now)
			case 15, 16, 17:
				ttl := int64(1 + rng.Intn(20))
				if rng.Intn(100) < 3 {
					ttl = 0
				}
				input = fmt.Sprintf("Offer(%q,%q,%q,%d,%d)", bk, tn, tn2, ttl, now)
				got = s.Offer(bk, tn, tn2, ttl, now)
				want, reason = m.offer(bk, tn, tn2, ttl, now)
			case 18:
				input = fmt.Sprintf("Cancel(%q,%q,%d)", bk, tn, now)
				got = s.Cancel(bk, tn, now)
				want, reason = m.cancel(bk, tn, now)
			case 19, 20, 21:
				input = fmt.Sprintf("Accept(%q,%q,%d)", bk, tn, now)
				got = s.Accept(bk, tn, now)
				want, reason = m.accept(bk, tn, now)
			case 22:
				k := rng.Intn(6)
				input = fmt.Sprintf("InjectCrashAfterStep(%d)", k)
				s.InjectCrashAfterStep(k)
				m.injectCrash(k)
				reason = "注入崩溃点"
			case 23:
				input = "Recover()"
				s.Recover()
				m.recover()
				reason = "崩溃恢复"
			}
			t.Logf("seq=%d op=%d 输入=%s 输出=%v 期望=%v 判定依据=%s", seq, i, input, got, want, reason)
			if got != want {
				t.Fatalf("seq=%d op=%d 输入=%s: got %v, model %v (%s)", seq, i, input, got, want, reason)
			}
			ctx := fmt.Sprintf("seq=%d op=%d 输入=%s", seq, i, input)
			checkAgainstModel(t, s, m, tenants, bucketNames, ctx)
		}
		checkInvariants(t, s, tenants, bucketNames)
	}
}
