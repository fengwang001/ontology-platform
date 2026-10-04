package registry

import (
	"errors"
	"sort"
)

// Phase 是数据集所处的生命周期阶段，阶段除 Undeprecate 外只进不退。
type Phase int

const (
	Active Phase = iota
	Deprecated
	Brownout
	Retired
)

func (p Phase) String() string { return phaseNames[p] }

var phaseNames = [...]string{"Active", "Deprecated", "Brownout", "Retired"}

// ErrExists 表示数据集名已登记。
var ErrExists = errors.New("registry: dataset already exists")

// ErrMissingParent 表示上游中有尚未登记的数据集。
var ErrMissingParent = errors.New("registry: parent dataset does not exist")

// ErrTooManyParents 表示上游数量超过 8。
var ErrTooManyParents = errors.New("registry: more than 8 parents")

// ErrEmptyName 表示数据集名为空字符串。
var ErrEmptyName = errors.New("registry: empty dataset name")

// Dataset 是单个数据集的注册表记录。
type Dataset struct {
	Name       string
	Parents    []string
	Children   []string
	Phase      Phase
	SunsetAt   int64
	BrownStart int64
	ExtendCnt  int
	ExtendSum  int64
}

// Registry 保存全部数据集及其派生依赖图。非并发安全，由 sunset 加锁调用。
type Registry struct {
	datasets map[string]*Dataset
}

// New 创建空注册表。
func New() *Registry { return &Registry{datasets: make(map[string]*Dataset)} }

// Add 登记新数据集。父级必须全部已登记，名字不得重复。
func (r *Registry) Add(name string, parents []string) error {
	if name == "" {
		return ErrEmptyName
	}
	if _, ok := r.datasets[name]; ok {
		return ErrExists
	}
	if len(parents) > 8 {
		return ErrTooManyParents
	}
	for _, p := range parents {
		if _, ok := r.datasets[p]; !ok {
			return ErrMissingParent
		}
	}
	ps := append([]string(nil), parents...)
	d := &Dataset{Name: name, Parents: ps, Phase: Active}
	r.datasets[name] = d
	for _, p := range ps {
		r.datasets[p].Children = append(r.datasets[p].Children, name)
	}
	return nil
}

// Get 返回数据集记录（同一指针）与是否存在。
func (r *Registry) Get(name string) (*Dataset, bool) {
	d, ok := r.datasets[name]
	return d, ok
}

// AffectedDownstream 返回 name 的全部传递下游中尚未 Retired 者，按名字节序升序。
func (r *Registry) AffectedDownstream(name string) []string {
	root, ok := r.datasets[name]
	if !ok {
		return nil
	}
	visited := map[string]bool{root.Name: true}
	var out []string
	var walk func(d *Dataset)
	walk = func(d *Dataset) {
		for _, ch := range d.Children {
			if visited[ch] {
				continue
			}
			visited[ch] = true
			c := r.datasets[ch]
			if c.Phase != Retired {
				out = append(out, ch)
			}
			walk(c)
		}
	}
	walk(root)
	sort.Strings(out)
	return out
}

// UnretiredChildren 返回 name 的直接下游中尚未 Retired 者，按名字节序升序。
func (r *Registry) UnretiredChildren(name string) []string {
	d, ok := r.datasets[name]
	if !ok {
		return nil
	}
	var out []string
	for _, ch := range d.Children {
		if r.datasets[ch].Phase != Retired {
			out = append(out, ch)
		}
	}
	sort.Strings(out)
	return out
}
