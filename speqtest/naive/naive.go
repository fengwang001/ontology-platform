// Package naive 是独立编写的朴素参考模型：
// 不依赖 speq 包，日历与业务规则全部另写一遍，
// 预警与设备附件判定均为 O(对象总数)/O(设备附件数) 的线性扫描。
// 用于与优化实现做随机操作序列差分对照。
package naive

import (
	"errors"
	"time"
)

// ---- 错误（码值与 speq 对齐，但独立定义） ----

type Code int

const (
	CodeInvalid Code = iota
	CodeRegression
	CodeNotFound
	CodeScrapped
	CodeState
	CodeCondition
)

type Err struct {
	Code Code
	Msg  string
}

func (e *Err) Error() string { return e.Msg }

type Reject int

const (
	RejectOK Reject = iota
	RejectSealed
	RejectDisabled
	RejectExpired
	RejectNoSV
	RejectAttach
)

type UseError struct {
	Reason Reject
	Attach string
	Detail string
}

func (e *UseError) Error() string { return e.Detail }

// ---- 领域枚举 ----

type Kind int

const (
	KDevice Kind = iota
	KSV
	KPG
)

type Result int

const (
	RPass Result = iota
	RCond
	RFail
)

type Category struct {
	Code      string
	Kind      Kind
	PeriodM   int
	WindowD   int
	MinUnseal int
	WarnLead  int
}

type O struct {
	ID, Cat    string
	Kind       Kind
	Scrap      bool
	Seal       bool
	Dis        bool
	Exp        int
	SealDate   int
	SealAnchor int
	Host       string
}

type Snap struct {
	ID, Cat          string
	Kind             Kind
	Scrap, Seal, Dis bool
	Exp              int
	Host             string
}

type Warn struct {
	ID   string
	Kind Kind
	Exp  int
	Sort int
	Trig []string
}

type Model struct {
	Cats map[string]Category
	Objs map[string]*O
	// 设备附件关系用普通列表存储，强制线性判定。
	HostOf map[string]string
	Last   int
}

func New() *Model {
	return &Model{
		Cats:   map[string]Category{},
		Objs:   map[string]*O{},
		HostOf: map[string]string{},
		Last:   -1,
	}
}

var epoch = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

func toDate(ord int) time.Time {
	return epoch.AddDate(0, 0, ord)
}

func addMonths(ord, months int) int {
	t := toDate(ord)
	y, m := t.Year(), int(t.Month())+months
	total := (y*12 + (m - 1))
	y, m = total/12, total%12+1
	first := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if day > last {
		day = last
	}
	res := time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC)
	return int(res.Sub(epoch).Hours() / 24)
}

var _ = errors.New
