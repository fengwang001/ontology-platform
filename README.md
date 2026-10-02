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

## 检查点纪元拆分分配器

`Allocator` 用检查点纪元协调动态拆分发现、读取器失败恢复与完成状态撤销。

### 纪元与检查点

- `lt` 是最近已触发的检查点，初值为 `0`；当前分配纪元始终为 `E = lt + 1`。
- `done` 是最近已完成的检查点，初值为 `0`，并始终满足 `done <= lt`。
- `Checkpoint(cp)` 只接受 `cp == lt + 1`，触发后纪元立即加一，不等待 `Complete`。
- `Complete(cp)` 只接受 `done < cp <= lt`。已触发但尚未完成的检查点不属于恢复快照。
- 新分配写入 `at = E`，完成写入 `ft = E`。

### 失败后的归还、保留与撤销

`ReaderFailed(r)` 是单个原子步骤，按拆分编号升序处理读取器 `r` 名下所有 `ASSIGNED` 或 `FINISHED` 拆分：

- `at > done`：分配晚于最近可恢复快照，恢复出的状态中没有该拆分。清空属主、`ret++`，然后回到 `UNASSIGNED`；若 `ret == A`，改为终态 `QUARANTINED`。
- `at <= done` 且状态为 `FINISHED`、同时 `ft > done`：拆分本身存在于快照中，但完成不存在；保留属主并撤销为 `ASSIGNED`。
- `at <= done` 且 `ft <= done` 的已完成拆分保持 `FINISHED`；`at <= done` 的未完成拆分保持 `ASSIGNED`。

因此边界为：`at == done` 保留，`at == done + 1` 归还；`ft == done` 保留完成，`ft == done + 1` 撤销完成。

### 偏好、偷取、等待与结束

- 拆分 `s` 的偏好读取器是 `s mod R`。
- `RequestSplit(r)` 先选偏好为 `r` 的最小 `UNASSIGNED` 拆分。
- 偏好池为空时，只从偏好读取器当前已失败的 `UNASSIGNED` 拆分中选全局最小者偷取。
- 已失败读取器名下保留的 `ASSIGNED` 或 `FINISHED` 拆分不可偷取，也不可重新分配，直到该读取器重启。
- 没有候选时，只有 `sealed == true`、全局没有 `UNASSIGNED` 且没有任何 `ASSIGNED` 才返回无更多拆分；否则返回等待。

### 隔离次数

`ret` 只在 `ReaderFailed` 处理 `at > done` 的拆分时增加一次。达到阈值 `A` 后进入 `QUARANTINED`，之后不再参与分配；同一编号即使处于隔离态，也不能再次通过 `AddSplits` 登记。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...

# 竞态检测
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...

# 随机朴素模型对照（包含输入、输出与判据日志）
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -run TestRandomSequencesAgainstNaiveModel -v ./...
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
