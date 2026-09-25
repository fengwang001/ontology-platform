// Package api 是对外门面：New/Bucket/Feed/Count/SelfCheck，依赖 bkt。
package api

import (
	"errors"
	"time"

	"ontology/bkt"
	"ontology/tzone"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadZone    = tzone.ErrBadZone
	ErrNegativeTS = bkt.ErrNegativeTS
	ErrEmptyKey   = bkt.ErrEmptyKey
)

// Event 是对外的事件类型。
type Event = bkt.Event

// Service 是分桶服务的对外句柄。
type Service struct {
	b   *bkt.Bucketer
	loc *time.Location
}

// New 以 IANA 时区名创建服务；名字非法返回 ErrBadZone，不留任何状态。
func New(zoneName string) (*Service, error) {
	z, err := tzone.Load(zoneName)
	if err != nil {
		return nil, err
	}
	return &Service{b: bkt.New(z), loc: z.Location()}, nil
}

// Bucket 返回 ts 的本地日历日期；ts 为负返回 ErrNegativeTS。
func (s *Service) Bucket(ts int64) (string, error) {
	return s.b.Bucket(ts)
}

// Feed 整体校验并归入事件；任一非法则整体失败、状态不变。
func (s *Service) Feed(events []Event) error {
	return s.b.Feed(events)
}

// Count 返回某桶的事件数，O(1) 查表。
func (s *Service) Count(date string) int {
	return s.b.Count(date)
}

// selfCheckEvents 是内置核验事件（跨冬/夏与两个 DST 转换日）。
var selfCheckEvents = []Event{
	{EventTime: 1768451400, Key: "e1"},
	{EventTime: 1784089800, Key: "e2"},
	{EventTime: 1772944200, Key: "e3"},
	{EventTime: 1793507400, Key: "e4"},
	{EventTime: 1773030600, Key: "e5"},
}

// errSelfCheck 表示自检发现不变量被破坏。
var errSelfCheck = errors.New("api: self-check failed")

// SelfCheck 对内置事件核验四条不变量，全部通过返回 nil。
func (s *Service) SelfCheck() error {
	// 不变量 1：与 stdlib 朴素参照逐事件一致（2026 全年逐小时扫描）。
	ref := func(ts int64) string { return time.Unix(ts, 0).In(s.loc).Format("2006-01-02") }
	const start, end, step = 1767225600, 1767225600 + 366*86400, 3600
	prev := ""
	for ts := int64(start); ts < end; ts += step {
		d, err := s.Bucket(ts)
		if err != nil || d != ref(ts) {
			return errSelfCheck
		}
		// 不变量 2：日期字典序单调不减。
		if prev != "" && d < prev {
			return errSelfCheck
		}
		prev = d
	}
	// 不变量 3：本地午夜边界，含 23h/25h 的 DST 转换日。
	for day := time.Date(2026, 1, 1, 0, 0, 0, 0, s.loc); day.Year() == 2026; day = day.AddDate(0, 0, 1) {
		m := day.Unix()
		if d, _ := s.Bucket(m); d != ref(m) {
			return errSelfCheck
		}
		if d, _ := s.Bucket(m - 1); d != ref(m-1) || d == ref(m) {
			return errSelfCheck
		}
	}
	// 不变量 4：失败不留痕——非法 Feed 后计数不变。
	fresh, err := New(s.loc.String())
	if err != nil {
		return err
	}
	if err := fresh.Feed(selfCheckEvents); err != nil {
		return err
	}
	before := fresh.Count("2026-01-14")
	if fresh.Feed([]Event{{EventTime: -1, Key: "x"}}) != ErrNegativeTS {
		return errSelfCheck
	}
	if fresh.Feed([]Event{{EventTime: 1768451400, Key: ""}}) != ErrEmptyKey {
		return errSelfCheck
	}
	if _, err := New("Not/AZone"); err != ErrBadZone {
		return errSelfCheck
	}
	if fresh.Count("2026-01-14") != before {
		return errSelfCheck
	}
	return nil
}
