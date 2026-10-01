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

## 延迟分配空间账本

`Ledger` 用一个互斥锁串行化所有状态变更和查询，保证一次 `Write` 及其触发的全部自动回写对其他调用原子可见。

### 块与索引块

参数：

- `T`：总块数，必须满足 `T >= 1`。
- `S`：非特权写入不能使用的特权保留块，必须满足 `0 <= S <= T`。
- `E`：每个索引块能映射的数据块数，必须满足 `E >= 1`。
- `W`：允许的总延迟脏数据块水位，必须满足 `W >= 0`。

文件状态为已分配数据块数 `A` 与延迟数据块数 `D`。对任意块数 `P`：

```text
m(P) = ceil(P / E)，且 m(0) = 0
U    = sum(A + m(A))
F    = T - U
R    = sum(D + m(A + D) - m(A))
```

一次 `Write(f, n, priv)` 的新增预留量是：

```text
delta = n + m(A + D + n) - m(A + D)
```

非特权写入要求 `delta <= F - R - S`，特权写入要求 `delta <= F - R`。两边恰等时成功；空间检查只看写入前状态，发生在自动回写之前。

### Flush 与 Truncate

`Flush(f)` 将该文件的全部 `D` 转为 `A`。刷写前该文件的预留为：

```text
D + m(A + D) - m(A)
```

刷写后已占用增加量为：

```text
(A + D + m(A + D)) - (A + m(A))
= D + m(A + D) - m(A)
```

因此 `U` 的增加量恰等于刷写前预留量：`F` 与 `R` 同步减少相同数量，特权与非特权 `Avail` 都不变。这些块在 `Write` 成功时已经完成额度预留，所以 `Flush` 不需要也不会再做空间检查。

`Truncate(f, k)` 先从 `D` 中删除，最多删除 `D` 个；剩余的 `k` 再从 `A` 中删除。截断后按新的 `A`、`D` 重新计算占用与预留；`D` 未归零时保留原脏序号，`D` 归零才清除脏序号。截断不会触发自动回写。`Unlink(f)` 删除文件并释放其全部占用和预留；同编号之后再次成功写入会得到新文件。

### 脏序号与自动回写

全局写入计数器初始为 0。每次成功 `Write` 先把计数器加 1：

- 若写入前该文件 `D = 0`，文件脏序号取新的计数值。
- 若写入前该文件 `D > 0`，追加保留原脏序号，不会刷新到最新。
- `Flush`、使 `D` 归零的 `Truncate`、自动回写和 `Unlink` 都清除脏序号。

成功写入后，如果所有文件的 `D` 之和严格大于 `W`，反复选择脏序号最小的文件完整 `Flush`，直到总延迟块数不大于 `W`。等于水位不触发；刚写入的文件也可能被刷；`W = 0` 时每次写入后所有脏文件都会刷完。`Write` 返回自动回写文件编号序列，`Dirty()` 返回当前 `D > 0` 的文件并按脏序号升序排列。

### 拒绝顺序

各操作按以下顺序只返回第一个错误，拒绝时不修改文件、占用、预留或写入计数器：

1. 参数非法：`f < 0`，或 `Write` 的 `n <= 0`，或 `Truncate` 的 `k <= 0`。
2. 文件不存在：`Flush`、`Truncate`、`Unlink` 的文件从未成功写入或已被删除。
3. 操作自有错误：
   - `Flush`：`D = 0`，返回 `ErrNoDelayedBlocks`。
   - `Truncate`：`k > A + D`，返回 `ErrFileTooLarge`。
   - `Write`：`delta` 超过对应用户额度，返回 `ErrNoSpace`。

构造参数非法返回 `ErrInvalidArgument`。所有错误均为哨兵错误，可用 `errors.Is` 判断。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机操作序列的输入、输出、朴素模型判定依据
go test -v -run TestRandomSequencesAgainstNaiveModel ./...

# 代码检查
gofmt -l .
go vet ./...
```

若当前环境的 Go 构建缓存目录只读，可指定临时缓存：

```bash
GOCACHE=/tmp/ontology-go-cache go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
