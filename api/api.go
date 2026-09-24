// Package api 是水位线漂移/回拨检测器的对外入口。
// 它只依赖 wm（wm 再依赖 drift），依赖方向单向。
package api

import (
	"errors"

	"ontology/drift"
	"ontology/wm"
)

// 三类可判定的哨兵错误，两两不同，可用 errors.Is 判别。
var (
	// ErrInvalidThreshold：driftThreshold <= 0 或 rollbackTolerance < 0。
	ErrInvalidThreshold = errors.New("api: driftThreshold must be > 0 and rollbackTolerance must be >= 0")
	// ErrEmptySource：source 为空串。
	ErrEmptySource = wm.ErrEmptySource
	// ErrNegativeWatermark：w < 0。
	ErrNegativeWatermark = wm.ErrNegativeWatermark
)

// Detector 是并发安全的多源水位线检测器。
type Detector struct {
	mgr *wm.Manager
}

// New 构造检测器。参数非法时返回 ErrInvalidThreshold，且不产生任何状态。
func New(driftThreshold, rollbackTolerance int64) (*Detector, error) {
	if driftThreshold <= 0 || rollbackTolerance < 0 {
		return nil, ErrInvalidThreshold
	}
	return &Detector{mgr: wm.NewManager(driftThreshold, rollbackTolerance)}, nil
}

// Observe 对某源的一条水位线判类。source 为空或 w<0 时整体失败，
// 返回对应哨兵错误且不改变任何状态（last 与三计数器均不变）。
func (d *Detector) Observe(source string, w int64) (drift.Class, error) {
	// 校验全部前置：在触达任何状态之前拒绝，保证「失败不留痕」。
	if source == "" {
		return drift.Normal, ErrEmptySource
	}
	if w < 0 {
		return drift.Normal, ErrNegativeWatermark
	}
	return d.mgr.Observe(source, w), nil
}

// Counts 返回 drift/reorder/rollback 三类累计次数的一致快照。
func (d *Detector) Counts() (driftCount, reorderCount, rollbackCount int64) {
	return d.mgr.Counts()
}

// EightStepSequence 是第三节规定的内置序列（阈值 10/3）。
var EightStepSequence = []int64{100, 105, 120, 118, 117, 116, 130, 141}

// SelfCheck 在独立的内置状态上核验第二节四条不变量，不动接收者状态。
// 任一不成立返回描述性错误；全部成立返回 nil。
func (d *Detector) SelfCheck() error {
	m := wm.NewManager(10, 3)
	var nlast int64 // 朴素重算的 last
	seen := false
	var nd, nr, nrb, nonNormal int64
	prev := int64(0)
	for i, w := range EightStepSequence {
		// 朴素单遍重算（独立表述，刻意不调用被测实现）。
		var nc drift.Class
		switch {
		case !seen:
			nlast, seen, nc = w, true, drift.Normal
		case w > nlast+10:
			nlast, nc = w, drift.Drift
		case w >= nlast:
			nlast, nc = w, drift.Normal
		case nlast-w <= 3:
			nc = drift.Reorder
		default:
			nc = drift.Rollback
		}
		got := m.Observe("s", w)
		if got != nc { // 不变量 2：分类与朴素重算逐条一致。
			return errors.New("api self-check: class disagrees with naive recomputation")
		}
		cur, _ := m.Last("s")
		if i > 0 && cur < prev { // 不变量 1：last 只进不退。
			return errors.New("api self-check: last watermark decreased")
		}
		if cur != nlast {
			return errors.New("api self-check: last disagrees with naive recomputation")
		}
		prev = cur
		switch nc {
		case drift.Drift:
			nd++
			nonNormal++
		case drift.Reorder:
			nr++
			nonNormal++
		case drift.Rollback:
			nrb++
			nonNormal++
		}
	}
	cd, cr, crb := m.Counts() // 不变量 3：计数守恒。
	if cd != nd || cr != nr || crb != nrb || cd+cr+crb != nonNormal {
		return errors.New("api self-check: counters not conserved")
	}
	return selfCheckRejection() // 不变量 4：失败不留痕。
}

// selfCheckRejection 核验非法操作被整体拒绝且不留任何状态痕迹。
func selfCheckRejection() error {
	d2, err := New(10, 3)
	if err != nil {
		return errors.New("api self-check: valid New rejected")
	}
	if _, err := d2.Observe("s", 100); err != nil {
		return errors.New("api self-check: valid Observe rejected")
	}
	bcd, bcr, bcrb := d2.Counts()
	if _, e := d2.Observe("", 5); !errors.Is(e, ErrEmptySource) {
		return errors.New("api self-check: empty source not rejected")
	}
	if _, e := d2.Observe("s", -1); !errors.Is(e, ErrNegativeWatermark) {
		return errors.New("api self-check: negative watermark not rejected")
	}
	acd, acr, acrb := d2.Counts()
	if acd != bcd || acr != bcr || acrb != bcrb {
		return errors.New("api self-check: rejected Observe mutated counters")
	}
	// SelfCheck 与被测类型同包，可直接经非导出 mgr 读取 last，
	// 无需为此增加任何公开方法（公开接口仅限 New/Observe/Counts/SelfCheck）。
	if last, ok := d2.mgr.Last("s"); !ok || last != 100 {
		return errors.New("api self-check: rejected Observe mutated last")
	}
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidThreshold) {
		return errors.New("api self-check: driftThreshold<=0 not rejected")
	}
	if _, err := New(1, -1); !errors.Is(err, ErrInvalidThreshold) {
		return errors.New("api self-check: rollbackTolerance<0 not rejected")
	}
	return nil
}
