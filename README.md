# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## A/B 双分区固件引导状态机

`firmware.Manager` 管理槽 0 与槽 1，并通过互斥锁保证 `Install`、`Boot`、`Confirm` 与查询可并发调用且等价于某个串行顺序。

### 槽状态

- `Empty`：槽内没有可用固件，初始槽 1 为 `Empty`、版本 0。
- `Good`：固件已确认或仍是当前活动固件；活动槽始终为 `Good`。
- `Trial`：新安装固件等待确认，并保存剩余试启动次数。
- `Bad`：试启动次数耗尽后未确认，回滚时候选槽被标记为 `Bad`。
- 初始构造要求 `v0 >= 1`、`M >= 1`；槽 0 持有 `v0`、为 `Good` 且活动，版本下限 `floor = v0`。

### 操作规则

- `Install(v)` 只写非活动槽；可覆盖 `Empty`、`Good` 或 `Bad`，但不能覆盖正在 `Trial` 的槽。
- `Install(v)` 校验顺序固定为：`v == 0`、`v <= floor`、非活动槽为 `Trial`；被拒绝时不改变任何槽、floor 或计数。
- `Boot()` 永不被拒绝：非活动槽不是 `Trial` 时启动活动槽；非活动槽是 `Trial` 且剩余次数大于 0 时减 1 并试启动。
- 候选槽剩余次数为 0 时，下一次 `Boot()` 将其置为 `Bad`，返回活动槽并标记 `Rollback=true`；因此回滚发生在第 `M+1` 次启动。
- 回滚不改变 `floor`；被置为 `Bad` 的同一版本仍高于 floor 时可重新安装并重新获得 `M` 次试启动。
- `Confirm()` 只在上次启动是试启动时有效；确认后候选槽变 `Good` 并成为活动槽，原活动槽继续保持 `Good`。

### 版本下限与确定性

- `floor` 初始为 `v0`，只在成功 `Confirm` 时提升为候选版本，因此永不下降。
- 只有严格满足 `v > floor` 的安装会成功；`v == floor` 也会被拒绝。
- 任何时刻 `floor` 都等于活动槽版本；处于 `Trial` 的槽版本必然大于 `floor`。
- 相同操作序列重放会得到相同的选中槽、试启动次数、回滚所在启动、槽状态、floor 与错误原因。
- `Snapshot()` 返回槽版本、槽状态、剩余试启动次数、活动槽、floor 及“上次启动是否为试启动”的副本，便于复现与断言。

### 本地验证

```bash
# 如果当前 shell 找不到 go，可使用 /usr/local/go/bin/go
export PATH=/usr/local/go/bin:$PATH
GOCACHE=/tmp/go-cache-ontology go test -v ./firmware
GOCACHE=/tmp/go-cache-ontology go test -race -v ./...
GOCACHE=/tmp/go-cache-ontology go vet ./...
gofmt -l firmware
```

测试使用 `M=1` 和 `M=3` 逐步对照一个独立的朴素模拟，并用详细日志打印每次输入、返回值、拒绝原因与状态判定依据。

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
