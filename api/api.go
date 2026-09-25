// Package api 是对外门面：New/Apply/四类查询/SelfCheck。依赖 hist。
package api

import (
	"errors"

	"ontology/hist"
	"ontology/ver"
)

// 对外可判定的哨兵错误，互不相同。
var (
	ErrBadConfig          = errors.New("api: maxVersions must be >= 1")
	ErrEmptyKey           = hist.ErrEmptyKey
	ErrNonMonotonicIngest = hist.ErrNonMonotonicIngest
	ErrCapacity           = hist.ErrCapacity
	ErrNotFound           = ver.ErrNotFound
)

// System 是双时间戳版本历史系统，并发安全。
type System struct {
	s *hist.Store
}

// New 创建系统；maxVersions < 1 返回 ErrBadConfig。
func New(maxVersions int) (*System, error) {
	if maxVersions < 1 {
		return nil, ErrBadConfig
	}
	return &System{s: hist.New(maxVersions)}, nil
}

// Apply 追加一个版本，失败返回可判定哨兵错误且不留痕。
func (y *System) Apply(key, value string, ev, in int64) error {
	return y.s.Apply(key, value, ev, in)
}

// LatestEvent 返回该 key Ev 最大的版本（并列取 In 最大）。
func (y *System) LatestEvent(key string) (ver.Version, error) { return y.s.LatestEvent(key) }

// LatestIngest 返回该 key In 最大的版本。
func (y *System) LatestIngest(key string) (ver.Version, error) { return y.s.LatestIngest(key) }

// AtEvent 返回该 key Ev <= t 中 Ev 最大的版本（并列取 In 最大）。
func (y *System) AtEvent(key string, t int64) (ver.Version, error) { return y.s.AtEvent(key, t) }

// AtIngest 返回该 key In <= t 中 In 最大的版本。
func (y *System) AtIngest(key string, t int64) (ver.Version, error) { return y.s.AtIngest(key, t) }

// SelfCheck 用一组内置 Apply/查询序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	if err := ver.SelfCheck(); err != nil { // 不变量1(LatestEvent 部分) + O(1) 指针
		return err
	}
	y, err := New(8)
	if err != nil {
		return err
	}
	type rec struct {
		v      string
		ev, in int64
	}
	seq := []rec{{"A", 15, 1}, {"B", 25, 2}, {"C", 5, 3}, {"D", 20, 4}, {"E", -7, 5}, {"F", 25, 6}}
	var applied []rec
	naiveLE := func() ver.Version { // 朴素扫描：Ev 最大，并列取 In 最大
		b := applied[0]
		for _, r := range applied[1:] {
			if r.ev > b.ev || (r.ev == b.ev && r.in > b.in) {
				b = r
			}
		}
		return ver.Version{Value: b.v, Ev: b.ev, In: b.in}
	}
	prevEv := int64(-1) << 62
	for i, r := range seq {
		if err := y.Apply("K", r.v, r.ev, r.in); err != nil {
			return err
		}
		applied = append(applied, r)
		le, _ := y.LatestEvent("K")
		li, _ := y.LatestIngest("K")
		if le != naiveLE() || li.Value != r.v { // 不变量1：与朴素扫描一致
			return errors.New("api: 查询与朴素扫描不一致")
		}
		if le.Ev < prevEv { // 不变量2：LatestEvent 的 Ev 单调不减
			return errors.New("api: LatestEvent 的 Ev 回退")
		}
		prevEv = le.Ev
		ai, _ := y.AtIngest("K", r.in) // 不变量3：In 序列严格递增，AtIngest(当前In) 必为刚到达者
		if ai.In != r.in || (i > 0 && ai.In <= seq[i-1].in) {
			return errors.New("api: In 序列非严格递增")
		}
	}
	// 不变量4：四类失败错误可判定、互不相同，且被拒后状态不变。
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		return errors.New("api: 非法配置未返回 ErrBadConfig")
	}
	for i := 0; i < 2; i++ { // 把 K 打满到 maxVersions=8
		if err := y.Apply("K", "z", int64(100+i), int64(7+i)); err != nil {
			return err
		}
	}
	before, _ := y.LatestEvent("K")
	bads := []struct {
		op   func() error
		want error
	}{
		{func() error { return y.Apply("", "x", 1, 99) }, ErrEmptyKey},
		{func() error { return y.Apply("K", "x", 1, 3) }, ErrNonMonotonicIngest},
		{func() error { return y.Apply("K", "x", 1, 100) }, ErrCapacity},
	}
	for _, b := range bads {
		if err := b.op(); !errors.Is(err, b.want) {
			return errors.New("api: 失败操作未返回预期哨兵错误")
		}
	}
	if le, _ := y.LatestEvent("K"); le != before {
		return errors.New("api: 被拒操作改变了状态")
	}
	if li, err := y.LatestIngest("K"); err != nil || li.In != 8 { // 被拒后仍可正常查询
		return errors.New("api: 被拒后状态异常")
	}
	return nil
}
