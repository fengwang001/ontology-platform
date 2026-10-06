package settlement

import (
	"fmt"
	"time"
)

// Month 表示公历月份；比较与运算一律走连续月序号 index()，避免跨年分支。
type Month struct {
	Year  int
	Month int // 1..12
}

func validMonth(y, mo int) bool { return mo >= 1 && mo <= 12 && y >= 1 }

// parseMonth 严格解析 YYYY-MM；这是唯一接受月份字符串的入口。
func parseMonth(s string) (Month, error) {
	if len(s) != 7 || s[4] != '-' {
		return Month{}, fmt.Errorf("%w: 生效月份格式错", ErrInvalid)
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return Month{}, fmt.Errorf("%w: 生效月份格式错", ErrInvalid)
		}
	}
	if s[5] < '0' || s[5] > '9' || s[6] < '0' || s[6] > '9' {
		return Month{}, fmt.Errorf("%w: 生效月份格式错", ErrInvalid)
	}
	y := int(s[0]-'0')*1000 + int(s[1]-'0')*100 + int(s[2]-'0')*10 + int(s[3]-'0')
	mo := int(s[5]-'0')*10 + int(s[6]-'0')
	if !validMonth(y, mo) {
		return Month{}, fmt.Errorf("%w: 生效月份格式错", ErrInvalid)
	}
	return Month{y, mo}, nil
}

// MustMonth 供测试与字面量构造使用。
func MustMonth(s string) Month {
	m, err := parseMonth(s)
	if err != nil {
		panic(err)
	}
	return m
}

func (m Month) valid() bool { return validMonth(m.Year, m.Month) }

// index 返回从公元 1 年 1 月起的连续月序号。
func (m Month) index() int { return m.Year*12 + (m.Month - 1) }

func monthFromIndex(i int) Month { return Month{Year: i / 12, Month: (i % 12) + 1} }

func (m Month) add(n int) Month { return monthFromIndex(m.index() + n) }

func (m Month) before(o Month) bool { return m.index() < o.index() }

func (m Month) equal(o Month) bool { return m.Year == o.Year && m.Month == o.Month }

func (m Month) String() string { return fmt.Sprintf("%04d-%02d", m.Year, m.Month) }

func monthStartUTC(m Month) time.Time {
	return time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, time.UTC)
}

// Interval 是计量间隔起点。引擎只在 UTC 下解释时间，保证可复现。
type Interval = time.Time

func utcUnix(t time.Time) int64 { return t.UTC().Unix() }

// aligned 判断 t 是否落在以 epoch 为起点、intervalSeconds 为步长的计量网格上。
func aligned(t time.Time, epochUnix, intervalSeconds int64) bool {
	u := utcUnix(t)
	if t.UTC().Nanosecond() != 0 || u < epochUnix {
		return false
	}
	return (u-epochUnix)%intervalSeconds == 0
}

// monthOf 返回某时刻在 UTC 下所属月份。
func monthOf(t time.Time) Month {
	u := t.UTC()
	return Month{u.Year(), int(u.Month())}
}

// ErrorKind 对应固定拒绝次序：非法 > 已封账 > 顺序错误 > 数据缺失。
type ErrorKind int

const (
	KindInvalid ErrorKind = iota + 1
	KindClosed
	KindOrder
	KindMissing
)

// ErrInvalid 是所有参数类错误的哨兵，错误文本携带具体原因。
var (
	ErrInvalid = fmt.Errorf("参数非法")
	ErrClosed  = fmt.Errorf("月份已封账")
	ErrOrder   = fmt.Errorf("顺序错误")
	ErrMissing = fmt.Errorf("数据缺失")
)

// SettlementError 让调用方可按 Kind 区分错误；数据缺失时带最早缺失间隔。
type SettlementError struct {
	Kind    ErrorKind
	Op      string
	Reason  string
	Missing Interval
}

func (e *SettlementError) Error() string {
	if e.Kind == KindMissing && !e.Missing.IsZero() {
		return fmt.Sprintf("数据缺失: 最早缺失间隔 %s (%s)", e.Missing.UTC().Format(time.RFC3339), e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.sentinel(), e.Reason)
}

func (e *SettlementError) sentinel() error {
	switch e.Kind {
	case KindInvalid:
		return ErrInvalid
	case KindClosed:
		return ErrClosed
	case KindOrder:
		return ErrOrder
	default:
		return ErrMissing
	}
}

// Unwrap 支持 errors.Is(err, settlement.ErrClosed) 等判定。
func (e *SettlementError) Unwrap() error { return e.sentinel() }

func errInvalid(op, reason string) error {
	return &SettlementError{Kind: KindInvalid, Op: op, Reason: reason}
}
