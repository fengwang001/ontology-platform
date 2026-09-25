// Package api 对外暴露物化视图的乐观并发提交器。
package api

import "ontology/commit"

// Op 是一条操作：对 Key 施加非零增量 Delta。
type Op = commit.Op

// 可判定的哨兵错误，互不相同。
var (
	ErrEmptyBatch = commit.ErrEmptyBatch
	ErrEmptyKey   = commit.ErrEmptyKey
	ErrZeroDelta  = commit.ErrZeroDelta
	ErrBadConfig  = commit.ErrBadConfig
	ErrContended  = commit.ErrContended
)

// Committer 是物化视图的乐观并发提交器。
type Committer struct{ eng *commit.Engine }

// New 创建 Committer；maxRetries 必须 ≥ 1，否则 ErrBadConfig。
func New(maxRetries int) (*Committer, error) {
	e, err := commit.New(maxRetries)
	if err != nil {
		return nil, err
	}
	return &Committer{eng: e}, nil
}

// Commit 提交一个事务（一批 Op），冲突自动重试。
func (c *Committer) Commit(batch []Op) error { return c.eng.Commit(batch) }

// View 返回当前 Key → val 快照。
func (c *Committer) View() map[string]int64 { return c.eng.View() }

// Retries 返回累计的「冲突后重试并最终成功」的次数。
func (c *Committer) Retries() int64 { return c.eng.Retries() }

// SelfCheck 对内置序列核验不变量；全部通过返回 nil。
func (c *Committer) SelfCheck() error { return c.eng.SelfCheck() }
