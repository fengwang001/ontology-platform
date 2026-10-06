# scholarship — 奖学金评定排序与名额分配引擎

纯标准库 Go 包 `ontology/scholarship`，无外部依赖，线程安全，结果可精确复现。

## 概念

- 等级（`LevelConfig`）：全局次序由传入切片顺序决定（高→低），高等级成绩下限不低于低等级；
  每等级有成绩/学分下限、全局机动名额。
- 院系名额：`map[等级]map[院系]名额`。
- 学生（`Student`）：平均成绩、本周期学分、荣誉积分、不及格标记、处分列表（含解除时刻）。
- 评定结果（`Result`）：奖励列表（确定性排序）+ 各等级合格者排名快照。

## 快速开始

```go
levels := []scholarship.LevelConfig{
    {ID: "Gold", MinAverage: 90, MinCredits: 20, PoolQuota: 1},
    {ID: "Silver", MinAverage: 80, MinCredits: 15},
}
quotas := map[string]map[string]int{
    "Gold": {"CS": 2, "EE": 1},
}
eng, _ := scholarship.NewEngine(levels, quotas, nil)
_ = eng.AddStudent(scholarship.Student{ID: "s1", Dept: "CS", Average: 92, Credits: 24, Honor: 3})

res, _ := eng.Evaluate(evalTime)          // 评定 / 重评（从零）
aw, err := eng.Confirm("s1")              // 仅可确认当前结果中的奖励
_ = eng.CorrectGrade("s1", 95, 24, 5, false)
_ = eng.RegisterSanction("s1", "discipline-1")
_ = eng.ReleaseSanction("s1", "discipline-1", releaseTime)
eng.SetTracer(logger)                     // 记录每步输入/输出/判定依据
```

## 规则摘要

- 资格：成绩/学分下限取等视为达到；周期内无不及格；无生效中处分（解除时刻不晚于评定时刻即解除）。
  多条不满足只报首条，优先级：处分 > 不及格 > 学分不足 > 平均成绩不足。
- 排序：平均成绩↓ → 学分↓ → 荣誉↓；全同并列、同名次、其后跳号。
- 分配：等级高→低、院系逐一；按名次授予，并列组整组授予或整组跳过；
  跳过时该等级该院系剩余名额全部回流机动池。机动池在全部院系处理完后按等级高→低、
  跨院系同一排序规则授予，同样整组处理，用不完作废。每名学生至多一项。
- 重评：数据变更后从零重评，结果与从零评定完全一致；已确认奖励不被收回，
  原样占用其来源名额（院系或机动池），其余人在剩余名额内分配。

## 错误码（固定优先级）

`ErrInvalidArgument` > `ErrNotFound` > `ErrNotEvaluated` >
`ErrAwardNotInResult` > `ErrAlreadyConfirmed`。被拒绝操作不改动任何状态。

## 验证

```bash
GOCACHE=/tmp/gocache go test -race -v ./scholarship          # 全部场景 + 400 组随机对照
GOCACHE=/tmp/gocache go test -bench . -run '^$' ./scholarship # O(1) 资格 / 重评与历史无关
gofmt -l . && go vet ./...
```

设计取舍见 `DESIGN.md`。
