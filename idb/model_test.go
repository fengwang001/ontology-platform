package idb

import (
	"bytes"
	"fmt"
	"maps"
	"math/rand"
	"strings"
	"testing"
)

// ---------- 朴素模型 ----------
// 独立编写的参照实现：已提交数据用普通 map，只读快照整体深拷贝，
// 调度每轮扫描全部事务（含已结束的）。刻意不共享内核的任何内部结构，
// 用朴素且显然正确的方法对照内核的增量调度与 MVCC。

type mReq struct {
	done  bool
	err   Kind
	val   []byte
	found bool
}

type mTx struct {
	id       int
	mode     Mode
	scope    map[string]bool
	state    TxState
	outcome  Outcome
	snap     map[string]map[string][]byte
	overlay  map[string]map[string][]byte
	queue    []mQueued // 开始前排队的请求
	reqCount int
	conn     *mConn
}

type mQueued struct {
	req    *mReq
	run    func()
	cancel func()
}

type mConn struct {
	txs     map[*mTx]bool
	closing bool
	closed  bool
}

type model struct {
	stores map[string]map[string][]byte
	txs    []*mTx
	nextID int
}

func newModel(storeNames ...string) *model {
	m := &model{stores: map[string]map[string][]byte{}}
	for _, s := range storeNames {
		m.stores[s] = map[string][]byte{}
	}
	return m
}

func (m *model) addConn() *mConn {
	return &mConn{txs: map[*mTx]bool{}}
}

func deepCopyStores(src map[string]map[string][]byte) map[string]map[string][]byte {
	out := map[string]map[string][]byte{}
	for s, kv := range src {
		out[s] = maps.Clone(kv)
	}
	return out
}

// createTx 与内核相同的校验顺序：参数非法 > 状态不允许 > 仓库不存在。
func (m *model) createTx(c *mConn, mode Mode, scope []string) (*mTx, Kind) {
	if len(scope) == 0 {
		return nil, KindInvalidArg
	}
	scopeSet := map[string]bool{}
	for _, s := range scope {
		if s == "" {
			return nil, KindInvalidArg
		}
		scopeSet[s] = true
	}
	if c.closing || c.closed {
		return nil, KindInvalidState
	}
	for s := range scopeSet {
		if _, ok := m.stores[s]; !ok {
			return nil, KindStoreNotFound
		}
	}
	m.nextID++
	tx := &mTx{id: m.nextID, mode: mode, scope: scopeSet, state: TxPending, conn: c}
	c.txs[tx] = true
	m.txs = append(m.txs, tx)
	m.schedule()
	return tx, KindNone
}

// schedule 朴素地反复扫描全部事务，把可开始的 pending 事务置为运行中。
func (m *model) schedule() {
	for {
		progressed := false
		for _, tx := range m.txs {
			if tx.state != TxPending {
				continue
			}
			if m.canStart(tx) {
				tx.state = TxActive
				tx.snap = deepCopyStores(m.stores)
				queued := tx.queue
				tx.queue = nil
				for _, q := range queued {
					if tx.state != TxActive {
						q.cancel()
						continue
					}
					q.run()
				}
				progressed = true
			}
		}
		if !progressed {
			return
		}
		m.commitCheck()
	}
}

// canStart：不与任何运行中事务违反并行规则，且不越过更早创建、重叠、未结束的排队事务。
func (m *model) canStart(tx *mTx) bool {
	for _, other := range m.txs {
		if other.id >= tx.id || other.state == TxFinished {
			continue
		}
		if !mOverlap(other.scope, tx.scope) {
			continue
		}
		if other.state == TxPending {
			return false // 不得越过更早的排队重叠事务
		}
		if !(other.mode == ReadOnly && tx.mode == ReadOnly) {
			return false // 与运行中事务重叠且非只读-只读
		}
	}
	return true
}

func mOverlap(a, b map[string]bool) bool {
	for s := range a {
		if b[s] {
			return true
		}
	}
	return false
}

// commitCheck 自动提交：没有未完成请求的运行中事务提交。
func (m *model) commitCheck() {
	for {
		progressed := false
		for _, tx := range m.txs {
			if tx.state == TxActive && tx.reqCount > 0 && len(tx.queue) == 0 {
				m.commitTx(tx)
				progressed = true
			}
		}
		if !progressed {
			return
		}
	}
}

func (m *model) commitTx(tx *mTx) {
	for store, kvs := range tx.overlay {
		for key, val := range kvs {
			m.stores[store][key] = val
		}
	}
	m.finishTx(tx, OutcomeCommitted)
}

func (m *model) finishTx(tx *mTx, outcome Outcome) {
	if tx.state == TxFinished {
		return
	}
	tx.state = TxFinished
	tx.outcome = outcome
	for _, q := range tx.queue {
		q.cancel()
	}
	tx.queue = nil
	tx.overlay = nil
	delete(tx.conn.txs, tx)
	m.checkConn(tx.conn)
}

func (m *model) checkConn(c *mConn) {
	if c.closing && !c.closed && len(c.txs) == 0 {
		c.closed = true
	}
}

func (m *model) closeConn(c *mConn) {
	if c.closing || c.closed {
		return
	}
	c.closing = true
	for tx := range c.txs {
		if tx.state == TxPending {
			m.finishTx(tx, OutcomeCancelled)
		}
	}
	m.checkConn(c)
	m.schedule()
}

// request 与内核 submitLocked 相同的拒绝次序：
// 参数非法 > 状态不允许 > 仓库不存在 > 作用域不符 > 事务已结束。
// cb 在请求完成后同步执行（等价于内核的锁外回调）。
func (m *model) request(tx *mTx, isPut bool, store, key string, value []byte, addOnly, ignoreErr bool, cb func(*mReq)) *mReq {
	r := &mReq{}
	finish := func(k Kind) {
		r.done = true
		r.err = k
		if cb != nil {
			cb(r)
		}
	}
	switch {
	case store == "" || key == "":
		finish(KindInvalidArg)
		return r
	case isPut && tx.mode == ReadOnly:
		finish(KindInvalidState)
		return r
	}
	if _, ok := m.stores[store]; !ok {
		finish(KindStoreNotFound)
		return r
	}
	if !tx.scope[store] {
		finish(KindScopeMismatch)
		return r
	}
	if tx.state == TxFinished {
		finish(KindTxFinished)
		return r
	}
	if tx.state == TxPending {
		tx.queue = append(tx.queue, mQueued{
			req: r,
			run: func() {
				m.execute(tx, r, isPut, store, key, value, addOnly, ignoreErr, cb)
			},
			cancel: func() {
				r.done = true
				r.err = KindCancelled
				if cb != nil {
					cb(r)
				}
			},
		})
		return r
	}
	m.execute(tx, r, isPut, store, key, value, addOnly, ignoreErr, cb)
	return r
}

func (m *model) execute(tx *mTx, r *mReq, isPut bool, store, key string, value []byte, addOnly, ignoreErr bool, cb func(*mReq)) {
	tx.reqCount++
	r.done = true
	if !isPut {
		if vs, ok := tx.overlay[store]; ok {
			if v, ok := vs[key]; ok {
				r.val, r.found = v, true
			}
		}
		if !r.found {
			r.val, r.found = tx.snap[store][key]
		}
		if cb != nil {
			cb(r)
		}
		return
	}
	if addOnly {
		_, inOverlay := tx.overlay[store][key]
		_, inSnap := tx.snap[store][key]
		if inOverlay || inSnap {
			r.err = KindKeyConflict
			if !ignoreErr {
				m.finishTx(tx, OutcomeAborted)
			}
			if cb != nil {
				cb(r)
			}
			return
		}
	}
	if tx.overlay == nil {
		tx.overlay = map[string]map[string][]byte{}
	}
	if tx.overlay[store] == nil {
		tx.overlay[store] = map[string][]byte{}
	}
	tx.overlay[store][key] = value
	if cb != nil {
		cb(r)
	}
}

func (m *model) commit(tx *mTx) Kind {
	if tx.state == TxFinished {
		return KindTxFinished
	}
	m.commitTx(tx)
	m.schedule()
	return KindNone
}

func (m *model) abort(tx *mTx) Kind {
	if tx.state == TxFinished {
		return KindTxFinished
	}
	m.finishTx(tx, OutcomeAborted)
	m.schedule()
	return KindNone
}

// ---------- 随机对照测试 ----------

type reqSpec struct {
	isPut     bool
	store     string
	key       string
	value     []byte
	addOnly   bool
	ignoreErr bool
}

func (s reqSpec) String() string {
	if s.isPut {
		return fmt.Sprintf("Put(%s,%s,%s,addOnly=%v,ignoreErr=%v)", s.store, s.key, s.value, s.addOnly, s.ignoreErr)
	}
	return fmt.Sprintf("Get(%s,%s)", s.store, s.key)
}

type txPair struct {
	kt *Tx
	mt *mTx
}

type connPair struct {
	kc *Connection
	mc *mConn
}

type chainPair struct {
	specs []reqSpec
	kreqs []*Request
	mreqs []*mReq
}

func kernelChain(tx *Tx, specs []reqSpec) []*Request {
	reqs := make([]*Request, len(specs))
	var submit func(i int)
	submit = func(i int) {
		if i >= len(specs) {
			return
		}
		s := specs[i]
		cb := func(*Request) { submit(i + 1) }
		if s.isPut {
			reqs[i] = tx.Put(s.store, []byte(s.key), s.value, &Options{AddOnly: s.addOnly, IgnoreError: s.ignoreErr}, cb)
		} else {
			reqs[i] = tx.Get(s.store, []byte(s.key), &Options{IgnoreError: s.ignoreErr}, cb)
		}
	}
	submit(0)
	return reqs
}

func modelChain(m *model, tx *mTx, specs []reqSpec) []*mReq {
	reqs := make([]*mReq, len(specs))
	var submit func(i int)
	submit = func(i int) {
		if i >= len(specs) {
			return
		}
		s := specs[i]
		reqs[i] = m.request(tx, s.isPut, s.store, s.key, s.value, s.addOnly, s.ignoreErr, func(*mReq) { submit(i + 1) })
	}
	submit(0)
	return reqs
}

func compareChains(t *testing.T, ctx string, chains []chainPair) {
	t.Helper()
	for ci, c := range chains {
		for i := range c.specs {
			kr, mr := c.kreqs[i], c.mreqs[i]
			if kr == nil || mr == nil {
				if (kr == nil) != (mr == nil) {
					t.Fatalf("%s 链%d 请求%d %s: 提交进度不一致 kernel=%v model=%v", ctx, ci, i, c.specs[i], kr != nil, mr != nil)
				}
				continue
			}
			if kr.Done() != mr.done {
				t.Fatalf("%s 链%d 请求%d %s: 完成状态 kernel=%v model=%v", ctx, ci, i, c.specs[i], kr.Done(), mr.done)
			}
			if !kr.Done() {
				continue
			}
			if KindOf(kr.Err()) != mr.err {
				t.Fatalf("%s 链%d 请求%d %s: 错误 kernel=%v model=%v", ctx, ci, i, c.specs[i], kr.Err(), mr.err)
			}
			if mr.err == KindNone && !c.specs[i].isPut {
				kv, kf := kr.Value()
				if kf != mr.found || !bytes.Equal(kv, mr.val) {
					t.Fatalf("%s 链%d 请求%d %s: 结果 kernel=(%q,%v) model=(%q,%v)",
						ctx, ci, i, c.specs[i], kv, kf, mr.val, mr.found)
				}
			}
		}
	}
}

func checkConsistency(t *testing.T, ctx string, k *Kernel, m *model, txs []txPair, conns []connPair) {
	t.Helper()
	for i, p := range txs {
		if ks, ms := p.kt.State(), p.mt.state; ks != ms {
			t.Fatalf("%s tx#%d 状态 kernel=%v model=%v", ctx, i, ks, ms)
		}
		if ko, mo := p.kt.Outcome(), p.mt.outcome; ko != mo {
			t.Fatalf("%s tx#%d 结局 kernel=%v model=%v", ctx, i, ko, mo)
		}
	}
	for i, p := range conns {
		if p.kc.Closing() != p.mc.closing || p.kc.FullyClosed() != p.mc.closed {
			t.Fatalf("%s conn#%d kernel=(closing=%v,closed=%v) model=(closing=%v,closed=%v)",
				ctx, i, p.kc.Closing(), p.kc.FullyClosed(), p.mc.closing, p.mc.closed)
		}
	}
	_, stores := k.Snapshot("db")
	if len(stores) != len(m.stores) {
		t.Fatalf("%s 仓库集合 kernel=%d model=%d", ctx, len(stores), len(m.stores))
	}
	for name, mkv := range m.stores {
		kkv, ok := stores[name]
		if !ok || len(kkv) != len(mkv) {
			t.Fatalf("%s 仓库 %s kernel=%v model=%v", ctx, name, kkv, mkv)
		}
		for key, mv := range mkv {
			if !bytes.Equal(kkv[key], mv) {
				t.Fatalf("%s 仓库 %s 键 %s kernel=%q model=%q", ctx, name, key, kkv[key], mv)
			}
		}
	}
}

// 随机操作序列对照：内核（增量调度 + MVCC）与朴素模型（全量扫描 + 深拷贝快照）
// 在每个操作后比较事务状态、请求结果与已提交数据。日志打印输入、输出与判定依据。
func TestRandomizedAgainstModel(t *testing.T) {
	storeNames := []string{"s1", "s2", "s3"}
	keyNames := []string{"k1", "k2", "k3", "k4"}

	for seed := int64(0); seed < 150; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			k := NewKernel()
			m := newModel(storeNames...)

			upg := k.Open("db", 1, func(u *UpgradeTx) {
				for _, s := range storeNames {
					if err := u.CreateStore(s); err != nil {
						t.Fatalf("create store: %v", err)
					}
				}
			})
			conn0, err := upg.Result()
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			conns := []connPair{{kc: conn0, mc: m.addConn()}}
			var txs []txPair
			var chains []chainPair

			randStore := func() string {
				switch r := rng.Intn(100); {
				case r < 80:
					return storeNames[rng.Intn(len(storeNames))]
				case r < 90:
					return "sX" // 不存在的仓库
				case r < 95:
					return "" // 参数非法
				default:
					return storeNames[rng.Intn(len(storeNames))]
				}
			}
			randKey := func() string {
				if rng.Intn(100) < 5 {
					return ""
				}
				return keyNames[rng.Intn(len(keyNames))]
			}

			for op := 0; op < 400; op++ {
				ctx := fmt.Sprintf("seed=%d op=%d", seed, op)
				switch r := rng.Intn(100); {
				case r < 8:
					// 打开新连接（等于当前版本，直接打开）。
					openConns := 0
					for _, c := range conns {
						if !c.mc.closed {
							openConns++
						}
					}
					if openConns >= 3 {
						continue
					}
					req := k.Open("db", 1, nil)
					kc, err := req.Result()
					if err != nil {
						t.Fatalf("%s open conn: %v", ctx, err)
					}
					conns = append(conns, connPair{kc: kc, mc: m.addConn()})
					t.Logf("%s 输入: OpenConn; 输出: conns=%d", ctx, len(conns))
				case r < 26 && len(conns) > 0:
					// 创建事务。
					ci := rng.Intn(len(conns))
					mode := ReadOnly
					if rng.Intn(2) == 0 {
						mode = ReadWrite
					}
					var scope []string
					if rng.Intn(100) < 5 {
						scope = nil // 空作用域 → 参数非法
					} else {
						n := 1 + rng.Intn(3)
						perm := rng.Perm(len(storeNames))
						for i := 0; i < n; i++ {
							scope = append(scope, storeNames[perm[i]])
						}
						if rng.Intn(100) < 10 {
							scope = append(scope, "sX") // 仓库不存在
						}
					}
					kt, kerr := conns[ci].kc.CreateTx(mode, scope)
					mt, mkind := m.createTx(conns[ci].mc, mode, scope)
					t.Logf("%s 输入: CreateTx(conn#%d,%s,%v); 输出: kernel=%v model=%v",
						ctx, ci, mode, scope, KindOf(kerr), mkind)
					if KindOf(kerr) != mkind {
						t.Fatalf("%s CreateTx 错误类别 kernel=%v model=%v", ctx, KindOf(kerr), mkind)
					}
					if kerr == nil {
						txs = append(txs, txPair{kt: kt, mt: mt})
					}
				case r < 64 && len(txs) > 0:
					// 在一对事务上提交一条请求链（回调内续发）。
					ti := rng.Intn(len(txs))
					n := 1 + rng.Intn(3)
					specs := make([]reqSpec, n)
					for i := range specs {
						isPut := rng.Intn(2) == 0
						s := reqSpec{
							isPut:     isPut,
							store:     randStore(),
							key:       randKey(),
							ignoreErr: rng.Intn(100) < 30,
						}
						if isPut {
							s.value = []byte(fmt.Sprintf("v%d", rng.Intn(1000)))
							s.addOnly = rng.Intn(100) < 30
						}
						specs[i] = s
					}
					kreqs := kernelChain(txs[ti].kt, specs)
					mreqs := modelChain(m, txs[ti].mt, specs)
					m.commitCheck()
					m.schedule()
					chains = append(chains, chainPair{specs: specs, kreqs: kreqs, mreqs: mreqs})
					t.Logf("%s 输入: RequestChain(tx#%d,%v); 输出: kernel=%s model=%s",
						ctx, ti, specs, chainSummary(kreqs), mChainSummary(mreqs))
				case r < 76 && len(txs) > 0:
					ti := rng.Intn(len(txs))
					kerr := txs[ti].kt.Commit()
					mkind := m.commit(txs[ti].mt)
					t.Logf("%s 输入: Commit(tx#%d); 输出: kernel=%v model=%v", ctx, ti, KindOf(kerr), mkind)
					if KindOf(kerr) != mkind {
						t.Fatalf("%s Commit 错误类别 kernel=%v model=%v", ctx, KindOf(kerr), mkind)
					}
				case r < 86 && len(txs) > 0:
					ti := rng.Intn(len(txs))
					kerr := txs[ti].kt.Abort()
					mkind := m.abort(txs[ti].mt)
					t.Logf("%s 输入: Abort(tx#%d); 输出: kernel=%v model=%v", ctx, ti, KindOf(kerr), mkind)
					if KindOf(kerr) != mkind {
						t.Fatalf("%s Abort 错误类别 kernel=%v model=%v", ctx, KindOf(kerr), mkind)
					}
				case r < 94 && len(conns) > 0:
					ci := rng.Intn(len(conns))
					conns[ci].kc.Close()
					m.closeConn(conns[ci].mc)
					t.Logf("%s 输入: CloseConn(conn#%d); 输出: kernelClosed=%v modelClosed=%v",
						ctx, ci, conns[ci].kc.FullyClosed(), conns[ci].mc.closed)
				default:
					continue
				}
				checkConsistency(t, ctx, k, m, txs, conns)
				compareChains(t, ctx, chains)
			}

			checkConsistency(t, fmt.Sprintf("seed=%d final", seed), k, m, txs, conns)
			compareChains(t, fmt.Sprintf("seed=%d final", seed), chains)
			t.Logf("seed=%d 完成: txs=%d chains=%d conns=%d（判定依据：内核与朴素模型逐步一致）",
				seed, len(txs), len(chains), len(conns))
		})
	}
}

func chainSummary(reqs []*Request) string {
	var sb strings.Builder
	for i, r := range reqs {
		if i > 0 {
			sb.WriteString(",")
		}
		if r == nil {
			sb.WriteString("-")
			continue
		}
		if !r.Done() {
			sb.WriteString("queued")
			continue
		}
		if err := r.Err(); err != nil {
			sb.WriteString(KindOf(err).String())
			continue
		}
		if r.op == opGet {
			v, f := r.Value()
			fmt.Fprintf(&sb, "get(%q,%v)", v, f)
		} else {
			sb.WriteString("ok")
		}
	}
	return sb.String()
}

func mChainSummary(reqs []*mReq) string {
	var sb strings.Builder
	for i, r := range reqs {
		if i > 0 {
			sb.WriteString(",")
		}
		if r == nil {
			sb.WriteString("-")
			continue
		}
		if !r.done {
			sb.WriteString("queued")
			continue
		}
		if r.err != KindNone {
			sb.WriteString(r.err.String())
			continue
		}
		if r.found {
			fmt.Fprintf(&sb, "get(%q,true)", r.val)
		} else {
			sb.WriteString("ok")
		}
	}
	return sb.String()
}

// 保留 strings 引用。
var _ = strings.NewReader
