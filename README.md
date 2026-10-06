# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 考场排考与调考引擎（`scheduler` 包）

零依赖的考场排考引擎，提供安排 / 移动 / 互换 / 撤销 / 查询，所有操作并发安全，拒绝原因、冲突归因与调考原子性可精确复现。模块划分与设计取舍见 `scheduler/DESIGN.md`。

```go
eng, _ := scheduler.NewEngine(cfg, rooms, proctors) // cfg 含每日时段数、日数、
                                                    // 同日门数上限、最小间隔、
                                                    // 加时比例、监考每日上限
_ = eng.AddExam(scheduler.Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 2})
p, err := eng.Schedule("E1", 0, []string{"R1"})     // 安排
_, err = eng.Move("E1", 3, []string{"R2"})          // 移动（以自身已移除为基准）
_, _, err = eng.Swap("E1", "E2")                    // 原子互换
_ = eng.Cancel("E1")                                 // 撤销
eng.StudentSlots("s1")                               // 升序占用查询
```

错误为 `*scheduler.SchedError`，携带稳定原因码（`invalid_param` /
`exam_already_placed` / `slot_cross_day_or_out_of_range` / `room_slot_busy` /
`capacity_insufficient` / `student_conflict`（含 `Student`、`Category`）/
`proctor_insufficient` / `exam_not_placed`）与不合法考试方 `ExamID`。

测试覆盖加时进位、间隔/门数恰等边界、多考场容量恰等、移动与自身重叠、
互换仅一方不合法、监考每日上限恰满、拒绝优先级逐对验证，并以独立朴素模型
（全量重建 + 回溯匹配）对 60 个随机场景 × 250 步操作做差分对照，
日志打印每步输入、输出与判定依据：

```bash
go test -race -v ./scheduler
go test -v ./scheduler/ -run TestNaiveDifferential -args -verbose-diff
```
