package keygroup

import "fmt"

// GroupMigration 描述一个键组的迁移记录。
type GroupMigration struct {
	Group int // 键组编号
	From  int // 原实例下标
	To    int // 新实例下标
	Keys  int // 整组迁移的键数
}

// MigrationReport 是一次扩缩容的完整结果。
type MigrationReport struct {
	OldParallelism int
	NewParallelism int
	MovedGroups    []GroupMigration // 归属变化、被整组迁移的键组
	MovedKeys      int              // 迁移量 = 被移动的键数
	TotalKeys      int              // 扩缩容前后保持不变的键总数
}

// Rescale 把并行度调整为 newParallelism。
//
// 规则：逐键组比较调整前后的归属实例，归属变化的键组整组迁移，
// 归属不变的键组原地不动；迁移量即被移动键组内的键数总和。
// 参数非法时返回错误，且当前并行度、键值与分桶均不改变。
func (s *Store) Rescale(newParallelism int) (MigrationReport, error) {
	if newParallelism <= 0 || newParallelism > s.maxParallelism {
		return MigrationReport{}, invalidInput("Rescale", ErrInvalidParallelism,
			fmt.Sprintf("newParallelism=%d, maxParallelism=%d", newParallelism, s.maxParallelism))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	oldOwners := s.owners
	newOwners, err := computeAssignment(s.maxParallelism, newParallelism)
	if err != nil {
		return MigrationReport{}, err
	}

	report := MigrationReport{
		OldParallelism: s.parallelism,
		NewParallelism: newParallelism,
	}
	for g := 0; g < s.maxParallelism; g++ {
		report.TotalKeys += len(s.buckets[g])
		if oldOwners[g] == newOwners[g] {
			continue // 归属不变，原地不动
		}
		report.MovedGroups = append(report.MovedGroups, GroupMigration{
			Group: g,
			From:  oldOwners[g],
			To:    newOwners[g],
			Keys:  len(s.buckets[g]),
		})
		report.MovedKeys += len(s.buckets[g])
	}

	// 全部校验与计算通过后一次性提交，保证失败不改变状态。
	s.parallelism = newParallelism
	s.owners = newOwners

	s.logger.Info("扩缩容完成",
		"oldParallelism", report.OldParallelism,
		"newParallelism", report.NewParallelism,
		"oldOwners", oldOwners,
		"newOwners", newOwners,
		"movedGroups", report.MovedGroups,
		"movedKeys", report.MovedKeys,
		"totalKeys", report.TotalKeys,
		"rule", "归属变化的键组整组迁移，归属不变的原地不动")
	return report, nil
}
