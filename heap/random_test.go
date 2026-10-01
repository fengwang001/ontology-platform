package heap

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naive 是「总是回表」的朴素判定模型：不维护全可见位，
// Scan 对每个被扫到的条目都按 xmin<s && (xmax=0 || xmax>=s) 判定。
// 它同时按规则推导每页全可见位应有的值，用于核对被测实现。
type naive struct {
	pages  [][]*Row
	allVis []bool
	index  []Entry
	snaps  map[int]int64
	nextID int
	maxH   int64
}

func newNaive(p, c int) *naive {
	pages := make([][]*Row, p)
	for i := range pages {
		pages[i] = make([]*Row, c)
	}
	return &naive{pages: pages, allVis: make([]bool, p), snaps: map[int]int64{}, nextID: 1}
}

func (n *naive) insert(key string, xid int64) (int, int, error) {
	if xid <= 0 {
		return 0, 0, ErrInvalidXid
	}
	for p := range n.pages {
		for s := range n.pages[p] {
			if n.pages[p][s] == nil {
				n.pages[p][s] = &Row{Key: key, Xmin: xid}
				e := Entry{Key: key, Page: p, Slot: s}
				i := sort.Search(len(n.index), func(i int) bool { return !indexLess(n.index[i], e) })
				n.index = append(n.index, Entry{})
				copy(n.index[i+1:], n.index[i:])
				n.index[i] = e
				n.allVis[p] = false
				return p, s, nil
			}
		}
	}
	return 0, 0, ErrNoFreeSlot
}

func (n *naive) delete(page, slot int, xid int64) error {
	if xid <= 0 {
		return ErrInvalidXid
	}
	if page < 0 || page >= len(n.pages) || slot < 0 || slot >= len(n.pages[page]) {
		return ErrSlotOutOfRange
	}
	row := n.pages[page][slot]
	if row == nil {
		return ErrSlotEmpty
	}
	if row.Xmax != 0 {
		return ErrAlreadyDeleted
	}
	row.Xmax = xid
	n.allVis[page] = false
	return nil
}

func (n *naive) snapshot(s int64) (int, error) {
	if s <= 0 {
		return 0, ErrInvalidSnapshotValue
	}
	if s < n.maxH {
		return 0, ErrSnapshotTooOld
	}
	id := n.nextID
	n.nextID++
	n.snaps[id] = s
	return id, nil
}

func (n *naive) release(id int) error {
	if _, ok := n.snaps[id]; !ok {
		return ErrUnknownSnapshot
	}
	delete(n.snaps, id)
	return nil
}

func (n *naive) vacuum(page int, h int64) error {
	if h <= 0 {
		return ErrInvalidHorizon
	}
	if page < 0 || page >= len(n.pages) {
		return ErrPageOutOfRange
	}
	for _, s := range n.snaps {
		if s < h {
			return ErrSnapshotBlocking
		}
	}
	for slot, row := range n.pages[page] {
		if row != nil && row.Xmax != 0 && row.Xmax < h {
			e := Entry{Key: row.Key, Page: page, Slot: slot}
			i := sort.Search(len(n.index), func(i int) bool { return !indexLess(n.index[i], e) })
			if i < len(n.index) && n.index[i] == e {
				n.index = append(n.index[:i], n.index[i+1:]...)
			}
			n.pages[page][slot] = nil
		}
	}
	allVis := true
	for _, row := range n.pages[page] {
		if row != nil && !(row.Xmin < h && row.Xmax == 0) {
			allVis = false
			break
		}
	}
	n.allVis[page] = allVis
	if h > n.maxH {
		n.maxH = h
	}
	return nil
}

// scan 总是回表：fetches 等于被扫到的条目数中所在页全可见位为假的数量。
func (n *naive) scan(lo, hi string, snapID int) (ScanResult, error) {
	var res ScanResult
	if lo > hi {
		return res, ErrInvalidRange
	}
	s, ok := n.snaps[snapID]
	if !ok {
		return res, ErrUnknownSnapshot
	}
	for _, e := range n.index {
		if e.Key < lo {
			continue
		}
		if e.Key >= hi {
			break
		}
		if n.allVis[e.Page] {
			res.Skips++
		} else {
			res.Fetches++
		}
		row := n.pages[e.Page][e.Slot]
		if row != nil && row.Xmin < s && (row.Xmax == 0 || row.Xmax >= s) {
			res.Rows = append(res.Rows, e)
		}
	}
	return res, nil
}

func errReason(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// TestRandomDifferential 对拍 2000 组随机操作序列：
// 被测实现（带可见性映射）与朴素模型（总是回表）逐步执行相同操作，
// 要求错误一致、产出集合一致、回表次数等于全可见位为假的页上被扫到的条目数。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	keys := []string{"", "a", "b", "c", "d", "e", "f", "g"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		p, c := 1+rng.Intn(4), 1+rng.Intn(4)
		tb, err := New(p, c)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		nv := newNaive(p, c)
		ops := 20 + rng.Intn(60)
		liveSnaps := map[int]bool{}

		for step := 0; step < ops; step++ {
			stepLog := fmt.Sprintf("seq=%d step=%d", seq, step)
			fail := func(format string, args ...interface{}) {
				t.Helper()
				t.Fatalf("%s: %s", stepLog, fmt.Sprintf(format, args...))
			}

			switch rng.Intn(6) {
			case 0: // Insert
				key := keys[rng.Intn(len(keys))]
				xid := int64(rng.Intn(12)) // 含 0，触发拒绝
				gp, gs, gerr := tb.Insert(key, xid)
				wp, ws, werr := nv.insert(key, xid)
				t.Logf("%s Insert(%q,%d) -> got=(%d,%d,%s) want=(%d,%d,%s)",
					stepLog, key, xid, gp, gs, errReason(gerr), wp, ws, errReason(werr))
				if !errors.Is(gerr, werr) || gp != wp || gs != ws {
					fail("Insert 不一致")
				}
			case 1: // Delete
				page, slot := rng.Intn(p+1), rng.Intn(c+1)
				if rng.Intn(4) == 0 {
					page = rng.Intn(p + 2) // 可能越界
				}
				xid := int64(rng.Intn(12))
				gerr := tb.Delete(page, slot, xid)
				werr := nv.delete(page, slot, xid)
				t.Logf("%s Delete(%d,%d,%d) -> got=%s want=%s",
					stepLog, page, slot, xid, errReason(gerr), errReason(werr))
				if !errors.Is(gerr, werr) {
					fail("Delete 不一致")
				}
			case 2: // Snapshot
				s := int64(rng.Intn(15))
				gid, gerr := tb.Snapshot(s)
				wid, werr := nv.snapshot(s)
				t.Logf("%s Snapshot(%d) -> got=(%d,%s) want=(%d,%s)",
					stepLog, s, gid, errReason(gerr), wid, errReason(werr))
				if !errors.Is(gerr, werr) || gid != wid {
					fail("Snapshot 不一致")
				}
				if gerr == nil {
					liveSnaps[gid] = true
				}
			case 3: // Release
				id := rng.Intn(8)
				gerr := tb.Release(id)
				werr := nv.release(id)
				t.Logf("%s Release(%d) -> got=%s want=%s",
					stepLog, id, errReason(gerr), errReason(werr))
				if !errors.Is(gerr, werr) {
					fail("Release 不一致")
				}
				if gerr == nil {
					delete(liveSnaps, id)
				}
			case 4: // Vacuum
				page := rng.Intn(p + 1) // 可能越界
				h := int64(rng.Intn(15))
				gerr := tb.Vacuum(page, h)
				werr := nv.vacuum(page, h)
				t.Logf("%s Vacuum(%d,%d) -> got=%s want=%s",
					stepLog, page, h, errReason(gerr), errReason(werr))
				if !errors.Is(gerr, werr) {
					fail("Vacuum 不一致")
				}
			case 5: // Scan
				lo, hi := keys[rng.Intn(len(keys))], keys[rng.Intn(len(keys))]
				if rng.Intn(2) == 0 && lo > hi {
					lo, hi = hi, lo
				}
				id := rng.Intn(8)
				got, gerr := tb.Scan(lo, hi, id)
				want, werr := nv.scan(lo, hi, id)
				t.Logf("%s Scan(%q,%q,snap=%d) -> got rows=%v fetches=%d skips=%d err=%s; want rows=%v fetches=%d skips=%d err=%s",
					stepLog, lo, hi, id, got.Rows, got.Fetches, got.Skips, errReason(gerr),
					want.Rows, want.Fetches, want.Skips, errReason(werr))
				if !errors.Is(gerr, werr) {
					fail("Scan 错误不一致")
				}
				if gerr != nil {
					continue
				}
				if !reflect.DeepEqual(got.Rows, want.Rows) {
					fail("Scan 产出集合不一致: got=%v want=%v", got.Rows, want.Rows)
				}
				if got.Fetches != want.Fetches || got.Skips != want.Skips {
					fail("回表/免回表计数不一致: got=(%d,%d) want=(%d,%d)",
						got.Fetches, got.Skips, want.Fetches, want.Skips)
				}
			}

			// 每步核对全可见位与全局最大已用 h。
			for page := 0; page < p; page++ {
				if tb.AllVisible(page) != nv.allVis[page] {
					fail("页 %d 全可见位不一致: got=%v want=%v", page, tb.AllVisible(page), nv.allVis[page])
				}
				// 不变式：置位页上每一行都满足 xmin < maxH 且 xmax = 0。
				if nv.allVis[page] {
					for _, row := range nv.pages[page] {
						if row != nil && !(row.Xmin < nv.maxH && row.Xmax == 0) {
							fail("不变式违反: 页 %d 置位但行 %+v 不满足 xmin<maxH(%d) 且 xmax=0",
								page, row, nv.maxH)
						}
					}
				}
			}
			if tb.MaxH() != nv.maxH {
				fail("maxH 不一致: got=%d want=%d", tb.MaxH(), nv.maxH)
			}
		}
		t.Logf("seq=%d 完成: P=%d C=%d ops=%d 存活快照=%d", seq, p, c, ops, len(liveSnaps))
	}
}

// TestConcurrent 并发调用所有操作，验证串行等价性（配合 -race 使用）。
func TestConcurrent(t *testing.T) {
	tb := mustNew(t, 4, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				switch rng.Intn(6) {
				case 0:
					_, _, _ = tb.Insert(fmt.Sprintf("k%d", rng.Intn(8)), int64(1+rng.Intn(10)))
				case 1:
					_ = tb.Delete(rng.Intn(4), rng.Intn(8), int64(1+rng.Intn(10)))
				case 2:
					id, err := tb.Snapshot(int64(1 + rng.Intn(20)))
					if err == nil {
						_, _ = tb.Scan("k0", "k9", id)
						_ = tb.Release(id)
					}
				case 3:
					_ = tb.Vacuum(rng.Intn(4), int64(1+rng.Intn(20)))
				}
			}
		}(g)
	}
	wg.Wait()
}
