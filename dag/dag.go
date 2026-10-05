// Package dag 负责作业图的校验与下游闭包计算。
//
// 校验按「参数非法 > 重名 > 未知依赖 > 有环」的次序只报第一类错误，
// 四类错误可用 errors.Is 区分。
package dag

import (
	"errors"
	"fmt"

	"ontology/rule"
)

var (
	ErrInvalidArgument   = errors.New("dag: invalid argument")
	ErrDuplicateName     = errors.New("dag: duplicate job name")
	ErrUnknownDependency = errors.New("dag: unknown dependency")
	ErrCycle             = errors.New("dag: dependency cycle")
)

// MaxJobs 是单条流水线允许的最大作业数。
const MaxJobs = 10000

// MaxRetry 是单个作业允许的最大重试次数。
const MaxRetry = 2

// Job 是作业的声明规格。
type Job struct {
	Name         string
	Needs        []string
	When         string
	AllowFailure bool
	Retry        int
}

// Graph 是校验通过的作业图，是下游关系的唯一事实来源。
type Graph struct {
	jobs  []Job
	index map[string]int
	down  [][]int
}

// New 校验 jobs 并构造图。校验按固定次序进行，只报告第一类错误。
func New(jobs []Job) (*Graph, error) {
	if err := checkArguments(jobs); err != nil {
		return nil, err
	}
	index, err := checkDuplicates(jobs)
	if err != nil {
		return nil, err
	}
	if err := checkNeeds(jobs, index); err != nil {
		return nil, err
	}
	if err := checkCycle(jobs, index); err != nil {
		return nil, err
	}
	g := &Graph{jobs: jobs, index: index, down: make([][]int, len(jobs))}
	for i, j := range jobs {
		for _, need := range j.Needs {
			n := index[need]
			g.down[n] = append(g.down[n], i)
		}
	}
	return g, nil
}

func checkArguments(jobs []Job) error {
	if len(jobs) == 0 || len(jobs) > MaxJobs {
		return fmt.Errorf("%w: job count must be in [1, %d], got %d", ErrInvalidArgument, MaxJobs, len(jobs))
	}
	for _, j := range jobs {
		if j.Name == "" {
			return fmt.Errorf("%w: empty job name", ErrInvalidArgument)
		}
		if _, ok := rule.Parse(j.When); !ok {
			return fmt.Errorf("%w: job %q has invalid when %q", ErrInvalidArgument, j.Name, j.When)
		}
		if j.Retry < 0 || j.Retry > MaxRetry {
			return fmt.Errorf("%w: job %q retry %d out of [0, %d]", ErrInvalidArgument, j.Name, j.Retry, MaxRetry)
		}
	}
	return nil
}

func checkDuplicates(jobs []Job) (map[string]int, error) {
	index := make(map[string]int, len(jobs))
	for i, j := range jobs {
		if _, dup := index[j.Name]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, j.Name)
		}
		index[j.Name] = i
	}
	return index, nil
}

func checkNeeds(jobs []Job, index map[string]int) error {
	for _, j := range jobs {
		for _, need := range j.Needs {
			if _, ok := index[need]; !ok {
				return fmt.Errorf("%w: job %q needs %q", ErrUnknownDependency, j.Name, need)
			}
		}
	}
	return nil
}

func checkCycle(jobs []Job, index map[string]int) error {
	const (
		white = iota
		gray
		black
	)
	color := make([]int, len(jobs))
	var visit func(i int) error
	visit = func(i int) error {
		color[i] = gray
		for _, need := range jobs[i].Needs {
			n := index[need]
			switch color[n] {
			case gray:
				return fmt.Errorf("%w: %q and %q", ErrCycle, jobs[i].Name, need)
			case white:
				if err := visit(n); err != nil {
					return err
				}
			}
		}
		color[i] = black
		return nil
	}
	for i := range jobs {
		if color[i] == white {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// Jobs 返回按声明次序排列的作业规格。
func (g *Graph) Jobs() []Job {
	return g.jobs
}

// DirectDownstream 返回 name 的直接下游作业名，按声明次序排列。
func (g *Graph) DirectDownstream(name string) []string {
	i, ok := g.index[name]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(g.down[i]))
	for _, n := range g.down[i] {
		out = append(out, g.jobs[n].Name)
	}
	return out
}

// TransitiveDownstream 返回 name 的传递下游闭包（不含 name 自身）。
func (g *Graph) TransitiveDownstream(name string) map[string]bool {
	out := make(map[string]bool)
	if _, ok := g.index[name]; !ok {
		return out
	}
	queue := []string{name}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range g.DirectDownstream(cur) {
			if !out[nxt] {
				out[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}
	return out
}
