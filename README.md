# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 阅卷分配与双评仲裁引擎（`ontology` 包）

`ontology/` 实现了确定性的评卷任务分配、双评、仲裁与终分判定：

- 两名初评人来自不同评卷组；满足回避、跨题不重复、每日配额约束。
- 候选选择确定性可复现：优先在手未完成任务最少，并列取评卷人 ID 最小，
  选择开销 O(评卷人数)，与答卷数、已完成任务数无关（测试中以扫描计数器证明）。
- 分差大于阈值触发第三组仲裁；平均分按最小步长向满分方向靠拢取格。
- 撤回/停用保留已提交分数并重新分配，退出者不得再被选中；无候选时进入待分配，
  新评卷人可用后由 `TriggerAllocation()`（或新增评卷人）补领。
- 固定优先级错误分类与完整审计；拒绝操作不改动任何状态、不入审计。

关键入口：`New`、`AddGroup/AddQuestion/AddReviewer/AddSheet`、
`Submit/Withdraw/Deactivate`、`TriggerAllocation`、`Snapshot`；
`NaiveEngine` 为独立朴素参考实现，供随机对照测试使用。

详见 `ontology/DESIGN.md`（设计取舍）与包内 doc 注释。

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
