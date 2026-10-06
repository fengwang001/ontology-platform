package ecscache

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// 本文件包含一个独立的朴素模型：用扁平切片保存全部条目、每次查询线性
// 扫描（O(总条目数)），语义直接翻译自需求。随机测试将真实实现与该模型
// 在 1000+ 组随机序列上逐步对照，并打印每步输入、输出与判定依据。

// ---------- 朴素模型 ----------

type modelEntry struct {
	name      string
	typ       uint16
	family    int
	prefix    []byte
	scope     int
	result    Result
	expiresAt time.Time
	seq       int
}

type naiveModel struct {
	k             int
	entries       []modelEntry
	seq           int
	upstreamCalls int
}

// lookup 线性扫描全部条目，返回命中结果与判定依据。
func (m *naiveModel) lookup(name string, typ uint16, fam int, addr []byte, now time.Time) (Result, bool, string) {
	best := -1
	var expired, otherFamily, notCovering int
	for i := range m.entries {
		e := &m.entries[i]
		if e.name != name || e.typ != typ {
			continue
		}
		if e.family != fam {
			otherFamily++
			continue
		}
		if !now.Before(e.expiresAt) {
			expired++
			continue
		}
		if !prefixEqual(e.prefix, addr, e.scope) {
			notCovering++
			continue
		}
		if best == -1 || e.scope > m.entries[best].scope {
			best = i
		}
	}
	if best >= 0 {
		e := &m.entries[best]
		return e.result, true, fmt.Sprintf("hit: scope=%d prefix=%v expires_in=%s",
			e.scope, e.prefix, e.expiresAt.Sub(now))
	}
	return Result{}, false, fmt.Sprintf("miss: expired=%d other_family=%d not_covering=%d",
		expired, otherFamily, notCovering)
}

func (m *naiveModel) store(name string, typ uint16, fam int, addr []byte, scope int, res Result, ttl int, now time.Time) {
	// 先清除已到期条目（左闭右开）。
	kept := m.entries[:0]
	for _, e := range m.entries {
		if now.Before(e.expiresAt) {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	// 同键覆盖。
	masked := maskAddr(addr, scope)
	m.seq++
	ne := modelEntry{
		name: name, typ: typ, family: fam, prefix: masked, scope: scope,
		result: res, expiresAt: now.Add(time.Duration(ttl) * time.Second), seq: m.seq,
	}
	replaced := false
	for i := range m.entries {
		e := &m.entries[i]
		if e.name == name && e.typ == typ && e.family == fam && e.scope == scope &&
			reflect.DeepEqual(e.prefix, masked) {
			m.entries[i] = ne
			replaced = true
			break
		}
	}
	if !replaced {
		m.entries = append(m.entries, ne)
	}
	// 容量：同名字同类型条目数不超过 k。
	for {
		count := 0
		for i := range m.entries {
			if m.entries[i].name == name && m.entries[i].typ == typ {
				count++
			}
		}
		if count <= m.k {
			break
		}
		// 淘汰：剩余有效期最短 → 范围更长 → 写入更早。
		victim := -1
		for i := range m.entries {
			e := &m.entries[i]
			if e.name != name || e.typ != typ {
				continue
			}
			if victim == -1 {
				victim = i
				continue
			}
			v := &m.entries[victim]
			switch {
			case !e.expiresAt.Equal(v.expiresAt):
				if e.expiresAt.Before(v.expiresAt) {
					victim = i
				}
			case e.scope != v.scope:
				if e.scope > v.scope {
					victim = i
				}
			case e.seq < v.seq:
				victim = i
			}
		}
		m.entries = append(m.entries[:victim], m.entries[victim+1:]...)
	}
}

// resolve 为模型的完整查询流程，返回结果、错误与判定依据。
func (m *naiveModel) resolve(q Query, now time.Time, up ResolverFunc) (Result, error, string) {
	fam, err := validateQuery(q)
	if err != nil {
		return Result{}, err, "rejected: invalid argument"
	}
	name := normalizeName(q.Name)
	if res, ok, why := m.lookup(name, q.Type, fam, q.ClientAddress, now); ok {
		return res, nil, why
	} else if why != "" {
		// 未命中：向上游查询。
		m.upstreamCalls++
		resp, uerr := up(context.Background(), Request{
			Name: name, Type: q.Type,
			ClientAddress:   append([]byte(nil), q.ClientAddress...),
			SourcePrefixLen: q.SourcePrefixLen,
		})
		if uerr != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrUpstream, uerr), why + " -> upstream error"
		}
		if !validResponse(resp, fam) {
			return Result{}, fmt.Errorf("%w: invalid response", ErrUpstream), why + " -> invalid upstream response"
		}
		if resp.TTL > 0 {
			m.store(name, q.Type, fam, q.ClientAddress, min(resp.Scope, q.SourcePrefixLen), resp.Result, resp.TTL, now)
		}
		return resp.Result, nil, fmt.Sprintf("%s -> upstream ttl=%d scope=%d", why, resp.TTL, resp.Scope)
	}
	panic("unreachable")
}

// ---------- 确定性随机上游 ----------

// makeDeterministicUpstream 返回一个只由 (种子, 请求) 决定应答的上游，
// 供真实实现与模型在相同输入下得到相同应答。
func makeDeterministicUpstream(seed int64) ResolverFunc {
	return func(_ context.Context, req Request) (Response, error) {
		h := fnv.New64a()
		h.Write([]byte(req.Name))
		h.Write([]byte{byte(req.Type), byte(req.Type >> 8)})
		h.Write(req.ClientAddress)
		h.Write([]byte{byte(req.SourcePrefixLen), byte(seed), byte(seed >> 8)})
		r := rand.New(rand.NewSource(int64(h.Sum64())))

		if r.Intn(20) == 0 { // 5% 上游失败
			return Response{}, errors.New("deterministic upstream error")
		}
		ttl := []int{0, 1, 2, 3, 5, 10, 100, MaxTTLSeconds}[r.Intn(8)]
		maxBits := len(req.ClientAddress) * 8
		var scope int
		switch r.Intn(5) {
		case 0:
			scope = 0
		case 1:
			scope = req.SourcePrefixLen
		case 2:
			scope = req.SourcePrefixLen / 2
		case 3: // 大于源前缀（钳制路径）
			scope = min(req.SourcePrefixLen+1+r.Intn(16), maxBits)
		default:
			scope = r.Intn(maxBits + 1)
		}
		var res Result
		switch x := r.Intn(10); {
		case x < 6:
			res = recs(fmt.Sprintf("rec-%d", r.Intn(1000)))
		case x < 8:
			res = Result{Kind: KindNXDomain}
		default:
			res = Result{Kind: KindNoData}
		}
		return Response{Result: res, TTL: ttl, Scope: scope}, nil
	}
}

// ---------- 随机序列对照 ----------

// TestModelComparison 在 1000+ 组随机序列上对照真实实现与朴素模型，
// 逐步打印输入、输出与判定依据。
func TestModelComparison(t *testing.T) {
	const sequences = 1200
	const opsPerSeq = 25

	namePool := []string{"a.com", "A.com.", "b.com", "B.COM", "c.com.", "d.com"}
	typePool := []uint16{1, 2, 28}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		fc := newFakeClock()
		upstreamFn := makeDeterministicUpstream(int64(seq))
		rec := &recorder{fn: upstreamFn}
		k := 1 + rng.Intn(6)
		real := New(k, fc.now, rec)
		model := &naiveModel{k: k}

		for step := 0; step < opsPerSeq; step++ {
			// 步进时钟：小步长为主，便于命中恰到期边界。
			fc.advance(time.Duration([]int{0, 0, 1, 1, 2, 3, 7}[rng.Intn(7)]) * time.Second)
			now := fc.now()

			q := randomQuery(rng, namePool, typePool)
			gotRes, gotErr := real.Resolve(context.Background(), q)
			wantRes, wantErr, why := model.resolve(q, now, upstreamFn)

			t.Logf("seq=%d step=%d t=%s k=%d q=%+v => res=%+v err=%v | %s",
				seq, step, now.Format("15:04:05"), k, q, gotRes, gotErr, why)

			if !sameErrorClass(gotErr, wantErr) {
				t.Fatalf("seq=%d step=%d q=%+v: error mismatch: real=%v model=%v",
					seq, step, q, gotErr, wantErr)
			}
			if gotErr == nil && !reflect.DeepEqual(gotRes, wantRes) {
				t.Fatalf("seq=%d step=%d q=%+v: result mismatch: real=%+v model=%+v",
					seq, step, q, gotRes, wantRes)
			}
		}
		if rec.n() != model.upstreamCalls {
			t.Fatalf("seq=%d: upstream calls: real=%d model=%d", seq, rec.n(), model.upstreamCalls)
		}
	}
}

func sameErrorClass(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if errors.Is(a, ErrInvalidArgument) || errors.Is(b, ErrInvalidArgument) {
		return errors.Is(a, ErrInvalidArgument) && errors.Is(b, ErrInvalidArgument)
	}
	return errors.Is(a, ErrUpstream) && errors.Is(b, ErrUpstream)
}

// randomQuery 生成随机查询，地址池制造大量共享前缀；偶尔生成非法查询。
func randomQuery(rng *rand.Rand, names []string, types []uint16) Query {
	q := Query{
		Name: names[rng.Intn(len(names))],
		Type: types[rng.Intn(len(types))],
	}
	if rng.Intn(2) == 0 { // IPv4
		q.ClientAddress = []byte{
			[]byte{10, 10, 10, 11, 192}[rng.Intn(5)],
			byte(rng.Intn(4)), byte(rng.Intn(256)), byte(rng.Intn(256)),
		}
		q.SourcePrefixLen = []int{0, 4, 8, 12, 16, 24, 32}[rng.Intn(7)]
	} else { // IPv6
		addr := make([]byte, 16)
		addr[0] = []byte{0x20, 0x20, 0xfe}[rng.Intn(3)]
		addr[1] = byte(rng.Intn(3))
		for i := 2; i < 16; i++ {
			addr[i] = byte(rng.Intn(256))
		}
		q.ClientAddress = addr
		q.SourcePrefixLen = []int{0, 8, 16, 32, 48, 64, 128}[rng.Intn(7)]
	}
	// 5% 概率生成非法查询（拒绝路径不得改变缓存）。
	switch rng.Intn(20) {
	case 0:
		q.SourcePrefixLen = len(q.ClientAddress)*8 + 1 + rng.Intn(8)
	case 1:
		q.ClientAddress = []byte{1, 2, 3}
	}
	return q
}
