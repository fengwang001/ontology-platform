// Package api 是写者优先读写锁的对外接口，仅依赖 lock。
package api

import (
	"errors"
	"strconv"
	"strings"

	"ontology/lock"
)

// 重导出三类可判定哨兵错误，互不相同。
var (
	ErrEmptyID       = lock.ErrEmptyID
	ErrNotHeld       = lock.ErrNotHeld
	ErrDoubleRelease = lock.ErrDoubleRelease
)

// Lock 是对外的写者优先读写锁。
type Lock struct{ l *lock.L }

// New 创建一把空锁。
func New() *Lock { return &Lock{l: lock.New()} }

func (x *Lock) AcquireRead(id string) error  { return x.l.AcquireRead(id) }
func (x *Lock) AcquireWrite(id string) error { return x.l.AcquireWrite(id) }
func (x *Lock) ReleaseRead(id string) error  { return x.l.ReleaseRead(id) }
func (x *Lock) ReleaseWrite(id string) error { return x.l.ReleaseWrite(id) }
func (x *Lock) Readers() []string            { return x.l.Readers() }
func (x *Lock) Writer() string               { return x.l.Writer() }

// snap 返回等待队列的可读表示。
func snap(q []string) string { return strings.Join(q, ",") }

// SelfCheck 在内部锁上回放内置八步序列与故障用例，核验：
// 互斥、写者优先、与朴素手推一致、失败不留痕，以及 O(1) 标志判定。
// 任一不符返回可判定的非 nil 错误。
func (x *Lock) SelfCheck() error {
	l := lock.New()
	// 八步：write/rel 标记动作，grant 为当场是否放行，后三列为期望状态。
	type row struct {
		write, rel bool
		id         string
		grant      bool
		readers    string
		writer     string
		queue      string
	}
	rows := []row{
		{false, false, "R1", true, "R1", "", ""},
		{false, false, "R2", true, "R1,R2", "", ""},
		{true, false, "W1", false, "R1,R2", "", "W1W"},
		{false, false, "R3", false, "R1,R2", "", "W1W,R3R"},
		{false, true, "R1", false, "R2", "", "W1W,R3R"},
		{false, true, "R2", false, "", "W1", "R3R"},
		{false, false, "R4", false, "", "W1", "R3R,R4R"},
		{true, true, "W1", false, "R3,R4", "", ""},
	}
	for i, r := range rows {
		var g bool
		var err error
		if r.rel {
			if r.write {
				err = l.ReleaseWrite(r.id)
			} else {
				err = l.ReleaseRead(r.id)
			}
		} else {
			g, err = l.Try(r.id, r.write)
		}
		if err != nil {
			return err
		}
		if !r.rel && g != r.grant {
			return errors.New("selfcheck: grant mismatch at step " + strconv.Itoa(i+1))
		}
		rs := strings.Join(l.Readers(), ",")
		if rs != r.readers || l.Writer() != r.writer || snap(l.Waiting()) != r.queue {
			return errors.New("selfcheck: state mismatch at step " + strconv.Itoa(i+1))
		}
		if l.Writer() != "" && len(l.Readers()) != 0 { // 不变量1：互斥
			return errors.New("selfcheck: mutual exclusion violated at step " + strconv.Itoa(i+1))
		}
	}
	_ = l.ReleaseRead("R3")
	_ = l.ReleaseRead("R4")
	// 不变量4：三类拒绝互不相同且不留痕。
	if err := checkRejections(l); err != nil {
		return err
	}
	if !lock.FlagDecisionO1() { // O(1) 标志判定，不读计数器数值
		return errors.New("selfcheck: writer-preference decision is not O(1)")
	}
	return nil
}

// checkRejections 核验空 ID/未持有/重复释放三类错误互异、被拒后状态不变、仍可用。
func checkRejections(l *lock.L) error {
	before := snap(l.Waiting()) + "|" + strings.Join(l.Readers(), ",") + "|" + l.Writer()
	if _, err := l.Try("", false); !errors.Is(err, ErrEmptyID) {
		return errors.New("selfcheck: empty id not rejected")
	}
	if err := l.ReleaseRead("ghost"); !errors.Is(err, ErrNotHeld) {
		return errors.New("selfcheck: not-held not rejected")
	}
	if _, err := l.Try("Z", false); err != nil {
		return err
	}
	if err := l.ReleaseRead("Z"); err != nil {
		return err
	}
	if err := l.ReleaseRead("Z"); !errors.Is(err, ErrDoubleRelease) {
		return errors.New("selfcheck: double release not rejected")
	}
	after := snap(l.Waiting()) + "|" + strings.Join(l.Readers(), ",") + "|" + l.Writer()
	if before != after {
		return errors.New("selfcheck: rejected operation changed state")
	}
	if g, err := l.Try("Q", false); err != nil || !g {
		return errors.New("selfcheck: lock unusable after rejection")
	}
	_ = l.ReleaseRead("Q")
	return nil
}
