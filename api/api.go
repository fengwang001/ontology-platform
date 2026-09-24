// Package api 对外暴露复制槽接口；并发安全。依赖 slot。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"

	"ontology/slot"
	"ontology/wal"
)

type API struct {
	mu sync.Mutex
	s  *slot.Slot
}

func New(maxOpen int) *API { return &API{s: slot.New(maxOpen)} }

func (a *API) Append(recs ...wal.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.s.Append(recs...)
}
func (a *API) Confirm(lsn int64) error { a.mu.Lock(); defer a.mu.Unlock(); return a.s.Confirm(lsn) }
func (a *API) Restart()                { a.mu.Lock(); defer a.mu.Unlock(); a.s.Restart() }
func (a *API) Emitted() []slot.Txn     { a.mu.Lock(); defer a.mu.Unlock(); return a.s.Emitted() }
func (a *API) ConfirmedLSN() int64     { a.mu.Lock(); defer a.mu.Unlock(); return a.s.Confirmed() }
func (a *API) RestartLSN() int64       { a.mu.Lock(); defer a.mu.Unlock(); return a.s.RestartLSN() }

// SelfCheck 对内置随机操作序列核验四条不变量：朴素参照一致、可恢复、单调有序、失败不留痕。
func (a *API) SelfCheck() error {
	rng := rand.New(rand.NewSource(7))
	ap := New(1000)
	type nt struct {
		begin, commit int64
		open, aborted bool
	}
	var txns []nt
	txnIdx := map[string]int{}
	committed := map[string]slot.Txn{}
	openID, openChg := []string{}, map[string][]string{}
	confirmed, lastC, lastR, lsn, xidN := int64(5), int64(5), int64(5), int64(5), 0
	naiveRestart := func() int64 {
		r := confirmed
		for _, t := range txns {
			if !t.aborted && (t.open || t.commit > confirmed) && t.begin < r {
				r = t.begin
			}
		}
		return r
	}
	steady := func() error { // 单调有序：任意时刻成立
		c, r := ap.ConfirmedLSN(), ap.RestartLSN()
		if c < lastC || r < lastR || r > c {
			return fmt.Errorf("单调性破坏 c=%d r=%d", c, r)
		}
		lastC, lastR = c, r
		return nil
	}
	noTrace := func(bad func() error, want error) error { // 失败不留痕
		c0, r0, e0 := ap.ConfirmedLSN(), ap.RestartLSN(), ap.Emitted()
		if err := bad(); !errors.Is(err, want) {
			return fmt.Errorf("拒绝错误=%v 期望=%v", err, want)
		}
		if ap.ConfirmedLSN() != c0 || ap.RestartLSN() != r0 || !reflect.DeepEqual(ap.Emitted(), e0) {
			return errors.New("被拒操作改变了状态")
		}
		return nil
	}
	for round := 0; round < 300; round++ {
		var batch []wal.Record
		for i := 0; i < 1+rng.Intn(4); i++ {
			lsn += int64(1 + rng.Intn(9))
			if len(openID) < 8 && (len(openID) == 0 || rng.Intn(2) == 0) {
				xidN++
				x := fmt.Sprintf("T%d", xidN)
				batch = append(batch, wal.Record{LSN: lsn, Kind: wal.Begin, Xid: x})
				openID = append(openID, x)
				txnIdx[x] = len(txns)
				txns = append(txns, nt{begin: lsn, commit: -1, open: true})
				continue
			}
			j := rng.Intn(len(openID))
			x := openID[j]
			switch rng.Intn(3) {
			case 0:
				d := fmt.Sprintf("d%d", lsn)
				batch = append(batch, wal.Record{LSN: lsn, Kind: wal.Change, Xid: x, Data: d})
				openChg[x] = append(openChg[x], d)
			case 1:
				batch = append(batch, wal.Record{LSN: lsn, Kind: wal.Commit, Xid: x})
				txns[txnIdx[x]].open, txns[txnIdx[x]].commit = false, lsn
				committed[x] = slot.Txn{Xid: x, CommitLSN: lsn, Changes: openChg[x]}
				openID = append(openID[:j], openID[j+1:]...)
			default:
				batch = append(batch, wal.Record{LSN: lsn, Kind: wal.Abort, Xid: x})
				txns[txnIdx[x]].open, txns[txnIdx[x]].aborted = false, true
				openID = append(openID[:j], openID[j+1:]...)
			}
		}
		if err := ap.Append(batch...); err != nil {
			return err
		}
		if rng.Intn(3) == 0 { // 确认一个已发出事务，并在重算点比对朴素参照
			for _, t := range ap.Emitted() {
				if t.CommitLSN > confirmed {
					if err := ap.Confirm(t.CommitLSN); err != nil {
						return err
					}
					confirmed = t.CommitLSN
					if r := ap.RestartLSN(); r != naiveRestart() {
						return fmt.Errorf("restart=%d 朴素=%d", r, naiveRestart())
					}
					break
				}
			}
		}
		if rng.Intn(5) == 0 { // 重启并核验可恢复
			ap.Restart()
			var exp []slot.Txn
			for _, t := range committed {
				if t.CommitLSN > confirmed {
					exp = append(exp, t)
				}
			}
			sort.Slice(exp, func(i, j int) bool { return exp[i].CommitLSN < exp[j].CommitLSN })
			if !reflect.DeepEqual(ap.Emitted(), exp) {
				return fmt.Errorf("重启后发出集合不符 got=%v want=%v", ap.Emitted(), exp)
			}
		}
		if err := steady(); err != nil {
			return err
		}
		if err := noTrace(func() error { return ap.Confirm(confirmed - 1) }, slot.ErrBackward); err != nil {
			return err
		}
		if err := noTrace(func() error { return ap.Confirm(1 << 60) }, slot.ErrNotBoundary); err != nil {
			return err
		}
		if err := noTrace(func() error { return ap.Append(wal.Record{LSN: lsn, Kind: wal.Begin, Xid: "bad"}) }, wal.ErrLSNOrder); err != nil {
			return err
		}
	}
	return nil
}
