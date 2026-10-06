# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 团队看板卡片流转服务（`kanban` + `naive`）

实现多人并发拖拽下的卡片流转：在制品（列/负责人 G）上限、前置依赖、
唯一加急例外、乐观版本号与单调时钟，拒绝原因严格按固定次序上报。

### 包结构

- `kanban`：生产实现（`Board` 无锁内核 + `Service` 单锁线性化 + 操作日志）。
- `naive`：独立朴素参考模型（全量重算占用），供差分测试对照。
- `DESIGN.md`：关键取舍、被放弃方案、复杂度论证与本地验证方法。

### 快速上手

```go
svc, _ := kanban.NewService(kanban.Config{
    Columns:    []string{"todo", "dev", "qa", "done"}, // 3..8 列
    Limits:     []int{0, 3, 2, 0},                     // 0 = 不限；首尾列必须 0
    OwnerLimit: 5,                                      // G：每人进行中合计 1..50
}, logWriter) // logWriter 可为 nil；非 nil 时逐行打印 输入/输出/判定依据

svc.AddCard("c1", "alice", 100)
svc.AddDep("u", "c1", "c0", 1, 101)          // c1 的前置 c0
card, err := svc.Move("u", "c1", 1, expectVer, expedite, now)
svc.Reopen("u", "c1", expectVer, now)
svc.ChangeOwner("u", "c1", "bob", expectVer, now)
svc.SetColumnLimit(1, 2, now)
```

错误码（拒绝只报第一个，次序从严到宽）：
`invalid_argument > clock_rollback > card_not_found > version_conflict >
illegal_flow > dependency > expedite_busy > column_full > owner_full`。

### 测试

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test ./...                                      # 全量（含 1500 组随机差分）
go test -race ./...                                # 含并发竞态检测
go test -run TestDifferential1500 -v ./kanban       # 差分：1500 组随机序列
go test -bench BenchmarkMoveIndependence -run '^$' ./kanban
```

差分逐操作日志输出到 `kanban/diff_trace.log`（输入、双方输出与判定原因）。

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
