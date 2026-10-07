# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 作业结算引擎

根包 `ontology` 实现作业提交截止、延期与迟交扣分结算引擎，设计取舍见 `DESIGN.md`。

- `Engine`：管理多次作业，所有操作携带单调递增的整数时刻，单锁串行化，并发安全。
- `CreateAssignment`：统一截止、硬性关闭、递增迟交档位、小组开关、迟交覆盖开关。
- `Submit`：产生连续无洞的版本号；恰等于截止为准时，晚于硬性关闭拒绝。
- `GrantExtension` / `RevokeExtension`：按个人或小组授予/撤销，取最大延长时长，
  不溯及已产生的版本；超出硬性关闭的授予被拒绝。
- `JoinGroup` / `LeaveGroup`：首次提交后方可加入会被拒绝；退出者结算冻结在退出时刻。
- `DesignateVersion`：显式指定评分版本，优先于默认规则；指定无效版本被拒绝。
- `Settle`：关闭后结算并冻结全部状态，重复结算报已结算。
- 错误分类固定优先级：参数非法 > 时钟回退 > 不存在 > 已结算 > 已关闭 >
  状态不允许 > 延期超界 > 版本无效。

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
