// Package match 是记忆化 DP 匹配核心。依赖 parse。
package match

import (
	"sync/atomic"

	"ontology/parse"
)

// evaluated 是非导出计数器：记录记忆化 DP 实际求值的 (i,k) 状态个数。
// 只在本包内的测试里可观察，不出现在任何公开接口。
var evaluated atomic.Int64

// Match 判断 p 是否整体匹配 s；非法模式视为不匹配。
func Match(s, p string) bool {
	toks, err := parse.Parse(p)
	if err != nil {
		return false
	}
	return MatchTokens(s, toks)
}

// MatchTokens 对已解析的模式做记忆化 DP 整体匹配。
func MatchTokens(s string, toks []parse.Token) bool {
	return matchTokens(s, toks, true)
}

// Naive 是不带记忆化的朴素回溯（对 '*' 枚举取零次/多次并回溯），
// 与 MatchTokens 共用同一递推，语义逐对相同，只是复杂度为指数级。
func Naive(s string, toks []parse.Token) bool {
	return matchTokens(s, toks, false)
}

// matchTokens 判定 toks 是否整体匹配 s。f(i,k) 表示 toks[k:] 是否整体匹配 s[i:]。
// useMemo 只决定是否缓存已确定的布尔结果，不改变任何语义。
func matchTokens(s string, toks []parse.Token, useMemo bool) bool {
	type key struct{ i, k int }
	var cache map[key]bool
	if useMemo {
		cache = make(map[key]bool, len(toks)+1)
	}
	var f func(i, k int) bool
	f = func(i, k int) bool {
		if useMemo {
			if v, ok := cache[key{i, k}]; ok {
				return v
			}
			evaluated.Add(1)
		}
		var r bool
		if k == len(toks) {
			r = i == len(s) // 锚定首尾：模式用完当且仅当文本也用完
		} else {
			t := toks[k]
			first := i < len(s) && (t.Ch == '.' || t.Ch == s[i])
			if t.Star {
				r = f(i, k+1) || first && f(i+1, k) // 零次，或消费一个字符后继续
			} else {
				r = first && f(i+1, k+1)
			}
		}
		if useMemo {
			cache[key{i, k}] = r
		}
		return r
	}
	return f(0, 0)
}
