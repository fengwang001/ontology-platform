// Package api 是跨分区会话键归并的对外接口：New/Append/Close/Result/SelfCheck。
// 依赖方向：api -> mrg -> seg，api 不直接依赖 seg。
package api

import (
	"errors"

	"ontology/mrg"
)

// 四类可判定哨兵错误，互不相同；数值取自底层 mrg/seg 的同一哨兵。
var (
	ErrBadKey     = mrg.ErrBadKey     // 键非法：Sid 为空
	ErrInvalid    = mrg.ErrInvalid    // Seq<=0 或 Value 不在 [0,9]
	ErrConflict   = mrg.ErrConflict   // 同 Sid 同 Seq 的 Value 不同
	ErrIncomplete = mrg.ErrIncomplete // Close 有缺口或越界项
	ErrClosed     = mrg.ErrClosed     // 已关闭后 Append，或 Close 不同 N
)

// Event 是上游分区投来的一条事件；Sid 是唯一归并键。
type Event struct {
	Sid   string
	Seq   int
	Value int
}

// API 是归并器句柄，并发安全。零值不可用，用 New 创建。
type API struct {
	m *mrg.Mgr
}

// New 创建空归并器（状态仅在进程内存）。
func New() *API { return &API{m: mrg.New()} }

// Append 按 Sid 归并一条事件。任何拒绝都整体失败且不留痕。
func (a *API) Append(ev Event) error {
	return a.m.Append(mrg.Event{Sid: ev.Sid, Seq: ev.Seq, Value: ev.Value})
}

// Close 在 1..n 恰好齐备时冻结结果；语义见 mrg/seg。
func (a *API) Close(sid string, n int) error { return a.m.Close(sid, n) }

// Result 返回冻结结果；未关闭/未知会话 ok 为 false。
func (a *API) Result(sid string) (value int, ok bool, err error) {
	return a.m.Result(sid)
}

// SelfCheck 对一组内置操作序列核验第二节四条不变量；全部成立返回 nil。
func (a *API) SelfCheck() error {
	// 不变量1/2：乱序到达后 Close 的结果必须等于朴素批量重算，且冻结后不变。
	events := [][2]int{{2, 4}, {4, 8}, {1, 1}, {3, 5}} // (seq,value)，乱序到达
	for _, ev := range events {
		if err := a.Append(Event{Sid: "chk", Seq: ev[0], Value: ev[1]}); err != nil {
			return err
		}
	}
	want := 0 // 朴素批量重算：把全部事件按 Seq 升序拼接
	for seq := 1; seq <= 4; seq++ {
		for _, ev := range events {
			if ev[0] == seq {
				want = want*10 + ev[1]
			}
		}
	}
	if err := a.Close("chk", 4); err != nil {
		return err
	}
	v, closed, err := a.Result("chk")
	if err != nil || !closed || v != want {
		return errors.New("api: selfcheck batch/fold mismatch")
	}
	if err := a.Append(Event{Sid: "chk", Seq: 5, Value: 9}); err != ErrClosed {
		return errors.New("api: selfcheck closed append")
	}
	if err := a.Close("chk", 5); err != ErrClosed {
		return errors.New("api: selfcheck close diff N")
	}
	if v2, _, _ := a.Result("chk"); v2 != want {
		return errors.New("api: selfcheck frozen result changed")
	}
	// 不变量3：同值幂等、异值冲突。
	if err := a.Append(Event{Sid: "u", Seq: 1, Value: 7}); err != nil {
		return err
	}
	if err := a.Append(Event{Sid: "u", Seq: 1, Value: 7}); err != nil {
		return errors.New("api: selfcheck idempotency")
	}
	if err := a.Append(Event{Sid: "u", Seq: 1, Value: 8}); err != ErrConflict {
		return errors.New("api: selfcheck conflict")
	}
	// 不变量4：非法输入被整体拒绝；拒绝不留痕。补齐 Seq2=3 后 Close(2) 成功且
	// 结果为 73，即证明 Seq1 仍是 7、冲突值未写入、缺口/越界计数未被污染、会话仍可用。
	if a.Append(Event{Sid: "", Seq: 1, Value: 1}) != ErrBadKey {
		return errors.New("api: selfcheck bad key")
	}
	if a.Append(Event{Sid: "u", Seq: 0, Value: 1}) != ErrInvalid {
		return errors.New("api: selfcheck bad seq")
	}
	if a.Append(Event{Sid: "u", Seq: 2, Value: 10}) != ErrInvalid {
		return errors.New("api: selfcheck bad value")
	}
	if a.Close("u", 2) != ErrIncomplete {
		return errors.New("api: selfcheck incomplete")
	}
	if err := a.Append(Event{Sid: "u", Seq: 2, Value: 3}); err != nil {
		return errors.New("api: selfcheck session unusable after reject")
	}
	if err := a.Close("u", 2); err != nil {
		return err
	}
	if v, closed, _ := a.Result("u"); !closed || v != 73 {
		return errors.New("api: selfcheck rejected op left trace")
	}
	return nil
}
