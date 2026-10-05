// Package registry 维护数据集、生命周期阶段与上下游依赖。
package registry

import "sort"

// Phase 为数据集生命周期阶段，除 Undeprecate 外只进不退。
type Phase int

const (
	Active Phase = iota
	Deprecated
	Brownout
	Retired
)

func (p Phase) String() string {
	switch p {
	case Active:
		return "Active"
	case Deprecated:
		return "Deprecated"
	case Brownout:
		return "Brownout"
	case Retired:
		return "Retired"
	}
	return "Unknown"
}

// Dataset 为一个已登记数据集的全部可变状态。
type Dataset struct {
	Name       string
	Phase      Phase
	Parents    []string
	Children   []string
	SunsetAt   int64
	BrownStart int64
	ExtCount   int
	ExtTotal   int64
}

// Registry 为数据集登记表，非并发安全，由上层串行化。
type Registry struct {
	byName map[string]*Dataset
}

func New() *Registry {
	return &Registry{byName: make(map[string]*Dataset)}
}

// Get 按名查找数据集。
func (r *Registry) Get(name string) (*Dataset, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// Add 登记新数据集（调用方保证参数合法、上游均已登记）。
func (r *Registry) Add(name string, parents []string) *Dataset {
	d := &Dataset{
		Name:    name,
		Phase:   Active,
		Parents: append([]string(nil), parents...),
	}
	r.byName[name] = d
	for _, p := range parents {
		parent := r.byName[p]
		parent.Children = append(parent.Children, name)
	}
	return d
}

// Impact 返回 name 的全部传递下游中尚未 Retired 者，按名字节序升序。
func (r *Registry) Impact(name string) []string {
	seen := make(map[string]bool)
	stack := append([]string(nil), r.byName[name].Children...)
	out := []string{}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		d := r.byName[n]
		if d.Phase != Retired {
			out = append(out, n)
		}
		stack = append(stack, d.Children...)
	}
	sort.Strings(out)
	return out
}
