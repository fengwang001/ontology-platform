// Package budget 提供数据集与分析师的无锁账目原语。
package budget

import "errors"

// MaxBudget 是单个数据集终身额度或分析师窗口额度的上界。
const MaxBudget int64 = 1_000_000_000_000

// ErrInvalidArgument 表示登记参数非法（空名或额度越界）。
var ErrInvalidArgument = errors.New("budget: invalid argument")

// Dataset 记录一个数据集的终身账目：used 只增，resv 为在途预留。
type Dataset struct {
	Name string
	B    int64 // 终身额度
	Used int64
	Resv int64
}

// Analyst 记录一个分析师各窗口的占用。
type Analyst struct {
	Name string
	B    int64 // 每窗口额度
	Win  map[int64]int64
}

// NewDataset 创建数据集账目。b 必须在 [1, MaxBudget]。
func NewDataset(name string, b int64) (*Dataset, error) {
	if name == "" || b < 1 || b > MaxBudget {
		return nil, ErrInvalidArgument
	}
	return &Dataset{Name: name, B: b}, nil
}

// NewAnalyst 创建分析师账目。b 必须在 [1, MaxBudget]。
func NewAnalyst(name string, b int64) (*Analyst, error) {
	if name == "" || b < 1 || b > MaxBudget {
		return nil, ErrInvalidArgument
	}
	return &Analyst{Name: name, B: b, Win: map[int64]int64{}}, nil
}

// Remaining 返回数据集剩余额度。
func (d *Dataset) Remaining() int64 { return d.B - d.Used - d.Resv }

// CanReserve 报告 cost 是否可再预留（恰等允许）。
func (d *Dataset) CanReserve(cost int64) bool { return d.Used+d.Resv+cost <= d.B }

// Reserve 增加在途预留。
func (d *Dataset) Reserve(cost int64) { d.Resv += cost }

// Settle 结算一次查询：释放预留 cost，按 actual 计入已用。
func (d *Dataset) Settle(cost, actual int64) {
	d.Resv -= cost
	d.Used += actual
}

// Window 取分析师某窗口占用（不存在为 0）。
func (a *Analyst) Window(w int64) int64 { return a.Win[w] }

// WindowRemaining 返回分析师某窗口剩余额度。
func (a *Analyst) WindowRemaining(w int64) int64 { return a.B - a.Win[w] }

// CanWindow 报告在窗口 w 再占 cost 是否不超额度（恰等允许）。
func (a *Analyst) CanWindow(w, cost int64) bool { return a.Win[w]+cost <= a.B }

// AddWindow 在窗口 w 增加占用。
func (a *Analyst) AddWindow(w, cost int64) { a.Win[w] += cost }

// SetWindow 把窗口 w 的占用中由 cost 改为 actual（结算差额退还）。
func (a *Analyst) SetWindow(w, cost, actual int64) { a.Win[w] += actual - cost }
