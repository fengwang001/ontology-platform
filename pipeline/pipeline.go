// Package pipeline 串起 source→parse→分组聚合→sink，并负责恢复与优雅停止。
package pipeline

import "context"

// Config 是管线配置。
type Config struct {
	WorkDir   string
	Capacity  int
	Epoch     int
	MaxGroups int
}

// Report 是一次运行的结果统计。
type Report struct {
	Pos        int64
	Bad        int64
	Blocks     int64
	MaxInFlight int64
	FellBack   bool
}

// Pipeline 是一条可 Start/Stop/Resume 的聚合管线（骨架）。
type Pipeline struct{}

// New 创建管线。
func New(cfg Config) *Pipeline { return &Pipeline{} }

// Run 从检查点恢复并跑到结束，返回报告。
func (p *Pipeline) Run(ctx context.Context) (Report, error) { return Report{}, nil }

// Stop 幂等地请求优雅停止。
func (p *Pipeline) Stop() {}
