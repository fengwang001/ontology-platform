// Package matrix 展开构建矩阵：轴的笛卡尔积，依次应用 exclude 与 include 规则，
// 产出确定次序的作业列表。相同配置的展开结果逐字节相同。
package matrix

import (
	"errors"
	"fmt"
	"slices"
)

// 错误哨兵，均可用 errors.Is 区分。
var (
	ErrInvalid  = errors.New("matrix: invalid argument") // 参数非法
	ErrTooLarge = errors.New("matrix: matrix too large") // 矩阵过大
	ErrEmpty    = errors.New("matrix: empty matrix")     // 空矩阵
)

const (
	maxAxes   = 5
	maxValues = 10
	maxRules  = 64
	maxKey    = 32
	maxValue  = 64
	// MaxJobs 为最终组合数上限。
	MaxJobs = 256
)

// Axis 是一条有序的轴：键加互异值列表。
type Axis struct {
	Key    string
	Values []string
}

// Config 为矩阵展开配置。
type Config struct {
	Axes    []Axis
	Exclude []map[string]string
	Include []map[string]string
}

// Pair 为一个键值对。
type Pair struct {
	Key   string
	Value string
}

// Job 为展开后的一个作业：键值对按确定次序排列（轴键按轴声明序，
// 附加键按首次写入序），Experimental 表示是否为试验性作业。
type Job struct {
	Pairs        []Pair
	Experimental bool
}

// Get 返回作业中键 key 的值。
func (j Job) Get(key string) (string, bool) {
	for _, p := range j.Pairs {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// Matrix 为展开结果，Jobs 按最终次序编号 0 到 n-1。
type Matrix struct {
	Jobs []Job
}

// New 校验配置并展开矩阵。拒绝次序：参数非法 > 矩阵过大 > 空矩阵。
func New(cfg Config) (*Matrix, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	jobs := expand(cfg)
	if len(jobs) > MaxJobs {
		return nil, fmt.Errorf("%w: %d jobs", ErrTooLarge, len(jobs))
	}
	if len(jobs) == 0 {
		return nil, ErrEmpty
	}
	return &Matrix{Jobs: jobs}, nil
}

func validKey(key string) bool { return len(key) >= 1 && len(key) <= maxKey }

func validValue(value string) bool { return len(value) <= maxValue }

func validate(cfg Config) error {
	if len(cfg.Axes) < 1 || len(cfg.Axes) > maxAxes {
		return fmt.Errorf("%w: axes count %d", ErrInvalid, len(cfg.Axes))
	}
	isAxis := make(map[string]bool, len(cfg.Axes))
	for _, ax := range cfg.Axes {
		if !validKey(ax.Key) {
			return fmt.Errorf("%w: axis key %q", ErrInvalid, ax.Key)
		}
		if isAxis[ax.Key] {
			return fmt.Errorf("%w: duplicate axis key %q", ErrInvalid, ax.Key)
		}
		isAxis[ax.Key] = true
		if len(ax.Values) < 1 || len(ax.Values) > maxValues {
			return fmt.Errorf("%w: axis %q values count %d", ErrInvalid, ax.Key, len(ax.Values))
		}
		seen := make(map[string]bool, len(ax.Values))
		for _, v := range ax.Values {
			if !validValue(v) {
				return fmt.Errorf("%w: axis %q value too long", ErrInvalid, ax.Key)
			}
			if seen[v] {
				return fmt.Errorf("%w: axis %q duplicate value %q", ErrInvalid, ax.Key, v)
			}
			seen[v] = true
		}
	}
	if len(cfg.Exclude) > maxRules {
		return fmt.Errorf("%w: exclude count %d", ErrInvalid, len(cfg.Exclude))
	}
	for _, ex := range cfg.Exclude {
		if len(ex) == 0 {
			return fmt.Errorf("%w: empty exclude item", ErrInvalid)
		}
		for k, v := range ex {
			if !validKey(k) || !validValue(v) {
				return fmt.Errorf("%w: exclude item pair %q", ErrInvalid, k)
			}
			if !isAxis[k] {
				return fmt.Errorf("%w: exclude key %q is not an axis", ErrInvalid, k)
			}
		}
	}
	if len(cfg.Include) > maxRules {
		return fmt.Errorf("%w: include count %d", ErrInvalid, len(cfg.Include))
	}
	for _, inc := range cfg.Include {
		if len(inc) == 0 {
			return fmt.Errorf("%w: empty include item", ErrInvalid)
		}
		for k, v := range inc {
			if !validKey(k) || !validValue(v) {
				return fmt.Errorf("%w: include item pair %q", ErrInvalid, k)
			}
		}
	}
	return nil
}

// combo 为展开过程中的基础组合：轴值加已写入的附加键值。
type combo struct {
	axisVals []string
	extra    map[string]string
	extraOrd []string
}

func expand(cfg Config) []Job {
	axisIndex := make(map[string]int, len(cfg.Axes))
	for i, ax := range cfg.Axes {
		axisIndex[ax.Key] = i
	}

	// 第一步：笛卡尔积，按轴声明次序、首轴变化最慢。
	base := []*combo{{axisVals: make([]string, len(cfg.Axes)), extra: map[string]string{}}}
	for ai, ax := range cfg.Axes {
		var next []*combo
		for _, c := range base {
			for _, v := range ax.Values {
				nc := &combo{axisVals: slices.Clone(c.axisVals), extra: map[string]string{}}
				nc.axisVals[ai] = v
				next = append(next, nc)
			}
		}
		base = next
	}

	// 第二步：exclude，全部先于 include 处理。
	var surviving []*combo
excluded:
	for _, c := range base {
		for _, ex := range cfg.Exclude {
			match := true
			for k, v := range ex {
				if c.axisVals[axisIndex[k]] != v {
					match = false
					break
				}
			}
			if match {
				continue excluded
			}
		}
		surviving = append(surviving, c)
	}

	// 第三步：include，按列表次序逐项处理；追加的组合不再参与匹配。
	var appended []Job
	for _, inc := range cfg.Include {
		var axisKeys, addKeys []string
		for k := range inc {
			if _, ok := axisIndex[k]; ok {
				axisKeys = append(axisKeys, k)
			} else {
				addKeys = append(addKeys, k)
			}
		}
		slices.SortFunc(axisKeys, func(a, b string) int {
			return axisIndex[a] - axisIndex[b]
		})
		slices.Sort(addKeys)

		matched := false
		for _, c := range surviving {
			ok := true
			for _, k := range axisKeys {
				if c.axisVals[axisIndex[k]] != inc[k] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			matched = true
			for _, k := range addKeys {
				if _, has := c.extra[k]; !has {
					c.extraOrd = append(c.extraOrd, k)
				}
				c.extra[k] = inc[k]
			}
		}
		if !matched {
			pairs := make([]Pair, 0, len(inc))
			for _, k := range axisKeys {
				pairs = append(pairs, Pair{Key: k, Value: inc[k]})
			}
			for _, k := range addKeys {
				pairs = append(pairs, Pair{Key: k, Value: inc[k]})
			}
			appended = append(appended, Job{Pairs: pairs, Experimental: isExperimental(pairs)})
		}
	}

	jobs := make([]Job, 0, len(surviving)+len(appended))
	for _, c := range surviving {
		pairs := make([]Pair, 0, len(cfg.Axes)+len(c.extraOrd))
		for i, ax := range cfg.Axes {
			pairs = append(pairs, Pair{Key: ax.Key, Value: c.axisVals[i]})
		}
		for _, k := range c.extraOrd {
			pairs = append(pairs, Pair{Key: k, Value: c.extra[k]})
		}
		jobs = append(jobs, Job{Pairs: pairs, Experimental: isExperimental(pairs)})
	}
	return append(jobs, appended...)
}

// isExperimental 判定试验性作业：任一键值对中 experimental 的值恰为 "true"。
func isExperimental(pairs []Pair) bool {
	for _, p := range pairs {
		if p.Key == "experimental" && p.Value == "true" {
			return true
		}
	}
	return false
}
