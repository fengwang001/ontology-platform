package quota

import "fmt"

// attributes 是 Pod 与作用域匹配有关的属性：服务等级与期限。
type attributes struct {
	bestEffort  bool
	terminating bool
}

// attributesOf 由补全结果与原始声明推导 Pod 属性。
// 存活截止时长已声明且大于零为有期限，否则无期限。
func attributesOf(c completed, spec PodSpec) attributes {
	return attributes{
		bestEffort:  c.bestEffort(),
		terminating: spec.ActiveDeadlineSeconds != nil && *spec.ActiveDeadlineSeconds > 0,
	}
}

// scopeSet 是配额作用域的规范化集合。
type scopeSet map[Scope]struct{}

func newScopeSet(scopes []Scope) scopeSet {
	s := make(scopeSet, len(scopes))
	for _, sc := range scopes {
		s[sc] = struct{}{}
	}
	return s
}

// matches 报告属性是否满足作用域集合中的全部作用域；空集合适用全部 Pod。
func (s scopeSet) matches(a attributes) bool {
	for sc := range s {
		switch sc {
		case ScopeBestEffort:
			if !a.bestEffort {
				return false
			}
		case ScopeNotBestEffort:
			if a.bestEffort {
				return false
			}
		case ScopeTerminating:
			if !a.terminating {
				return false
			}
		case ScopeNotTerminating:
			if a.terminating {
				return false
			}
		}
	}
	return true
}

// validateQuotaParams 校验配额的参数层面问题：未知作用域字面量、
// 硬上限资源名形式非法、负上限，报 KindInvalidArgument。
// 与 validateQuotaConfig 拆分是为了实现错误优先级：
// 参数非法 > 命名空间不存在 > 重复创建 > 非法配置。
func validateQuotaParams(spec QuotaSpec) *Error {
	for _, sc := range spec.Scopes {
		if !sc.known() {
			return invalidArg(fmt.Sprintf("未知作用域 %q", sc))
		}
	}
	for r, v := range spec.Hard {
		if !r.IsValid() {
			return invalidArg(fmt.Sprintf("硬上限资源名 %q 形式非法，只允许 pods / requests.X / limits.X", r))
		}
		if v < 0 {
			return invalidArg(fmt.Sprintf("硬上限 %s 为负数: %d", r, v))
		}
	}
	return nil
}

// validateQuotaConfig 校验配额的语义层面问题：矛盾作用域、
// 含尽力型作用域的配额限制 pods 以外的资源，报 KindInvalidConfiguration。
func validateQuotaConfig(spec QuotaSpec) *Error {
	set := newScopeSet(spec.Scopes)
	_, be := set[ScopeBestEffort]
	_, nbe := set[ScopeNotBestEffort]
	if be && nbe {
		return invalidConfig("作用域同时包含 BestEffort 与 NotBestEffort，互相矛盾")
	}
	_, term := set[ScopeTerminating]
	_, nterm := set[ScopeNotTerminating]
	if term && nterm {
		return invalidConfig("作用域同时包含 Terminating 与 NotTerminating，互相矛盾")
	}
	if be {
		for r := range spec.Hard {
			if r != ResourcePods {
				return invalidConfig(fmt.Sprintf("含 BestEffort 作用域的配额硬上限只允许 pods，出现 %s", r))
			}
		}
	}
	return nil
}
