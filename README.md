# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 弱硬实时距离优先级调度器

调度器位于 `scheduler` 包。任务参数为 `ID`、首次释放 `Phi`、周期 `T`、执行时间 `C`、弱硬实时约束 `(M,K)`；相对截止期等于 `T`。任务窗口长度恒为 `K`，按自旧到新保存最近 `K` 个作业结果，初始全为 `1`。

- 距离 `s`：在窗口末尾连续追加 `s` 个 `0`，只保留最新 `K` 项后，达标数首次小于 `M`。当前达标数已小于 `M` 时 `s=0`。
- 距离例子：`(M,K)=(2,3)` 时，`111、110、101、011、100` 的 `s` 分别为 `2、1、1、2、0`；`M=K` 时窗口全 1 则 `s=1`，否则 `s=0`。
- tick `[t,t+1)` 的固定次序：先判定并立即丢弃 `remaining > d-t` 的作业并追加 `0`；再按 `t>=Phi` 且 `(t-Phi)%T==0` 释放作业；然后按 `(s,d,编号字节序)` 选择作业运行一个 tick；运行后剩余为 0 则追加 `1` 并移除。
- `remaining == d-t` 不丢弃；剩余比 `d-t` 大 1 时丢弃。完成时刻恰为截止时刻仍算达标。
- 每次追加结果后都独立检查窗口，若达标数小于 `M`，`DynamicFailures` 加一。

主要 API：

- `NewScheduler()`：创建调度器。
- `AddTask(TaskSpec)`：仅首次 tick 前可添加；错误优先级为已开始、参数非法、编号重复、任务数满（16 个）。
- `Step(n)`：原子推进 `1..1000000` 个 tick；拆分推进与一次推进的逐 tick 轨迹一致。
- `Window(id)`、`Distance(id)`、`Stats(id)`：查询窗口、距离和达标/未达标/动态失败次数。
- `RunAt(t)`：返回第 `t` 个 tick 运行的任务编号，空闲返回空字符串。

本地验证：

```bash
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -v ./scheduler
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
```

测试包含题目两个轨迹例子、截止期边界、`Phi>0`、动态失败累计、错误优先级、并发调用，以及 2000 组随机任务集与独立朴素模拟器的逐项对拍。`go test -v ./scheduler` 会打印每组随机用例的输入、输出轨迹和判定依据。

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
