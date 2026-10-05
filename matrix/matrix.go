// Package matrix 展开构建矩阵：轴的笛卡尔积，exclude 剔除，include 匹配写入或追加。
package matrix

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrInvalidParam = errors.New("matrix: invalid parameter")
	ErrTooLarge     = errors.New("matrix: matrix too large")
	ErrEmpty        = errors.New("matrix: empty matrix")
)

const (
	maxAxes     = 5
	maxValues   = 10
	maxRules    = 64
	maxKeyLen   = 32
	maxValueLen = 64
	// MaxCombos 为最终组合数上限。
	MaxCombos = 256
)

// Axis 为一条有序的（键, 值列表）。
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

// Combo 为展开后的一个组合。键序规范化为：轴键按声明序，附加键按字典序。
type Combo struct {
	axis  []string
	extra []string
	vals  map[string]string
	base  bool
}

// Base 报告该组合是否为基础组合（而非 include 追加的组合）。
func (c Combo) Base() bool { return c.base }

// Keys 按规范化次序返回全部键。
func (c Combo) Keys() []string {
	keys := make([]string, 0, len(c.axis)+len(c.extra))
	keys = append(keys, c.axis...)
	keys = append(keys, c.extra...)
	return keys
}

// Get 返回键对应值。
func (c Combo) Get(key string) (string, bool) {
	v, ok := c.vals[key]
	return v, ok
}

// Experimental 报告该组合是否为试验性（experimental 的值恰为 "true"）。
func (c Combo) Experimental() bool {
	v, ok := c.vals["experimental"]
	return ok && v == "true"
}

// String 返回规范化序列化，相同配置的展开结果逐字节相同。
func (c Combo) String() string {
	var sb strings.Builder
	for i, k := range c.Keys() {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s=%s", k, c.vals[k])
	}
	return sb.String()
}

func validate(cfg Config) error {
	if len(cfg.Axes) < 1 || len(cfg.Axes) > maxAxes {
		return ErrInvalidParam
	}
	axisKeys := make(map[string]bool, len(cfg.Axes))
	for _, ax := range cfg.Axes {
		if len(ax.Key) < 1 || len(ax.Key) > maxKeyLen || axisKeys[ax.Key] {
			return ErrInvalidParam
		}
		axisKeys[ax.Key] = true
		if len(ax.Values) < 1 || len(ax.Values) > maxValues {
			return ErrInvalidParam
		}
		seen := make(map[string]bool, len(ax.Values))
		for _, v := range ax.Values {
			if len(v) > maxValueLen || seen[v] {
				return ErrInvalidParam
			}
			seen[v] = true
		}
	}
	if len(cfg.Exclude) > maxRules || len(cfg.Include) > maxRules {
		return ErrInvalidParam
	}
	for _, item := range cfg.Exclude {
		if len(item) == 0 {
			return ErrInvalidParam
		}
		for k, v := range item {
			if !axisKeys[k] || len(v) > maxValueLen {
				return ErrInvalidParam
			}
		}
	}
	for _, item := range cfg.Include {
		if len(item) == 0 {
			return ErrInvalidParam
		}
		for k, v := range item {
			if len(k) < 1 || len(k) > maxKeyLen || len(v) > maxValueLen {
				return ErrInvalidParam
			}
		}
	}
	return nil
}

func insertSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// Expand 校验配置并展开矩阵。拒绝次序：参数非法 > 矩阵过大 > 空矩阵。
func Expand(cfg Config) ([]Combo, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	axisSet := make(map[string]bool, len(cfg.Axes))
	axisOrder := make([]string, len(cfg.Axes))
	for i, ax := range cfg.Axes {
		axisSet[ax.Key] = true
		axisOrder[i] = ax.Key
	}

	// 第一步：笛卡尔积，按轴声明次序、首轴变化最慢。
	var kept []Combo
	var walk func(ax int, vals map[string]string)
	walk = func(ax int, vals map[string]string) {
		if ax == len(cfg.Axes) {
			cp := make(map[string]string, len(vals))
			for k, v := range vals {
				cp[k] = v
			}
			kept = append(kept, Combo{axis: axisOrder, vals: cp, base: true})
			return
		}
		for _, v := range cfg.Axes[ax].Values {
			vals[cfg.Axes[ax].Key] = v
			walk(ax+1, vals)
		}
	}
	walk(0, map[string]string{})

	// 第二步：exclude 全部先于 include 处理。
	survived := kept[:0]
	for _, c := range kept {
		excluded := false
		for _, item := range cfg.Exclude {
			match := true
			for k, v := range item {
				if c.vals[k] != v {
					match = false
					break
				}
			}
			if match {
				excluded = true
				break
			}
		}
		if !excluded {
			survived = append(survived, c)
		}
	}
	kept = survived

	// 第三步：include 按列表次序逐项处理，只匹配留存的基础组合。
	var appended []Combo
	for _, item := range cfg.Include {
		var axisPart, extraPart []string
		for k := range item {
			if axisSet[k] {
				axisPart = append(axisPart, k)
			} else {
				extraPart = append(extraPart, k)
			}
		}
		sort.Strings(extraPart)
		matched := false
		for i := range kept {
			match := true
			for _, k := range axisPart {
				if kept[i].vals[k] != item[k] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			matched = true
			for _, k := range extraPart {
				if _, ok := kept[i].vals[k]; !ok {
					kept[i].extra = insertSorted(kept[i].extra, k)
				}
				kept[i].vals[k] = item[k]
			}
		}
		if !matched {
			vals := make(map[string]string, len(item))
			for k, v := range item {
				vals[k] = v
			}
			var ax []string
			for _, k := range axisOrder {
				if _, ok := item[k]; ok {
					ax = append(ax, k)
				}
			}
			appended = append(appended, Combo{axis: ax, extra: extraPart, vals: vals})
		}
	}

	final := append(kept, appended...)
	if len(final) > MaxCombos {
		return nil, ErrTooLarge
	}
	if len(final) == 0 {
		return nil, ErrEmpty
	}
	return final, nil
}
