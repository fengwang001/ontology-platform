package quota

import "fmt"

// PodClass 是 Pod 的服务等级与期限属性。
type PodClass struct {
	// BestEffort：所有资源补全后的请求量与上限量均缺省或为零。
	BestEffort bool
	// Terminating：声明了大于零的存活截止时长。
	Terminating bool
}

// classify 由补全结果判定 Pod 类别。
func classify(eff EffectivePod) PodClass {
	bestEffort := true
	for _, v := range eff.Requests {
		if v != 0 {
			bestEffort = false
			break
		}
	}
	if bestEffort {
		for _, v := range eff.Limits {
			if v != 0 {
				bestEffort = false
				break
			}
		}
	}
	return PodClass{BestEffort: bestEffort, Terminating: eff.Terminating}
}

// scopeSet 是配额作用域的去重集合。
type scopeSet map[Scope]struct{}

// newScopeSet 校验作用域集合：未知作用域或互相矛盾的组合为非法配置。
func newScopeSet(scopes []Scope) (scopeSet, error) {
	s := make(scopeSet, len(scopes))
	for _, sc := range scopes {
		switch sc {
		case ScopeBestEffort, ScopeNotBestEffort, ScopeTerminating, ScopeNotTerminating:
			s[sc] = struct{}{}
		default:
			return nil, &Error{Kind: ErrInvalidConfiguration,
				Msg: fmt.Sprintf("未知作用域 %q", string(sc))}
		}
	}
	if _, a := s[ScopeBestEffort]; a {
		if _, b := s[ScopeNotBestEffort]; b {
			return nil, &Error{Kind: ErrInvalidConfiguration,
				Msg: "作用域 BestEffort 与 NotBestEffort 互相矛盾"}
		}
	}
	if _, a := s[ScopeTerminating]; a {
		if _, b := s[ScopeNotTerminating]; b {
			return nil, &Error{Kind: ErrInvalidConfiguration,
				Msg: "作用域 Terminating 与 NotTerminating 互相矛盾"}
		}
	}
	return s, nil
}

// matches 判断类别为 c 的 Pod 是否满足全部作用域；空集合适用全部 Pod。
func (s scopeSet) matches(c PodClass) bool {
	if _, ok := s[ScopeBestEffort]; ok && !c.BestEffort {
		return false
	}
	if _, ok := s[ScopeNotBestEffort]; ok && c.BestEffort {
		return false
	}
	if _, ok := s[ScopeTerminating]; ok && !c.Terminating {
		return false
	}
	if _, ok := s[ScopeNotTerminating]; ok && c.Terminating {
		return false
	}
	return true
}
