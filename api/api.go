// Package api 对外门面：New/Insert/Delete/Complete/Count/SelfCheck。依赖 rank。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"

	"ontology/rank"
	"ontology/trie"
)

// Trie 是自动补全组件的对外句柄，并发安全。
type Trie struct{ t *trie.Trie }

func New() *Trie { return &Trie{t: trie.New()} }

// Insert 新建串（频率 f）或对已存在串累加频率。
func (a *Trie) Insert(s string, f int) error { return a.t.Insert(s, f) }

// Delete 整条移除 s；不存在则报错。
func (a *Trie) Delete(s string) error { return a.t.Delete(s) }

// Complete 返回以 prefix 开头、按 (频率降, 字典序升) 的前 k 个串。
func (a *Trie) Complete(prefix string, k int) ([]string, error) {
	if err := trie.CheckString(prefix); err != nil {
		return nil, err
	}
	cands, err := a.t.Collect(prefix)
	if err != nil {
		return nil, err
	}
	return rank.TopK(cands, k)
}

// Count 返回当前不同串总数。
func (a *Trie) Count() int { return a.t.Count() }

// naiveTopK 朴素参照：收集全部前缀匹配、全排序、取前 k。
func naiveTopK(m map[string]int, prefix string, k int) []string {
	var c []trie.Candidate
	for s, f := range m {
		if strings.HasPrefix(s, prefix) {
			c = append(c, trie.Candidate{Word: s, Freq: f})
		}
	}
	sort.Slice(c, func(i, j int) bool {
		if c[i].Freq != c[j].Freq {
			return c[i].Freq > c[j].Freq
		}
		return c[i].Word < c[j].Word
	})
	var out []string
	for i := 0; i < len(c) && i < k; i++ {
		out = append(out, c[i].Word)
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *Trie) SelfCheck() error {
	t := New()
	for _, iv := range [][2]any{{"apricot", 5}, {"apple", 5}, {"app", 3}, {"application", 2}} {
		if err := t.Insert(iv[0].(string), iv[1].(int)); err != nil {
			return err
		}
	}
	got, err := t.Complete("ap", 3)
	if err != nil || !slices.Equal(got, []string{"apple", "apricot", "app"}) {
		return fmt.Errorf("selfcheck: complete before delete = %v, %v", got, err)
	}
	if err := t.Delete("app"); err != nil {
		return err
	}
	got, err = t.Complete("ap", 3)
	if err != nil || !slices.Equal(got, []string{"apple", "apricot", "application"}) || t.Count() != 3 {
		return fmt.Errorf("selfcheck: after delete = %v count=%d", got, t.Count())
	}
	// 不变量3（行为面）：app 节点不可连带剪掉，appl 子树必须完整保留
	sub, err := t.Complete("appl", 10)
	if err != nil || !slices.Equal(sub, []string{"apple", "application"}) {
		return fmt.Errorf("selfcheck: subtree over-pruned = %v", sub)
	}
	// 不变量4：四类拒绝互不相同且不留痕
	before := t.Count()
	var utf8Err *trie.UTF8Error
	if !errors.Is(t.Insert("", 1), trie.ErrEmptyString) ||
		!errors.Is(t.Delete(""), trie.ErrEmptyString) {
		return errors.New("selfcheck: empty string not rejected")
	}
	err = t.Insert("a\xffb", 1)
	if !errors.Is(err, trie.ErrInvalidUTF8) || !errors.As(err, &utf8Err) || utf8Err.Offset != 1 {
		return errors.New("selfcheck: invalid utf-8 not rejected with offset")
	}
	if !errors.Is(t.Delete("absent"), trie.ErrNotFound) {
		return errors.New("selfcheck: delete-absent not rejected")
	}
	if _, err = t.Complete("ap", 0); !errors.Is(err, rank.ErrInvalidK) {
		return errors.New("selfcheck: bad k not rejected")
	}
	if t.Count() != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	// 不变量1/2：确定性随机操作流 vs 朴素参照
	rng := rand.New(rand.NewSource(1))
	m := map[string]int{}
	u := New()
	for i := 0; i < 300; i++ {
		s := string([]byte{byte('a' + rng.Intn(3)), byte('a' + rng.Intn(3)), byte('a' + rng.Intn(3))}[:1+rng.Intn(3)])
		switch rng.Intn(3) {
		case 0:
			f := 1 + rng.Intn(9)
			m[s] += f
			_ = u.Insert(s, f)
		case 1:
			if _, ok := m[s]; ok {
				delete(m, s)
				_ = u.Delete(s)
			}
		default:
			k := 1 + rng.Intn(4)
			want := naiveTopK(m, s[:1], k)
			got, err := u.Complete(s[:1], k)
			if err != nil || !slices.Equal(got, want) {
				return fmt.Errorf("selfcheck: naive mismatch got=%v want=%v", got, want)
			}
		}
	}
	if u.Count() != len(m) {
		return errors.New("selfcheck: count mismatch")
	}
	for s := range m { // 全部删光后必须归零且仍可正常使用（剪枝无残留）
		if err := u.Delete(s); err != nil {
			return err
		}
	}
	if u.Count() != 0 {
		return errors.New("selfcheck: delete-all did not drain")
	}
	return u.Insert("ok", 1)
}
