package inodeledger

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// 朴素模型：严格按规则逐条写成，与 Ledger 的实现相互独立。
// Used 每次现算、成员资格每次现判，用于交叉验证。

type mInode struct {
	links  int
	opens  int
	blocks int
	pend   bool
	pb     int
}

type model struct {
	P, K, L int
	inodes  map[int]*mInode
	handles map[int]int
	orphans []int
	nextI   int
	nextH   int
}

func newModel(P, K, L int) *model {
	return &model{P: P, K: K, L: L, inodes: map[int]*mInode{}, handles: map[int]int{}, nextI: 1, nextH: 1}
}

func (m *model) used() int {
	s := 0
	for _, in := range m.inodes {
		s += in.blocks
	}
	return s
}

func (m *model) inList(i int) bool {
	for _, id := range m.orphans {
		if id == i {
			return true
		}
	}
	return false
}

func (m *model) prepend(i int) {
	if m.inList(i) {
		return
	}
	m.orphans = append([]int{i}, m.orphans...)
}

func (m *model) drop(i int) {
	for idx, id := range m.orphans {
		if id == i {
			m.orphans = append(m.orphans[:idx], m.orphans[idx+1:]...)
			return
		}
	}
}

func (m *model) del(i int) {
	delete(m.inodes, i)
	m.drop(i)
}

func (m *model) create(b int) (int, error) {
	if b < 0 {
		return 0, ErrInvalidArgument
	}
	if m.used()+b > m.P {
		return 0, ErrInsufficientSpace
	}
	id := m.nextI
	m.nextI++
	m.inodes[id] = &mInode{links: 1, blocks: b}
	return id, nil
}

func (m *model) link(i int) error {
	in, ok := m.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if in.links == 0 {
		return ErrNoLinks
	}
	if in.links+1 > m.L {
		return ErrLinkLimit
	}
	in.links++
	return nil
}

func (m *model) unlink(i int) error {
	in, ok := m.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if in.links == 0 {
		return ErrNoLinks
	}
	if in.links == 1 && in.opens > 0 && !m.inList(i) && len(m.orphans) >= m.K {
		return ErrOrphanListFull
	}
	in.links--
	if in.links == 0 {
		if in.opens == 0 {
			m.del(i)
		} else {
			m.prepend(i)
		}
	}
	return nil
}

func (m *model) open(i int) (int, error) {
	in, ok := m.inodes[i]
	if !ok {
		return 0, ErrNotFound
	}
	if in.links == 0 {
		return 0, ErrNoLinks
	}
	h := m.nextH
	m.nextH++
	m.handles[h] = i
	in.opens++
	return h, nil
}

func (m *model) close(h int) error {
	i, ok := m.handles[h]
	if !ok {
		return ErrNotFound
	}
	delete(m.handles, h)
	in := m.inodes[i]
	in.opens--
	if in.opens == 0 && in.links == 0 {
		m.del(i)
	}
	return nil
}

func (m *model) shrink(i, nb int) error {
	if nb < 0 {
		return ErrInvalidArgument
	}
	in, ok := m.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if nb > in.blocks {
		return ErrInvalidArgument
	}
	if in.pend {
		return ErrShrinkInProgress
	}
	in.blocks = nb
	return nil
}

func (m *model) beginShrink(i, nb int) error {
	if nb < 0 {
		return ErrInvalidArgument
	}
	in, ok := m.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if nb > in.blocks {
		return ErrInvalidArgument
	}
	if in.pend {
		return ErrShrinkInProgress
	}
	if !m.inList(i) && len(m.orphans) >= m.K {
		return ErrOrphanListFull
	}
	in.pend = true
	in.pb = nb
	m.prepend(i)
	return nil
}

func (m *model) finishShrink(i int) error {
	in, ok := m.inodes[i]
	if !ok {
		return ErrNotFound
	}
	if !in.pend {
		return ErrNoPendingShrink
	}
	in.blocks = in.pb
	in.pend = false
	in.pb = 0
	if !(in.links == 0 && in.opens > 0) {
		m.drop(i)
	}
	return nil
}

func (m *model) crash() []CrashRecord {
	m.handles = map[int]int{}
	for _, in := range m.inodes {
		in.opens = 0
	}
	var recs []CrashRecord
	for _, id := range m.orphans {
		in := m.inodes[id]
		if in.links == 0 {
			delete(m.inodes, id)
			recs = append(recs, CrashRecord{Inode: id, Action: ActionDelete})
		} else {
			in.blocks = in.pb
			in.pend = false
			in.pb = 0
			recs = append(recs, CrashRecord{Inode: id, Action: ActionTruncate})
		}
	}
	m.orphans = nil
	return recs
}

// 以下为随机对照 harness。

func errName(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidArgument):
		return "ErrInvalidArgument"
	case errors.Is(err, ErrNotFound):
		return "ErrNotFound"
	case errors.Is(err, ErrNoLinks):
		return "ErrNoLinks"
	case errors.Is(err, ErrShrinkInProgress):
		return "ErrShrinkInProgress"
	case errors.Is(err, ErrNoPendingShrink):
		return "ErrNoPendingShrink"
	case errors.Is(err, ErrLinkLimit):
		return "ErrLinkLimit"
	case errors.Is(err, ErrOrphanListFull):
		return "ErrOrphanListFull"
	case errors.Is(err, ErrInsufficientSpace):
		return "ErrInsufficientSpace"
	default:
		return fmt.Sprintf("unknown(%v)", err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalRecords(a, b []CrashRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRandomModelComparison(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		P := 1 + rng.Intn(30)
		K := 1 + rng.Intn(4)
		L := 1 + rng.Intn(4)
		l, err := New(P, K, L)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		m := newModel(P, K, L)
		created, opened := 0, 0
		ops := 20 + rng.Intn(30)
		for step := 0; step < ops; step++ {
			kind := rng.Intn(10)
			pickI := func() int { return 1 + rng.Intn(created+2) }
			pickH := func() int { return 1 + rng.Intn(opened+2) }
			var input, output, basis string
			fail := func(format string, args ...interface{}) {
				t.Fatalf("seq=%d step=%d 输入=%s: %s", seq, step, input, fmt.Sprintf(format, args...))
			}
			switch kind {
			case 0:
				b := rng.Intn(P+2) - 1
				input = fmt.Sprintf("Create(%d)", b)
				gID, gErr := l.Create(b)
				wID, wErr := m.create(b)
				output = fmt.Sprintf("id=%d err=%s", gID, errName(gErr))
				if gErr == nil {
					created++
					basis = "块池足够且 b>=0，分配连续编号"
				} else {
					basis = "b<0 报参数非法，否则块池不足报空间不足"
				}
				if gID != wID || gErr != wErr {
					fail("输出=%s 模型=id=%d err=%s 判定依据=%s", output, wID, errName(wErr), basis)
				}
			case 1:
				i := pickI()
				input = fmt.Sprintf("Link(%d)", i)
				gErr, wErr := l.Link(i), m.link(i)
				output = "err=" + errName(gErr)
				basis = "不存在>已无链接>链接数超过 L"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			case 2:
				i := pickI()
				input = fmt.Sprintf("Unlink(%d)", i)
				gErr, wErr := l.Unlink(i), m.unlink(i)
				output = "err=" + errName(gErr)
				basis = "不存在>已无链接>首次入链表且链表满报孤儿链表已满；降到 0 时有打开入链表、无打开立即删除"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			case 3:
				i := pickI()
				input = fmt.Sprintf("Open(%d)", i)
				gH, gErr := l.Open(i)
				wH, wErr := m.open(i)
				output = fmt.Sprintf("h=%d err=%s", gH, errName(gErr))
				if gErr == nil {
					opened++
					basis = "inode 存在且链接数不小于 1，分配连续句柄"
				} else {
					basis = "不存在>已无链接"
				}
				if gH != wH || gErr != wErr {
					fail("输出=%s 模型=h=%d err=%s 判定依据=%s", output, wH, errName(wErr), basis)
				}
			case 4:
				h := pickH()
				input = fmt.Sprintf("Close(%d)", h)
				gErr, wErr := l.Close(h), m.close(h)
				output = "err=" + errName(gErr)
				basis = "句柄须存在且未失效；打开数与链接数同降为 0 时立即删除"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			case 5:
				i, nb := pickI(), rng.Intn(12)-1
				input = fmt.Sprintf("Shrink(%d,%d)", i, nb)
				gErr, wErr := l.Shrink(i, nb), m.shrink(i, nb)
				output = "err=" + errName(gErr)
				basis = "nb<0 参数非法>不存在>nb 超过现有块数参数非法>截断进行中"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			case 6:
				i, nb := pickI(), rng.Intn(12)-1
				input = fmt.Sprintf("BeginShrink(%d,%d)", i, nb)
				gErr, wErr := l.BeginShrink(i, nb), m.beginShrink(i, nb)
				output = "err=" + errName(gErr)
				basis = "nb<0 参数非法>不存在>nb 超过现有块数参数非法>截断进行中>首次入链表且链表满报孤儿链表已满"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			case 7:
				i := pickI()
				input = fmt.Sprintf("FinishShrink(%d)", i)
				gErr, wErr := l.FinishShrink(i), m.finishShrink(i)
				output = "err=" + errName(gErr)
				basis = "不存在>无待完成截断；完成后仍是链接 0 且打开大于 0 则留链表原位否则摘除"
				if gErr != wErr {
					fail("输出=%s 模型=err=%s 判定依据=%s", output, errName(wErr), basis)
				}
			default:
				input = "Crash()"
				got, want := l.Crash(), m.crash()
				output = fmt.Sprintf("%v", got)
				basis = "自链表头到尾：链接 0 删除，链接不小于 1 按登记截断"
				if !equalRecords(got, want) {
					fail("输出=%s 模型=%v 判定依据=%s", output, want, basis)
				}
			}
			t.Logf("seq=%d step=%d 输入=%s 输出=%s 判定依据=%s", seq, step, input, output, basis)

			// 每步对照可观察状态。
			if l.Used() != m.used() {
				fail("Used=%d 模型=%d", l.Used(), m.used())
			}
			if !equalInts(l.Orphans(), m.orphans) {
				fail("Orphans=%v 模型=%v", l.Orphans(), m.orphans)
			}
			for i := 1; i <= created+1; i++ {
				gInfo, gOK := l.InfoOf(i)
				wIn, wOK := m.inodes[i]
				if gOK != wOK {
					fail("InfoOf(%d) ok=%v 模型 ok=%v", i, gOK, wOK)
				}
				if !gOK {
					continue
				}
				want := Info{Links: wIn.links, Opens: wIn.opens, Blocks: wIn.blocks, Pending: wIn.pend, PendingBlocks: wIn.pb, InOrphanList: m.inList(i)}
				if gInfo != want {
					fail("InfoOf(%d)=%+v 模型=%+v", i, gInfo, want)
				}
			}

			// 不变式校验。
			if l.Used() > P {
				fail("Used=%d 超过 P=%d", l.Used(), P)
			}
			if len(l.Orphans()) > K {
				fail("孤儿链表长度 %d 超过 K=%d", len(l.Orphans()), K)
			}
			for i := 1; i <= created+1; i++ {
				info, ok := l.InfoOf(i)
				if !ok {
					continue
				}
				member := (info.Links == 0 && info.Opens > 0) || info.Pending
				if info.InOrphanList != member {
					fail("inode %d 成员资格错误: %+v", i, info)
				}
				if info.Links == 0 && info.Opens == 0 {
					fail("inode %d 链接数与打开数同时为 0", i)
				}
			}
		}
	}
}
