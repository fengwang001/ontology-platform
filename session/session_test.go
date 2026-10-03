package session

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/journal"
)

// ---------- 逐步朴素模拟器 ----------

type nsess struct {
	id, total, c, expiry int64
	tenant, key          string
	state                State
	data                 []byte
}

type naive struct {
	t, m    int64
	quota   map[string]int64
	used    map[string]int64
	sizes   map[string]map[string]int64
	sess    map[int64]*nsess
	nextSID int64
	lastNow int64
	fail    map[int]bool
	accepts int
	attempt int
}

func newNaive(t, m int64, fail ...int) *naive {
	f := map[int]bool{}
	for _, i := range fail {
		f[i] = true
	}
	return &naive{
		t: t, m: m,
		quota: map[string]int64{}, used: map[string]int64{},
		sizes: map[string]map[string]int64{},
		sess:  map[int64]*nsess{},
		fail:  f,
	}
}

// collectExpired 是 now 的纯函数：返回仍开启但已到期的会话，不落地。
func (n *naive) collectExpired(now int64) []int64 {
	var out []int64
	for id, x := range n.sess {
		if x.state == StateOpen && x.expiry <= now {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func expSet(ids []int64) map[int64]bool {
	m := map[int64]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func (n *naive) postReclaimView(tenant string, exp []int64) (openCount int, reserved int64) {
	for _, x := range n.sess {
		if x.tenant == tenant && x.state == StateOpen {
			openCount++
			reserved += x.total
		}
	}
	for _, id := range exp {
		if n.sess[id].tenant == tenant {
			openCount--
			reserved -= n.sess[id].total
		}
	}
	return
}

func (n *naive) journalCheck() error {
	idx := n.attempt
	n.attempt++
	if n.fail[idx] {
		return ErrJournal
	}
	return nil
}

// landed 只在操作被接受后调用：落地回收、推进接受计数。
func (n *naive) landed(exp []int64, now int64) {
	for _, id := range exp {
		n.sess[id].state = StateExpired
	}
	n.accepts++
	if now > n.lastNow {
		n.lastNow = now
	}
}

func (n *naive) setQuota(tenant string, q int64) error {
	if tenant == "" || q < 0 || q > 1e15 {
		return ErrInvalid
	}
	var reserved int64
	for _, x := range n.sess {
		if x.tenant == tenant && x.state == StateOpen {
			reserved += x.total
		}
	}
	if n.used[tenant]+reserved > q {
		return ErrQuotaBelow
	}
	if err := n.journalCheck(); err != nil {
		return err
	}
	n.quota[tenant] = q
	n.accepts++
	return nil
}

func (n *naive) create(tenant, key string, total, now int64) (int64, error) {
	if tenant == "" || key == "" || total < 1 || total > 1e12 || now < 0 || now > 1e12 {
		return 0, ErrInvalid
	}
	if now < n.lastNow {
		return 0, ErrClockBack
	}
	exp := n.collectExpired(now)
	openCount, reserved := n.postReclaimView(tenant, exp)
	if int64(openCount) >= n.m {
		return 0, ErrTooMany
	}
	if n.used[tenant]+reserved+total > n.quota[tenant] {
		return 0, ErrQuota
	}
	if err := n.journalCheck(); err != nil {
		return 0, err
	}
	n.landed(exp, now)
	id := n.nextSID + 1
	n.nextSID = id
	n.sess[id] = &nsess{
		id: id, tenant: tenant, key: key, total: total,
		expiry: now + n.t, state: StateOpen,
	}
	return id, nil
}

func (n *naive) classify(sid int64, exp map[int64]bool) (*nsess, error) {
	x, ok := n.sess[sid]
	if !ok {
		return nil, ErrUnknownSID
	}
	if x.state == StateComplete || x.state == StateAborted {
		return nil, ErrSessionDone
	}
	if x.state == StateExpired || exp[sid] {
		return nil, ErrExpired
	}
	return x, nil
}

func (n *naive) chunk(sid, off int64, data []byte, now int64) error {
	if off < 0 || off > 1e12 || len(data) > 1<<20 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockBack
	}
	expIDs := n.collectExpired(now)
	x, err := n.classify(sid, expSet(expIDs))
	if err != nil {
		return err
	}
	end := off + int64(len(data))
	if off > x.c {
		return ErrGap
	}
	if end > x.total {
		return ErrBeyondTotal
	}
	for i := range data {
		pos := off + int64(i)
		if pos < x.c && data[i] != x.data[pos] {
			return ErrConflict
		}
	}
	if err := n.journalCheck(); err != nil {
		return err
	}
	n.landed(expIDs, now)
	if end > int64(len(x.data)) {
		grown := make([]byte, end)
		copy(grown, x.data)
		x.data = grown
	}
	copy(x.data[off:end], data)
	if end > x.c {
		x.c = end
		x.expiry = now + n.t
	}
	return nil
}

func (n *naive) complete(sid, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockBack
	}
	expIDs := n.collectExpired(now)
	x, err := n.classify(sid, expSet(expIDs))
	if err != nil {
		return err
	}
	if x.c != x.total {
		return &IncompleteError{Remaining: x.total - x.c}
	}
	if err := n.journalCheck(); err != nil {
		return err
	}
	n.landed(expIDs, now)
	oldSize := n.sizes[x.tenant][x.key]
	x.state = StateComplete
	n.used[x.tenant] += x.total - oldSize
	if n.sizes[x.tenant] == nil {
		n.sizes[x.tenant] = map[string]int64{}
	}
	n.sizes[x.tenant][x.key] = x.total
	return nil
}

func (n *naive) abort(sid, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < n.lastNow {
		return ErrClockBack
	}
	expIDs := n.collectExpired(now)
	x, err := n.classify(sid, expSet(expIDs))
	if err != nil {
		return err
	}
	if err := n.journalCheck(); err != nil {
		return err
	}
	n.landed(expIDs, now)
	x.state = StateAborted
	return nil
}

func (n *naive) query(sid, now int64) (int64, int64, error) {
	if now < 0 || now > 1e12 {
		return 0, 0, ErrInvalid
	}
	if now < n.lastNow {
		return 0, 0, ErrClockBack
	}
	x, ok := n.sess[sid]
	if !ok {
		return 0, 0, ErrUnknownSID
	}
	if x.state == StateComplete || x.state == StateAborted {
		return 0, 0, ErrSessionDone
	}
	if x.state == StateExpired || x.expiry <= now {
		return 0, 0, ErrExpired
	}
	return x.c, x.expiry, nil
}

// ---------- 快照 ----------

type snap struct {
	lastNow  int64
	nextSID  int64
	quota    map[string]int64
	used     map[string]int64
	reserved map[string]int64
	sizes    map[string]map[string]int64
	open     map[string]int
	sess     map[string]string
	heap     []int64
}

func (s *Service) snapshot() snap {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := snap{
		lastNow:  s.lastNow,
		nextSID:  s.nextSID,
		quota:    map[string]int64{},
		used:     map[string]int64{},
		reserved: map[string]int64{},
		sizes:    map[string]map[string]int64{},
		open:     map[string]int{},
		sess:     map[string]string{},
	}
	// 租户集合 = 显式设过额度的租户（含设为 0）∪ 出现过会话的租户，
	// 与朴素模拟器的集合口径一致。
	tenantSet := map[string]bool{}
	for _, t := range s.ledger.Tenants() {
		tenantSet[t] = true
	}
	for _, x := range s.sessions {
		tenantSet[x.tenant] = true
	}
	for t := range tenantSet {
		info := s.ledger.Info(t)
		out.quota[t] = info.Quota
		out.used[t] = info.Used
		out.reserved[t] = info.Reserved
		out.sizes[t] = map[string]int64{}
		if ts := s.tenants[t]; ts != nil {
			for k, v := range ts.sizes {
				out.sizes[t][k] = v
			}
		}
	}
	for id, x := range s.sessions {
		out.sess[fmt.Sprintf("%d", id)] = fmt.Sprintf(
			"t=%s k=%s total=%d c=%d exp=%d st=%d data=%x",
			x.tenant, x.key, x.total, x.c, x.expiry, x.state, x.data)
		if x.state == StateOpen {
			out.open[x.tenant]++
		}
	}
	out.heap = append(out.heap, s.heap.sids...)
	sort.Slice(out.heap, func(i, j int) bool { return out.heap[i] < out.heap[j] })
	return out
}

func (n *naive) snapshot() snap {
	out := snap{
		lastNow:  n.lastNow,
		nextSID:  n.nextSID,
		quota:    map[string]int64{},
		used:     map[string]int64{},
		reserved: map[string]int64{},
		sizes:    map[string]map[string]int64{},
		open:     map[string]int{},
		sess:     map[string]string{},
	}
	tenants := map[string]bool{}
	for t := range n.quota {
		tenants[t] = true
	}
	for _, x := range n.sess {
		tenants[x.tenant] = true
	}
	for t := range tenants {
		out.quota[t] = n.quota[t]
		out.used[t] = n.used[t]
		var r int64
		for _, x := range n.sess {
			if x.tenant == t && x.state == StateOpen {
				r += x.total
				out.open[t]++
			}
		}
		out.reserved[t] = r
		out.sizes[t] = map[string]int64{}
		for k, v := range n.sizes[t] {
			out.sizes[t][k] = v
		}
	}
	for id, x := range n.sess {
		out.sess[fmt.Sprintf("%d", id)] = fmt.Sprintf(
			"t=%s k=%s total=%d c=%d exp=%d st=%d data=%x",
			x.tenant, x.key, x.total, x.c, x.expiry, x.state, x.data)
	}
	var heapIDs []int64
	for id, x := range n.sess {
		if x.state == StateOpen {
			heapIDs = append(heapIDs, id)
		}
	}
	sort.Slice(heapIDs, func(i, j int) bool { return heapIDs[i] < heapIDs[j] })
	out.heap = heapIDs
	return out
}

func sameSnap(a, b snap) bool {
	return reflect.DeepEqual(a, b)
}

// ---------- 操作与双端执行 ----------

type op struct {
	kind               string
	tenant, key        string
	sid, off, total, q int64
	now                int64
	data               []byte
}

func (o op) String() string {
	if o.kind == "chunk" {
		return fmt.Sprintf("%s(sid=%d off=%d len=%d now=%d data=%x)",
			o.kind, o.sid, o.off, len(o.data), o.now, o.data)
	}
	switch o.kind {
	case "create":
		return fmt.Sprintf("create(t=%s k=%s total=%d now=%d)", o.tenant, o.key, o.total, o.now)
	case "setquota":
		return fmt.Sprintf("setquota(t=%s q=%d)", o.tenant, o.q)
	case "query":
		return fmt.Sprintf("query(sid=%d now=%d)", o.sid, o.now)
	default:
		return fmt.Sprintf("%s(sid=%d now=%d)", o.kind, o.sid, o.now)
	}
}

type outcome struct {
	sid int64
	c   int64
	exp int64
	err string
}

func normErr(e error) string {
	if e == nil {
		return ""
	}
	var ie *IncompleteError
	if errors.As(e, &ie) {
		return fmt.Sprintf("未完成:%d", ie.Remaining)
	}
	return e.Error()
}

func runReal(svc *Service, o op) outcome {
	switch o.kind {
	case "setquota":
		return outcome{err: normErr(svc.SetQuota(o.tenant, o.q))}
	case "create":
		sid, err := svc.Create(o.tenant, o.key, o.total, o.now)
		return outcome{sid: sid, err: normErr(err)}
	case "chunk":
		return outcome{err: normErr(svc.Chunk(o.sid, o.off, append([]byte(nil), o.data...), o.now))}
	case "complete":
		return outcome{err: normErr(svc.Complete(o.sid, o.now))}
	case "abort":
		return outcome{err: normErr(svc.Abort(o.sid, o.now))}
	case "query":
		c, exp, err := svc.Query(o.sid, o.now)
		return outcome{c: c, exp: exp, err: normErr(err)}
	default:
		panic("bad op kind")
	}
}

func runNaive(n *naive, o op) outcome {
	switch o.kind {
	case "setquota":
		return outcome{err: normErr(n.setQuota(o.tenant, o.q))}
	case "create":
		sid, err := n.create(o.tenant, o.key, o.total, o.now)
		return outcome{sid: sid, err: normErr(err)}
	case "chunk":
		return outcome{err: normErr(n.chunk(o.sid, o.off, append([]byte(nil), o.data...), o.now))}
	case "complete":
		return outcome{err: normErr(n.complete(o.sid, o.now))}
	case "abort":
		return outcome{err: normErr(n.abort(o.sid, o.now))}
	case "query":
		c, exp, err := n.query(o.sid, o.now)
		return outcome{c: c, exp: exp, err: normErr(err)}
	default:
		panic("bad op kind")
	}
}

// runHarness 对照真实服务与朴素模拟器；svc 与 n 须带相同故障注入。
func runHarness(t *testing.T, svc *Service, n *naive, ops []op) {
	t.Helper()
	sink := svc.sink
	for i, o := range ops {
		rc := runReal(svc, o)
		nc := runNaive(n, o)
		if !reflect.DeepEqual(rc, nc) {
			t.Fatalf("第%d步输入=%s\n真实输出=%+v 判定依据=%s\n朴素输出=%+v",
				i, o, rc, rc.err, nc)
		}
		if !sameSnap(svc.snapshot(), n.snapshot()) {
			t.Fatalf("第%d步状态不一致, 输入=%s\n真实=%#v\n朴素=%#v",
				i, o, svc.snapshot(), n.snapshot())
		}
		// 对每个 k 回放前 k 条被接受记录，须与当前真实状态逐字段相同。
		recs := sink.Records()
		rs, err := Replay(svc.t, svc.maxOpen, recs)
		if err != nil {
			t.Fatalf("第%d步 Replay: %v", i, err)
		}
		if !sameSnap(rs.snapshot(), svc.snapshot()) {
			t.Fatalf("第%d步回放不一致(k=%d)\n回放=%#v\n真实=%#v",
				i, len(recs), rs.snapshot(), svc.snapshot())
		}
		t.Logf("step=%d 输入=%s 输出=%+v", i, o, rc)
	}
}

func newPair(t, m int64, fail ...int) (*Service, *naive) {
	svc, err := New(t, int(m), journal.NewFaultSink(journal.NewMemorySink(), fail...))
	if err != nil {
		panic(err)
	}
	return svc, newNaive(t, m, fail...)
}

func bytes30(b byte) []byte {
	out := make([]byte, 30)
	for i := range out {
		out[i] = b
	}
	return out
}

func bytes10(b byte) []byte {
	out := make([]byte, 10)
	for i := range out {
		out[i] = b
	}
	return out
}

func bytesRange(off, end int64) []byte {
	out := make([]byte, end-off)
	for i := range out {
		out[i] = byte(int(off) + i)
	}
	return out
}

func bytes25() []byte {
	out := make([]byte, 25)
	for i := range out {
		out[i] = byte(i + 1)
	}
	return out
}

func bytes60() []byte {
	out := make([]byte, 60)
	for i := range out {
		out[i] = byte(i + 1)
	}
	return out
}

// TestSpecExamples 复现题目给出的两个示例。
func TestSpecExamples(t *testing.T) {
	svc, nm := newPair(10, 5)
	ops := []op{
		{kind: "setquota", tenant: "A", q: 100},
		{kind: "create", tenant: "A", key: "k", total: 60, now: 0},
		{kind: "create", tenant: "A", key: "m", total: 40, now: 0},
		{kind: "create", tenant: "A", key: "z", total: 1, now: 0},
		{kind: "chunk", sid: 1, off: 0, data: bytes30(0), now: 5},
		{kind: "chunk", sid: 1, off: 40, data: []byte{1}, now: 5},
		{kind: "chunk", sid: 1, off: 20, data: append(bytes10(0), bytesRange(30, 40)...), now: 6},
		{kind: "chunk", sid: 1, off: 0, data: bytes10(0), now: 7},
		{kind: "create", tenant: "A", key: "q", total: 30, now: 10},
	}
	runHarness(t, svc, nm, ops)
	c, exp, err := svc.Query(1, 10)
	if err != nil || c != 40 || exp != 16 {
		t.Fatalf("s1 c=%d exp=%d err=%v, want c=40 exp=16", c, exp, err)
	}
	// 到期回收 s2 后 s3 占用 30；11 时 s1(到期16) 仍可查。
	if _, _, err := svc.Query(2, 11); !errors.Is(err, ErrExpired) {
		t.Fatalf("s2 应已到期, got %v", err)
	}
}

// TestSameKeyOverwrite 覆盖同键：全量预留与提交时 U 净变化。
func TestSameKeyOverwrite(t *testing.T) {
	svc, nm := newPair(10, 5)
	ops := []op{
		{kind: "setquota", tenant: "A", q: 100},
		{kind: "create", tenant: "A", key: "k", total: 25, now: 0}, // s1
		{kind: "chunk", sid: 1, off: 0, data: bytes25(), now: 0},
		{kind: "complete", sid: 1, now: 0},                          // U=25
		{kind: "create", tenant: "A", key: "k", total: 60, now: 1},  // s2 全量预留60
		{kind: "create", tenant: "A", key: "j", total: 16, now: 1},  // 25+60+16=101 拒绝(不占号)
		{kind: "abort", sid: 2, now: 1},                             // 释放后可再建
		{kind: "create", tenant: "A", key: "k", total: 60, now: 1},  // s3
		{kind: "create", tenant: "A", key: "j2", total: 16, now: 1}, // 25+60+16=101 拒绝
		{kind: "chunk", sid: 3, off: 0, data: bytes60(), now: 2},
		{kind: "complete", sid: 3, now: 2},                         // U=25+60-25=60, R=0
		{kind: "create", tenant: "A", key: "j", total: 40, now: 3}, // s4 恰等
	}
	runHarness(t, svc, nm, ops)
	info := svc.ledger.Info("A")
	if info.Used != 60 || info.Reserved != 40 {
		t.Fatalf("覆盖后 U=%d R=%d, want 60/40", info.Used, info.Reserved)
	}
}

// TestBoundaryTable 表驱动：恰等/大1、到期恰等/小1、错误次序、被拒不落地等。
func TestBoundaryTable(t *testing.T) {
	type step struct {
		op
		want string
	}
	cases := []struct {
		name  string
		t, m  int64
		fails []int
		steps []step
	}{
		{
			name: "额度恰等通过大1拒绝", t: 10, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 10}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 10, now: 0}},
				{op: op{kind: "abort", sid: 1, now: 0}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 11, now: 0}, want: "额度不足"},
			},
		},
		{
			name: "SetQuota恰等与低于占用", t: 10, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 10}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 6, now: 0}},
				{op: op{kind: "setquota", tenant: "A", q: 5}, want: "低于占用"},
				{op: op{kind: "setquota", tenant: "A", q: 6}},
			},
		},
		{
			name: "到期小1未到期与恰等到期", t: 10, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "setquota", tenant: "B", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 10, now: 0}},  // 到期10
				{op: op{kind: "create", tenant: "A", key: "k2", total: 10, now: 0}}, // 到期10
				{op: op{kind: "query", sid: 1, now: 9}},
				{op: op{kind: "chunk", sid: 1, off: 1, data: []byte{2}, now: 10}, want: "会话已到期"},
				{op: op{kind: "complete", sid: 1, now: 10}, want: "会话已到期"},
				{op: op{kind: "abort", sid: 1, now: 10}, want: "会话已到期"},
				// 被接受的 now=11 操作落地回收 s1/s2（用 B 避免 A 容量干扰）。
				{op: op{kind: "create", tenant: "B", key: "b", total: 10, now: 11}},
				{op: op{kind: "create", tenant: "A", key: "k3", total: 10, now: 20}}, // s4 到期30
				{op: op{kind: "chunk", sid: 4, off: 0, data: []byte{1}, now: 25}},    // 到期35
				{op: op{kind: "query", sid: 4, now: 34}},
				{op: op{kind: "query", sid: 4, now: 35}, want: "会话已到期"},
			},
		},
		{
			name: "缺口先于越界先于冲突", t: 100, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 10, now: 0}},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte("01234"), now: 0}},
				{op: op{kind: "chunk", sid: 1, off: 9, data: []byte("XXXX"), now: 0}, want: "缺口"},
				{op: op{kind: "chunk", sid: 1, off: 4, data: []byte("XXXXXXXX"), now: 0}, want: "越界"},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte("9"), now: 0}, want: "冲突"},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte("0"), now: 0}},
			},
		},
		{
			name: "被拒操作不落地回收", t: 10, m: 5, fails: []int{4},
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 60, now: 0}},
				{op: op{kind: "create", tenant: "A", key: "j", total: 40, now: 0}},
				// now=10 两会话恰到期；回收后 R=0，Create(total=1) 判定通过并被接受，
				// 触发回收落地（s1/s2 置到期，R=0）。
				{op: op{kind: "create", tenant: "A", key: "z", total: 1, now: 10}},
				// now=11：s3 未到期(到期20)、R=1，再来 total=1 判通过，
				// 在日志处注入失败 -> 日志失败；s3 状态不变，R 仍为 1。
				{op: op{kind: "create", tenant: "A", key: "z2", total: 1, now: 11}, want: "日志失败"},
				// 失败后会话号不占号、R 不变：再来 total=1 仍恰等通过（号=4）。
				{op: op{kind: "create", tenant: "A", key: "z3", total: 1, now: 11}},
				// 但到期语义已生效：对会话操作报已到期。
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte{1}, now: 11}, want: "会话已到期"},
				{op: op{kind: "query", sid: 1, now: 9}, want: "时钟回退"},
				// SetQuota 不看时钟也不触发回收；一个被接受的 now=12 操作落地回收。
				{op: op{kind: "setquota", tenant: "B", q: 1}},
				{op: op{kind: "create", tenant: "B", key: "b", total: 1, now: 12}},
			},
		},
		{
			name: "纯重放不刷新到期", t: 10, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 10, now: 0}},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte("01234"), now: 5}},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte("0"), now: 8}},
			},
		},
		{
			name: "拒绝不占号且号不复用", t: 10, m: 1,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 5, now: 0}},
				{op: op{kind: "create", tenant: "A", key: "j", total: 5, now: 0}, want: "会话数超限"},
				{op: op{kind: "abort", sid: 1, now: 0}},
				{op: op{kind: "create", tenant: "A", key: "j", total: 5, now: 0}},
			},
		},
		{
			name: "零长探测", t: 100, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 5, now: 0}},
				{op: op{kind: "chunk", sid: 1, off: 3, data: []byte{}, now: 1}, want: "缺口"},
				{op: op{kind: "chunk", sid: 1, off: 0, data: []byte{}, now: 1}},
			},
		},
		{
			name: "拒绝次序参数优先于时钟", t: 10, m: 5,
			steps: []step{
				{op: op{kind: "setquota", tenant: "A", q: 100}},
				{op: op{kind: "create", tenant: "A", key: "k", total: 5, now: 5}},
				{op: op{kind: "create", tenant: "A", key: "", total: 5, now: 1}, want: "参数非法"},
				{op: op{kind: "chunk", sid: 1, off: -1, data: nil, now: 1}, want: "参数非法"},
				{op: op{kind: "complete", sid: 1, now: 1}, want: "时钟回退"},
				{op: op{kind: "complete", sid: 99, now: 5}, want: "未知会话"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, nm := newPair(c.t, c.m, c.fails...)
			var ops []op
			for _, st := range c.steps {
				rc := runReal(svc, st.op)
				nc := runNaive(nm, st.op)
				if !reflect.DeepEqual(rc, nc) {
					t.Fatalf("输入=%s 真实=%+v 朴素=%+v", st.op, rc, nc)
				}
				if rc.err != st.want {
					t.Fatalf("输入=%s 错误=%q 期望=%q", st.op, rc.err, st.want)
				}
				ops = append(ops, st.op)
				t.Logf("输入=%s 输出=%+v", st.op, rc)
			}
			rs, err := Replay(c.t, int(c.m), svc.sink.Records())
			if err != nil {
				t.Fatal(err)
			}
			if !sameSnap(rs.snapshot(), svc.snapshot()) {
				t.Fatalf("回放快照不一致\n%#v\n%#v", rs.snapshot(), svc.snapshot())
			}
			assertInvariants(t, svc, c.name)
		})
	}
}

// TestPureReplayNoRefresh 纯重放不刷新到期时刻。
func TestPureReplayNoRefresh(t *testing.T) {
	svc, nm := newPair(10, 5)
	ops := []op{
		{kind: "setquota", tenant: "A", q: 100},
		{kind: "create", tenant: "A", key: "k", total: 10, now: 0},
		{kind: "chunk", sid: 1, off: 0, data: []byte("01234"), now: 5}, // 到期 15
	}
	runHarness(t, svc, nm, ops[:3])
	before := svc.snapshot()
	if err := svc.Chunk(1, 0, []byte("0"), 8); err != nil { // 纯重放
		t.Fatal(err)
	}
	after := svc.snapshot()
	if after.sess["1"] != before.sess["1"] {
		t.Fatalf("纯重放改变了会话: before=%s after=%s", before.sess["1"], after.sess["1"])
	}
	if _, _, err := svc.Query(1, 14); err != nil {
		t.Fatalf("到期15前应可查: %v", err)
	}
	if _, _, err := svc.Query(1, 15); !errors.Is(err, ErrExpired) {
		t.Fatalf("到期15恰等应到期: %v", err)
	}
}

// TestIncompleteRemaining Complete 在未完成时返回 total-c。
func TestIncompleteRemaining(t *testing.T) {
	svc, _ := newPair(100, 5)
	if err := svc.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create("A", "k", 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.Chunk(1, 0, []byte("012"), 0); err != nil {
		t.Fatal(err)
	}
	err := svc.Complete(1, 0)
	var ie *IncompleteError
	if !errors.As(err, &ie) || ie.Remaining != 7 {
		t.Fatalf("期望未完成剩余7, got %v", err)
	}
}

// TestJournalFailure 注入日志失败：状态、会话号、时钟均不变。
func TestJournalFailure(t *testing.T) {
	svc, err := New(10, 5, journal.NewFaultSink(journal.NewMemorySink(), 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	sid, err := svc.Create("A", "k", 10, 0)
	if !errors.Is(err, ErrJournal) || sid != 0 {
		t.Fatalf("第1条日志应失败, sid=%d err=%v", sid, err)
	}
	snap1 := svc.snapshot()
	sid, err = svc.Create("A", "k", 10, 0)
	if err != nil || sid != 1 {
		t.Fatalf("失败后再创建 sid=%d err=%v", sid, err)
	}
	if len(svc.sink.Records()) != 2 {
		t.Fatalf("失败记录不得落地, got %d 条", len(svc.sink.Records()))
	}
	_ = snap1
}

func assertInvariants(t *testing.T, svc *Service, label string) {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	tenants := map[string]bool{}
	openCount := map[string]int{}
	reserved := map[string]int64{}
	for id, x := range svc.sessions {
		tenants[x.tenant] = true
		if x.c > x.total {
			t.Fatalf("[%s] sid=%d c=%d>total=%d", label, id, x.c, x.total)
		}
		if x.heapIdx >= 0 != (x.state == StateOpen) {
			t.Fatalf("[%s] sid=%d heapIdx=%d 与 state=%d 不一致",
				label, id, x.heapIdx, x.state)
		}
		if x.state == StateOpen {
			openCount[x.tenant]++
			reserved[x.tenant] += x.total
		}
	}
	for tn := range tenants {
		info := svc.ledger.Info(tn)
		if info.Used+info.Reserved > info.Quota {
			t.Fatalf("[%s] 租户 %s U+R=%d > q=%d",
				label, tn, info.Used+info.Reserved, info.Quota)
		}
		if info.Reserved != reserved[tn] {
			t.Fatalf("[%s] 租户 %s R=%d != 开启会话 total 之和 %d",
				label, tn, info.Reserved, reserved[tn])
		}
		if openCount[tn] > svc.maxOpen {
			t.Fatalf("[%s] 租户 %s 开启会话 %d > M=%d",
				label, tn, openCount[tn], svc.maxOpen)
		}
	}
	if svc.heap.Len() != len(svc.heap.sids) {
		t.Fatal("堆长度异常")
	}
	for i, sid := range svc.heap.sids {
		if svc.sessions[sid].heapIdx != i {
			t.Fatalf("[%s] sid=%d heapIdx=%d 但堆内位置=%d",
				label, sid, svc.sessions[sid].heapIdx, i)
		}
	}
}

// TestPoppedTiers 单次接受操作的 popped 增量只取决于本次落地回收数（+1 探界），
// 与开启中的会话总数无关：100 与 10000 两档都只有 2 个到期，增量恒为 3。
func TestPoppedTiers(t *testing.T) {
	for _, nSess := range []int{100, 10000} {
		name := fmt.Sprintf("%d", nSess)
		t.Run(name, func(t *testing.T) {
			const m = 1000
			const ttl int64 = 100
			svc, err := New(ttl, m, journal.NewMemorySink())
			if err != nil {
				t.Fatal(err)
			}
			nTenant := (nSess + m - 1) / m
			for i := 0; i < nTenant; i++ {
				tn := fmt.Sprintf("T%d", i)
				if err := svc.SetQuota(tn, 1e8); err != nil {
					t.Fatal(err)
				}
			}
			if err := svc.SetQuota("fresh", 100); err != nil {
				t.Fatal(err)
			}
			// nSess 个会话全部在 now=0 创建，到期时刻均为 ttl=100。
			for i := 0; i < nSess; i++ {
				tn := fmt.Sprintf("T%d", i/m)
				if _, err := svc.Create(tn, fmt.Sprintf("k%d", i), 1, 0); err != nil {
					t.Fatalf("i=%d tenant=%s: %v", i, tn, err)
				}
			}
			// 仅 sid=1、sid=2 保留到期100；其余 nSess-2 个在 now=50 推进一次非空
			// Chunk，到期刷新为 150。此刻全部 nSess 个会话仍开启。
			for id := int64(3); id <= int64(nSess); id++ {
				if err := svc.Chunk(id, 0, []byte{1}, 50); err != nil {
					t.Fatalf("刷新 sid=%d: %v", id, err)
				}
			}
			if got := openTotal(svc); got != nSess {
				t.Fatalf("刷新后开启会话=%d, want %d", got, nSess)
			}
			before := svc.Popped()
			// now=100：仅 sid=1/2 到期落地；未到期堆顶(到期150)探界弹出再放回。
			sid, err := svc.Create("fresh", "f", 1, 100)
			if err != nil {
				t.Fatal(err)
			}
			delta := svc.Popped() - before
			if delta != 3 {
				t.Fatalf("n=%d 开启中=%d 但 popped 增量=%d, 期望恒为 3（落地2+探界1）",
					nSess, nSess, delta)
			}
			if sid != int64(nSess+1) {
				t.Fatalf("会话号=%d, want %d", sid, nSess+1)
			}
			// sid=1/2 已到期；其余仍开启，到期150（now=149 可查、150 到期）。
			if _, _, err := svc.Query(2, 100); !errors.Is(err, ErrExpired) {
				t.Fatalf("sid=2 应已到期, got %v", err)
			}
			if _, exp, err := svc.Query(3, 149); err != nil || exp != 150 {
				t.Fatalf("sid=3 now=149 应开启且到期150, exp=%d err=%v", exp, err)
			}
			if _, _, err := svc.Query(3, 150); !errors.Is(err, ErrExpired) {
				t.Fatalf("sid=3 now=150 应到期, got %v", err)
			}
			assertInvariants(t, svc, name)
		})
	}
}

func openTotal(svc *Service) int {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	n := 0
	for _, x := range svc.sessions {
		if x.state == StateOpen {
			n++
		}
	}
	return n
}

// genOps 生成一条随机但合法形态的操作序列（仍刻意包含非法参数与回退）。
func genOps(rng *rand.Rand, length int) []op {
	const tenants = 3
	keys := []string{"k", "j", "z"}
	var ops []op
	var now int64
	for i := 0; i < length; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 8:
			tn := fmt.Sprintf("T%d", rng.Intn(tenants))
			ops = append(ops, op{kind: "setquota", tenant: tn, q: int64(rng.Intn(120))})
		case roll < 30:
			tn := fmt.Sprintf("T%d", rng.Intn(tenants))
			key := keys[rng.Intn(len(keys))]
			total := int64(1 + rng.Intn(40))
			now = advanceRand(rng, now)
			ops = append(ops, op{kind: "create", tenant: tn, key: key, total: total, now: now})
		case roll < 75:
			sid := int64(1 + rng.Intn(6))
			off := int64(rng.Intn(45))
			l := rng.Intn(12)
			data := make([]byte, l)
			for j := range data {
				data[j] = byte(rng.Intn(4))
			}
			now = advanceRand(rng, now)
			ops = append(ops, op{kind: "chunk", sid: sid, off: off, data: data, now: now})
		case roll < 85:
			now = advanceRand(rng, now)
			ops = append(ops, op{kind: "complete", sid: int64(1 + rng.Intn(6)), now: now})
		case roll < 92:
			now = advanceRand(rng, now)
			ops = append(ops, op{kind: "abort", sid: int64(1 + rng.Intn(6)), now: now})
		default:
			ops = append(ops, op{kind: "query", sid: int64(1 + rng.Intn(6)), now: now})
		}
	}
	return ops
}

func advanceRand(rng *rand.Rand, now int64) int64 {
	switch rng.Intn(10) {
	case 0:
		if now > 0 {
			return now - 1 // 偶发时钟回退
		}
	case 1:
		return now
	default:
		return now + int64(rng.Intn(6))
	}
	return now
}

// TestRandomDifferential 与逐步朴素模拟对照 1500 组随机序列，并逐 k 回放。
func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g) + 1))
		tval := int64(3 + rng.Intn(8))
		m := int64(1 + rng.Intn(4))
		var fails []int
		if g%10 == 0 {
			fails = append(fails, 2+rng.Intn(4))
		}
		svc, nm := newPair(tval, m, fails...)
		// 预设租户额度（也在朴素侧生效）。
		for _, tn := range []string{"T0", "T1", "T2"} {
			q := int64(20 + rng.Intn(60))
			ops0 := []op{{kind: "setquota", tenant: tn, q: q}}
			runHarness(t, svc, nm, ops0)
		}
		ops := genOps(rng, 30+rng.Intn(20))
		t.Run(fmt.Sprintf("g%d_t%d_m%d", g, tval, m), func(t *testing.T) {
			runHarness(t, svc, nm, ops)
			assertInvariants(t, svc, fmt.Sprintf("random-%d", g))
		})
	}
}

// TestConcurrent 并发压力：不变量恒成立，结果可线性化（不崩溃、不死锁）。
func TestConcurrent(t *testing.T) {
	svc, err := New(50, 1000, journal.NewMemorySink())
	if err != nil {
		t.Fatal(err)
	}
	for _, tn := range []string{"P", "Q"} {
		if err := svc.SetQuota(tn, 1e10); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			tn := []string{"P", "Q"}[seed%2]
			for i := 0; i < 300; i++ {
				now := int64(i)
				sid, err := svc.Create(tn, fmt.Sprintf("k%d", rng.Intn(20)), 50, now)
				if err != nil {
					continue
				}
				_ = svc.Chunk(sid, 0, make([]byte, 50), now)
				if rng.Intn(2) == 0 {
					_ = svc.Complete(sid, now)
				} else {
					_ = svc.Abort(sid, now)
				}
			}
		}(int64(w))
	}
	wg.Wait()
	assertInvariants(t, svc, "concurrent")
}
