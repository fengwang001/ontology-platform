package undo

// 本文件按规格逐条规则写成的朴素模拟（naive model），
// 与 Manager 的实现相互独立，用于随机操作序列的对照验证。

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type simSeg struct {
	n     int
	pages int
	trxNo int
}

type simTx struct {
	terminated bool
	hasI       bool
	hasU       bool
	segI       *simSeg
	segU       *simSeg
}

type sim struct {
	S, K, PB  int
	usedSlots int
	usedPages int
	issued    int
	nextView  int
	txs       map[int]*simTx
	views     map[int]int
	cacheI    []*simSeg
	cacheU    []*simSeg
	history   []*simSeg
}

func newSim(s, k, pb int) *sim {
	return &sim{S: s, K: k, PB: pb, txs: map[int]*simTx{}, views: map[int]int{}}
}

// simRelease 按 Release 规则处理段。kindI 表示插入 undo 段。
func (s *sim) simRelease(seg *simSeg, kindI bool) {
	if seg.pages == 1 && 4*seg.n <= 3*s.K {
		if kindI {
			s.cacheI = append(s.cacheI, seg)
		} else {
			s.cacheU = append(s.cacheU, seg)
		}
		return
	}
	s.usedSlots--
	s.usedPages -= seg.pages
}

func (s *sim) begin(t int) Code {
	if t < 1 || t > 1_000_000 {
		return CodeInvalidParam
	}
	if _, ok := s.txs[t]; ok {
		return CodeTxExists
	}
	s.txs[t] = &simTx{}
	return -1
}

func (s *sim) activeTx(t int) (*simTx, Code) {
	if t < 1 || t > 1_000_000 {
		return nil, CodeInvalidParam
	}
	tx, ok := s.txs[t]
	if !ok {
		return nil, CodeTxNotFound
	}
	if tx.terminated {
		return nil, CodeTxTerminated
	}
	return tx, -1
}

func (s *sim) append(t int, kindI bool) Code {
	tx, code := s.activeTx(t)
	if code != -1 {
		return code
	}
	var seg *simSeg
	if kindI {
		seg = tx.segI
	} else {
		seg = tx.segU
	}
	if seg == nil {
		// 先尝试缓存复用（不检查槽位），否则先槽位后页预算。
		var cache *[]*simSeg
		if kindI {
			cache = &s.cacheI
		} else {
			cache = &s.cacheU
		}
		if len(*cache) > 0 {
			top := (*cache)[len(*cache)-1]
			*cache = (*cache)[:len(*cache)-1]
			top.n = 0
			seg = top
		} else {
			if s.usedSlots >= s.S {
				return CodeNoFreeSlot
			}
			if s.usedPages >= s.PB {
				return CodePageBudget
			}
			s.usedSlots++
			s.usedPages++
			seg = &simSeg{pages: 1}
		}
		if kindI {
			tx.segI, tx.hasI = seg, true
		} else {
			tx.segU, tx.hasU = seg, true
		}
	}
	if seg.n+1 > s.K*seg.pages {
		if s.usedPages >= s.PB {
			return CodePageBudget
		}
		seg.pages++
		s.usedPages++
	}
	seg.n++
	return -1
}

func (s *sim) commit(t int) Code {
	tx, code := s.activeTx(t)
	if code != -1 {
		return code
	}
	s.issued++
	if tx.hasI {
		s.simRelease(tx.segI, true)
		tx.segI, tx.hasI = nil, false
	}
	if tx.hasU {
		tx.segU.trxNo = s.issued
		s.history = append(s.history, tx.segU)
		tx.segU, tx.hasU = nil, false
	}
	tx.terminated = true
	return -1
}

func (s *sim) rollback(t int) Code {
	tx, code := s.activeTx(t)
	if code != -1 {
		return code
	}
	if tx.hasI {
		s.simRelease(tx.segI, true)
		tx.segI, tx.hasI = nil, false
	}
	if tx.hasU {
		s.simRelease(tx.segU, false)
		tx.segU, tx.hasU = nil, false
	}
	tx.terminated = true
	return -1
}

func (s *sim) openView() int {
	s.nextView++
	s.views[s.nextView] = s.issued + 1
	return s.nextView
}

func (s *sim) closeView(id int) Code {
	if _, ok := s.views[id]; !ok {
		return CodeViewNotFound
	}
	delete(s.views, id)
	return -1
}

func (s *sim) purge(n int) ([]int, Code) {
	if n < 1 {
		return nil, CodeInvalidParam
	}
	pl := s.issued + 1
	for _, l := range s.views {
		if l < pl {
			pl = l
		}
	}
	var out []int
	for len(out) < n && len(s.history) > 0 && s.history[0].trxNo < pl {
		head := s.history[0]
		s.history = s.history[1:]
		out = append(out, head.trxNo)
		s.simRelease(head, false)
	}
	return out, -1
}

func (s *sim) snapshot() Snapshot {
	snap := Snapshot{
		UsedSlots: s.usedSlots,
		UsedPages: s.usedPages,
		IssuedTrx: s.issued,
		OpenViews: len(s.views),
	}
	for _, seg := range s.cacheI {
		snap.CacheI = append(snap.CacheI, seg.n)
	}
	for _, seg := range s.cacheU {
		snap.CacheU = append(snap.CacheU, seg.n)
	}
	for _, seg := range s.history {
		snap.History = append(snap.History, seg.trxNo)
	}
	return snap
}

// op 是一条随机操作。
type op struct {
	name string
	arg  int
}

func (o op) String() string { return fmt.Sprintf("%s(%d)", o.name, o.arg) }

// codeOf 把 error 映射为错误码；-1 表示成功。
func codeOf(err error) Code {
	if err == nil {
		return -1
	}
	return err.(*Error).Code
}

// TestRandomAgainstNaiveModel 用 2000 组随机操作序列对照朴素模拟，
// 日志打印每步输入、输出与判定依据（错误码 / 关键计数）。
func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		S := 1 + rng.Intn(6)
		K := 1 + rng.Intn(6)
		PB := 1 + rng.Intn(20)
		m, err := NewManager(S, K, PB)
		if err != nil {
			t.Fatalf("seq %d: NewManager: %v", seq, err)
		}
		model := newSim(S, K, PB)
		t.Logf("seq=%d 构造 S=%d K=%d PB=%d", seq, S, K, PB)

		steps := 1 + rng.Intn(50)
		for step := 0; step < steps; step++ {
			o := genOp(rng)
			var gotCode, wantCode Code
			var gotPurge, wantPurge []int
			var gotView, wantView int
			basis := ""
			switch o.name {
			case "Begin":
				gotCode, wantCode = codeOf(m.Begin(o.arg)), model.begin(o.arg)
				basis = "参数非法>事务已存在"
			case "Insert":
				gotCode, wantCode = codeOf(m.Insert(o.arg)), model.append(o.arg, true)
				basis = "参数>不存在>已终止>缓存复用|槽位>页预算"
			case "Modify":
				gotCode, wantCode = codeOf(m.Modify(o.arg)), model.append(o.arg, false)
				basis = "参数>不存在>已终止>缓存复用|槽位>页预算"
			case "Commit":
				gotCode, wantCode = codeOf(m.Commit(o.arg)), model.commit(o.arg)
				basis = "trx_no+1，I 立即 Release，U 入历史链"
			case "Rollback":
				gotCode, wantCode = codeOf(m.Rollback(o.arg)), model.rollback(o.arg)
				basis = "全部段立即 Release，不发 trx_no"
			case "OpenView":
				gotView, wantView = m.OpenView(), model.openView()
				basis = "limit=已发 trx_no+1"
			case "CloseView":
				gotCode, wantCode = codeOf(m.CloseView(o.arg)), model.closeView(o.arg)
				basis = "不存在或已关闭报视图不存在"
			case "Purge":
				gotPurge, err = m.Purge(o.arg)
				gotCode = codeOf(err)
				wantPurge, wantCode = model.purge(o.arg)
				basis = "PL=打开视图 limit 最小值（无视图取已发+1），头部 trx_no<PL 才回收"
			}
			if gotCode != wantCode {
				t.Fatalf("seq=%d step=%d op=%s: code got %v want %v", seq, step, o, gotCode, wantCode)
			}
			if o.name == "OpenView" && gotView != wantView {
				t.Fatalf("seq=%d step=%d op=%s: view got %d want %d", seq, step, o, gotView, wantView)
			}
			if o.name == "Purge" && !reflect.DeepEqual(gotPurge, wantPurge) {
				t.Fatalf("seq=%d step=%d op=%s: purge got %v want %v", seq, step, o, gotPurge, wantPurge)
			}
			gotSnap, wantSnap := m.Snapshot(), model.snapshot()
			if !reflect.DeepEqual(gotSnap, wantSnap) {
				t.Fatalf("seq=%d step=%d op=%s: snapshot mismatch\n got %+v\nwant %+v", seq, step, o, gotSnap, wantSnap)
			}
			t.Logf("seq=%d step=%d 输入=%s 输出(code=%d view=%d purge=%v) 判定依据=%s 状态(槽位=%d/%d 页=%d/%d 已发=%d)",
				seq, step, o, gotCode, gotView, gotPurge, basis,
				gotSnap.UsedSlots, S, gotSnap.UsedPages, PB, gotSnap.IssuedTrx)
		}
	}
}

// genOp 生成一条随机操作，事务号集中在小范围以制造冲突，
// 偶尔越界以覆盖参数非法分支。
func genOp(rng *rand.Rand) op {
	txID := func() int {
		switch rng.Intn(20) {
		case 0:
			return 0
		case 1:
			return 1_000_001
		default:
			return 1 + rng.Intn(10)
		}
	}
	switch rng.Intn(8) {
	case 0:
		return op{"Begin", txID()}
	case 1:
		return op{"Insert", txID()}
	case 2:
		return op{"Modify", txID()}
	case 3:
		return op{"Commit", txID()}
	case 4:
		return op{"Rollback", txID()}
	case 5:
		return op{"OpenView", 0}
	case 6:
		return op{"CloseView", rng.Intn(6)}
	default:
		return op{"Purge", rng.Intn(6)}
	}
}
