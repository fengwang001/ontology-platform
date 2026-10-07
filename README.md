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

## 子模块固定与更新协调器（submod 包）

`submod` 实现超级仓库的子模块固定与更新协调：超级仓库在指定路径上把另一个
仓库固定在某个提交，子模块可再嵌套子模块，形成挂载树。

- 模型：`Repo`（提交图 + 分支）、`Commit`（可附带子模块表 `Table`）、
  `Record`（挂载路径、目标仓库、固定提交、可选跟踪分支）。
- 协调器：`NewCoordinator(repos, superID, superBranch)` 载入并校验所有表。
- 状态：`StatusAll()` / `StatusAt(path)` 给出 一致/检出偏离/未提交修改/
  悬空固定/缺失 五种互斥状态。
- 更新：`UpdateToPins()` 递归对齐检出到固定点（脏或悬空则整体拒绝）；
  `AdvanceTracking()` 按跟踪分支快进推进固定点（整批原子）；
  `AddMount` / `RemoveMount` 维护挂载（移除脏挂载需 force）。
- 并发：所有操作可任意并发，结果等价于按返回序号排序的串行执行。

```go
c, _ := submod.NewCoordinator(repos, "S", "main")
c.SetCheckout("libs/a", "a0")          // 模拟工作区事件
statuses, _ := c.StatusAll()           // 查询五态
if _, err := c.UpdateToPins(); err != nil { /* 整体拒绝，无一改动 */ }
if _, err := c.AdvanceTracking(); err != nil { /* 非快进/分支不存在等 */ }
```

设计与取舍见 [DESIGN.md](DESIGN.md)。
