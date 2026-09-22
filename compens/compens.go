// Package compens 根据「已成功步骤集合」计算补偿计划：严格逆序、只补偿已成功者，
// 并给出被跳过的步骤。依赖 step，不依赖 journal/saga。
package compens

import (
	"errors"

	"ontology/step"
)

// Item 是补偿计划中的一项：补偿成功集合内的步骤（严格逆序）。
type Item struct {
	Index int
	Step  step.Step
}

// Plan 是一次补偿的执行计划。Items 必须严格按下标逆序执行；Skipped 列出
// 不在成功集合中的步骤（失败步与未执行步），它们绝不产生补偿调用。
type Plan struct {
	Items   []Item
	Skipped []int
}

// Validate 校验步骤定义：非空且幂等键唯一。返回哨兵错误，调用方可直接判定。
func Validate(steps []step.Step) error {
	if len(steps) == 0 {
		return ErrNoSteps
	}
	seen := make(map[string]struct{}, len(steps))
	for _, s := range steps {
		if _, ok := seen[s.Key]; ok {
			return ErrDuplicateKey
		}
		seen[s.Key] = struct{}{}
	}
	return nil
}

// Build 依据已成功步骤的下标集合生成补偿计划。
// succeeded 必须是合法下标集合；若其中某个步骤的补偿动作为 nil，
// 返回 ErrNilCompensate（与空步骤列表、重复键是互不相同的错误）。
func Build(steps []step.Step, succeeded map[int]bool) (Plan, error) {
	var p Plan
	for i := len(steps) - 1; i >= 0; i-- {
		if !succeeded[i] {
			p.Skipped = append(p.Skipped, i)
			continue
		}
		if steps[i].Compensate == nil {
			return Plan{}, ErrNilCompensate
		}
		p.Items = append(p.Items, Item{Index: i, Step: steps[i]})
	}
	reverseInts(p.Skipped)
	return p, nil
}

func reverseInts(xs []int) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}

var (
	// ErrNoSteps 表示 SAGA 没有任何步骤。
	ErrNoSteps = errors.New("compens: step list is empty")
	// ErrDuplicateKey 表示两个步骤的幂等键相同。
	ErrDuplicateKey = errors.New("compens: duplicate idempotency key")
	// ErrNilCompensate 表示已成功步骤没有补偿动作，无法满足补偿要求。
	ErrNilCompensate = errors.New("compens: succeeded step has nil compensate action")
)
