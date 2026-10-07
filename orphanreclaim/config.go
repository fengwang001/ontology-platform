package orphanreclaim

import "sort"

// ContributionKind 描述一种链接类型对「保留判定」的贡献方式。
type ContributionKind int

const (
	// ContributionUndefined 表示该链接类型尚未配置贡献方式。
	ContributionUndefined ContributionKind = iota
	// IndependentRetention 独立保留：存在任意一条该类型入边即非孤儿。
	IndependentRetention
	// JointRetention 联合保留：须与组内其他链接类型的入边同时存在。
	JointRetention
)

// LinkRule 是单个链接类型的保留判定配置。
type LinkRule struct {
	Type       string
	Kind       ContributionKind
	JointGroup []string // 仅 Kind == JointRetention 时有效：组内全部类型都须有入边（含自身）
}

// Config 是回收子系统的静态配置。
type Config struct {
	Rules     map[string]LinkRule
	GraceGen1 int64 // 第一代宽限期（单调时钟刻度，>0）
	GraceGen2 int64 // 第二代宽限期（单调时钟刻度，>0，通常与第一代不同且更短）
}

// normalizedConfig 是校验并规范化后的配置：
// 相互重叠的联合组经并查集合并为一个完整组；联合类型 -> 其所属完整组。
type normalizedConfig struct {
	independents []string            // 独立保留类型（字典序，核对次序稳定可复现）
	jointGroups  map[string][]string // 组成员名（联合类型 -> 其所属完整组）
	graceGen1    int64
	graceGen2    int64
}

// validateNormalize 按固定次序核对配置错误：
// 先 (3) 联合保留引用未定义类型 / 单元素组，再 (4) 宽限期非正数。
// (1)(2) 属于运行时按对象/按操作核对的错误，由 validateMutation 负责。
func validateNormalize(cfg Config) (normalizedConfig, error) {
	nc := normalizedConfig{
		jointGroups: map[string][]string{},
		graceGen1:   cfg.GraceGen1,
		graceGen2:   cfg.GraceGen2,
	}

	// (3a) 联合组引用的每个类型都必须存在；用并查集合并相互重叠的组。
	uf := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if uf[x] != x {
			uf[x] = find(uf[x])
		}
		return uf[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra < rb {
			uf[rb] = ra
		} else {
			uf[ra] = rb
		}
	}
	for name, rule := range cfg.Rules {
		if rule.Kind != JointRetention {
			continue
		}
		if _, seen := uf[name]; !seen {
			uf[name] = name
		}
		for _, ref := range rule.JointGroup {
			if _, ok := cfg.Rules[ref]; !ok {
				return normalizedConfig{}, ErrJointRefUndefined // (3)
			}
			if _, seen := uf[ref]; !seen {
				uf[ref] = ref
			}
			union(name, ref)
		}
	}
	// (3b) 汇总并查集分组；单元素「联合」组没有可联合的伙伴，归入第 (3) 类。
	rootMembers := map[string][]string{}
	for x := range uf {
		root := find(x)
		rootMembers[root] = append(rootMembers[root], x)
	}
	for _, members := range rootMembers {
		sort.Strings(members)
		if len(members) < 2 {
			return normalizedConfig{}, ErrJointRefUndefined
		}
		for _, m := range members {
			if cfg.Rules[m].Kind == JointRetention {
				nc.jointGroups[m] = members
			}
		}
	}

	if cfg.GraceGen1 <= 0 || cfg.GraceGen2 <= 0 {
		return normalizedConfig{}, ErrInvalidGracePeriod // (4)
	}

	for name, rule := range cfg.Rules {
		if rule.Kind == IndependentRetention {
			nc.independents = append(nc.independents, name)
		}
	}
	sort.Strings(nc.independents)
	return nc, nil
}

// validateMutation 核对单条链接写操作的输入，次序固定为 (1) 目标对象不存在
// 先于 (2) 链接类型未配置贡献方式。(3)(4) 已在 New 时拦截，不会出现在这里。
func (r *Reclaimer) validateMutation(target, linkType string) (*objectState, error) {
	obj, ok := r.objects[target]
	if !ok {
		return nil, ErrObjectNotFound // (1)
	}
	if _, ok := r.cfg.Rules[linkType]; !ok {
		return nil, ErrLinkTypeUnconfigured // (2)
	}
	return obj, nil
}
