package registry

import (
	"fmt"
	"io"
)

// Package registry 实现绿电证书登记簿：
// 按发电设施的分期计量核发整数单位证书，支持整批转让、用电期注销声明、
// 计量回溯修正引起的撤销，以及设施资格的生效与终止。
//
// 核心类型为 Registry；NaiveRegistry 是按同一份规则独立实现的朴素模型，
// 用于随机操作序列下的差分测试。所有方法可并发调用，拒绝以 *Error 返回
// 可区分的错误类别，错误判定次序固定（见 ErrorCode）。
//
// 关键语义：
//   - 资格区间为 [生效期, 终止期)，终止期 0 表示开放；
//   - 证书序号全簿单调连续，被拒绝的核发不占号；
//   - 不足一单位的余量按“最近操作期并入”口径累计；
//   - 批操作（转让/注销）全有或全无，按序号升序报首个失败项；
//   - 撤销优先持证书（序号降序），再已注销证书（注销时刻降序），
//     后者产生 EventDeclarationInvalidated 并回补有效注销量。
//
// New 创建登记簿。零值字段采用默认配置。
func New(cfg Config, trace io.Writer) *Registry {
	if cfg.UnitQty <= 0 {
		cfg.UnitQty = 1
	}
	if cfg.MaxAgePeriods < 0 {
		cfg.MaxAgePeriods = 0
	}
	if cfg.MinPeriod == 0 && cfg.MaxPeriod == 0 {
		cfg.MinPeriod = 1
		cfg.MaxPeriod = 1_000_000
	}
	return &Registry{
		cfg:   cfg,
		certs: map[int64]*Cert{},
		facs:  map[string]*facility{},
		cons:  map[string]*consumer{},
		trace: trace,
	}
}

func (r *Registry) validPeriod(p int64) bool {
	return p >= r.cfg.MinPeriod && p <= r.cfg.MaxPeriod
}

func (r *Registry) getFacility(id string) *facility {
	return r.facs[id]
}

func (r *Registry) getConsumer(id string) *consumer {
	c := r.cons[id]
	if c == nil {
		c = &consumer{periods: map[int64]*conPeriod{}}
		r.cons[id] = c
	}
	return c
}

// qualified 判断发电期是否落在资格区间 [start,end)（end=0 表示开放）。
func (f *facility) qualified(p int64) bool {
	if p < f.start {
		return false
	}
	if f.end != 0 && p >= f.end {
		return false
	}
	return true
}

func (r *Registry) emit(e Event) {
	r.events = append(r.events, e)
}

func (r *Registry) logf(format string, args ...any) {
	if r.trace != nil {
		fmt.Fprintf(r.trace, format+"\n", args...)
	}
}

// Cert 查询证书快照（不存在返回 nil）。
func (r *Registry) Cert(serial int64) *Cert {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.certs[serial]
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// CertCount 返回登记簿内证书总数。
func (r *Registry) CertCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.certs)
}

// LastSerial 返回已分配最大序号。
func (r *Registry) LastSerial() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.serial
}
