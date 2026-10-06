// Package scholarship 实现奖学金评定排序与名额分配引擎。
//
// 模块职责划分:
//   - types.go       数据模型、错误分类与配置校验
//   - eligibility.go 学生数据与资格判定
//   - ranking.go     排序与名次(并列处理)
//   - allocate.go    奖项等级、名额分配与回流
//   - engine.go      复议、重评、确认与并发串行化
package scholarship

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// 错误分类, 拒绝优先级(高 -> 低):
// ErrInvalidParam > ErrNotFound > ErrNotEvaluated > ErrAwardNotInResult > ErrAlreadyConfirmed
var (
	ErrInvalidParam     = errors.New("scholarship: invalid parameter")
	ErrNotFound         = errors.New("scholarship: student, department or level not found")
	ErrNotEvaluated     = errors.New("scholarship: evaluation has not been performed")
	ErrAwardNotInResult = errors.New("scholarship: award not present in current result")
	ErrAlreadyConfirmed = errors.New("scholarship: award already confirmed")
)

// FailReason 资格判定失败原因。多条不满足时只报告优先级最高的一条:
// 处分 > 不及格 > 学分不足 > 平均成绩不足。
type FailReason int

const (
	ReasonNone FailReason = iota
	ReasonDiscipline
	ReasonFailRecord
	ReasonCredits
	ReasonAvgGrade
)

func (r FailReason) String() string {
	switch r {
	case ReasonNone:
		return "合格"
	case ReasonDiscipline:
		return "处分生效中"
	case ReasonFailRecord:
		return "存在不及格记录"
	case ReasonCredits:
		return "学分不足"
	case ReasonAvgGrade:
		return "平均成绩不足"
	}
	return "未知"
}

// LevelConfig 奖项等级配置。Config.Levels 按全局次序从高到低排列,
// 高等级的下限不低于低等级(校验保证)。
type LevelConfig struct {
	ID         string
	MinAvg     float64
	MinCredits int
	Quotas     map[string]int // 院系 -> 该等级在该院系的名额
}

// Config 引擎配置。PoolSize 为全局机动名额池初始大小。
type Config struct {
	Levels   []LevelConfig
	PoolSize int
}

// Discipline 处分记录。LiftedAt 为零值表示无限期生效;
// 解除时刻不晚于评定时刻(LiftedAt <= at)视为已解除。
type Discipline struct {
	ID       string
	LiftedAt time.Time
}

// Student 学生数据。平均成绩作为输入直接给定, 引擎不做加权计算。
type Student struct {
	ID            string
	DeptID        string
	AvgGrade      float64
	Credits       int
	HonorPoints   int
	HasFailRecord bool
	Disciplines   []Discipline
}

// Award 一项授予结果。FromPool 表示名额来自全局机动池。
type Award struct {
	StudentID string
	LevelID   string
	DeptID    string
	FromPool  bool
}

// Disqualification 一条资格否决记录(首个不满足的规则)。
type Disqualification struct {
	StudentID string
	LevelID   string
	Reason    FailReason
}

// RankEntry 名次条目; 并列者同名次, 其后名次按人数跳号。
type RankEntry struct {
	StudentID string
	Rank      int
}

// DeptRanking 一个院系的完整名次表。
type DeptRanking struct {
	DeptID  string
	Entries []RankEntry
}

// Result 一次评定的完整结果, 所有切片顺序确定。
type Result struct {
	Awards       []Award
	Disqualified []Disqualification
	Rankings     []DeptRanking
	PoolLeft     int
}

// Stats 复杂度约束的可验证计数器。
type Stats struct {
	StudentReads int64
}

func validateGrade(avg float64) error {
	if math.IsNaN(avg) || avg < 0 || avg > 100 {
		return fmt.Errorf("%w: average grade %v out of range [0,100]", ErrInvalidParam, avg)
	}
	return nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Levels) == 0 {
		return fmt.Errorf("%w: no award levels", ErrInvalidParam)
	}
	if cfg.PoolSize < 0 {
		return fmt.Errorf("%w: negative pool size %d", ErrInvalidParam, cfg.PoolSize)
	}
	seen := map[string]bool{}
	for i, lv := range cfg.Levels {
		if lv.ID == "" {
			return fmt.Errorf("%w: empty level id", ErrInvalidParam)
		}
		if seen[lv.ID] {
			return fmt.Errorf("%w: duplicate level %q", ErrInvalidParam, lv.ID)
		}
		seen[lv.ID] = true
		if err := validateGrade(lv.MinAvg); err != nil {
			return err
		}
		if lv.MinCredits < 0 {
			return fmt.Errorf("%w: negative min credits for level %q", ErrInvalidParam, lv.ID)
		}
		for dept, q := range lv.Quotas {
			if dept == "" {
				return fmt.Errorf("%w: empty department id", ErrInvalidParam)
			}
			if q < 0 {
				return fmt.Errorf("%w: negative quota for level %q dept %q", ErrInvalidParam, lv.ID, dept)
			}
		}
		if i > 0 {
			prev := cfg.Levels[i-1]
			if lv.MinAvg > prev.MinAvg || lv.MinCredits > prev.MinCredits {
				return fmt.Errorf("%w: level %q thresholds exceed higher level %q", ErrInvalidParam, lv.ID, prev.ID)
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
