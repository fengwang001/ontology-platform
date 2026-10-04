package vaxrule

import (
	"errors"
	"sort"
	"sync"
)

// Grace 为年龄与间隔的固定宽限（日）；LiveGap 为不同活疫苗系列的最小间隔（日）。
const (
	Grace   = 4
	LiveGap = 28
)

// InvalidReason 是一条记录被判无效的原因。
type InvalidReason int

const (
	ReasonNone     InvalidReason = iota // 有效或多余
	ReasonAge                           // 年龄不足
	ReasonInterval                      // 与上一有效剂间隔不足
	ReasonRedose                        // 距最近无效记录不足 R
	ReasonLive                          // 与另一活疫苗系列间隔不足 28 日
)

// Series 描述一个疫苗系列的规则。
type Series struct {
	Name   string
	Live   bool
	N      int
	MinAge []int
	MinInt []int // 下标 k（1 起）；MinInt[1] 无意义
	R      int
}

var (
	ErrInvalid = errors.New("vaxrule: invalid argument")
	ErrExists  = errors.New("vaxrule: series already exists")
)

const (
	maxDose = 6
	maxVal  = 10000
)

// Catalog 保存全部系列定义。
type Catalog struct {
	mu     sync.RWMutex
	series map[string]*Series
}

func NewCatalog() *Catalog { return &Catalog{series: map[string]*Series{}} }

func (c *Catalog) AddSeries(name string, live bool, n int, minAge, minInt []int, r int) error {
	if name == "" || n < 1 || n > maxDose || len(minAge) != n || len(minInt) != n {
		return ErrInvalid
	}
	for k := 0; k < n; k++ {
		if minAge[k] < 0 || minAge[k] > maxVal {
			return ErrInvalid
		}
	}
	for k := 1; k < n; k++ {
		if minInt[k] < 0 || minInt[k] > maxVal {
			return ErrInvalid
		}
	}
	if r < 0 || r > maxVal {
		return ErrInvalid
	}
	s := &Series{
		Name:   name,
		Live:   live,
		N:      n,
		MinAge: append([]int(nil), minAge...),
		MinInt: append([]int(nil), minInt...),
		R:      r,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.series[name]; ok {
		return ErrExists
	}
	c.series[name] = s
	return nil
}

func (c *Catalog) Get(name string) (*Series, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.series[name]
	return s, ok
}

// Names 返回全部系列名（字节序），便于稳定遍历。
func (c *Catalog) Names() []string {
	c.mu.RLock()
	names := make([]string, 0, len(c.series))
	for name := range c.series {
		names = append(names, name)
	}
	c.mu.RUnlock()
	sort.Strings(names)
	return names
}

// Check 对一条候选记录（剂号 k，1 起，k<=n）按题面次序做四类无效检查。
// lastValidDate 为上一有效剂日期（k==1 时忽略）；lastRecord* 为该系列最近一条记录。
// liveConflict 表示是否存在另一活疫苗系列记录落在 (d-28, d)。
func (s *Series) Check(birth, d, k int, lastValidDate int, hasLastRecord bool, lastRecordDate int, lastRecordValid bool, liveConflict bool) InvalidReason {
	ki := k - 1
	if d-birth < s.MinAge[ki]-Grace {
		return ReasonAge
	}
	if k > 1 && d-lastValidDate < s.MinInt[ki]-Grace {
		return ReasonInterval
	}
	if hasLastRecord && !lastRecordValid && d-lastRecordDate < s.R {
		return ReasonRedose
	}
	if s.Live && liveConflict {
		return ReasonLive
	}
	return ReasonNone
}
