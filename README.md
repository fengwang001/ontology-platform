# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 延迟分配空间账本

`ledger.Ledger` 是并发安全的延迟分配空间账本。构造参数为：

- `T`：总块数，必须满足 `T >= 1`。
- `S`：特权保留块数，必须满足 `0 <= S <= T`。
- `E`：每个索引块可映射的数据块数，必须满足 `E >= 1`。
- `W`：全局脏水位，必须满足 `W >= 0`。

对任意块数 `P`，索引块数为 `m(P)=ceil(P/E)`，并定义 `m(0)=0`。文件状态为已分配数据块数 `A` 与延迟数据块数 `D`。全局已占用空间：

```text
U = Σ(A + m(A))
F = T - U
```

单个文件的预留量为：

```text
D + m(A + D) - m(A)
```

总预留 `R` 是所有文件预留量之和。`Write(f,n,priv)` 先在写入前状态计算：

```text
δ = n + m(A + D + n) - m(A + D)
```

非特权写入要求 `δ <= F-R-S`，特权写入要求 `δ <= F-R`，恰等允许。检查通过后只增加延迟块和预留，不立即占用数据块。

`Flush(f)` 把 `D` 个延迟块转为已分配块。刷写导致的占用增量正好是刷写前该文件的预留量：

```text
ΔU = D + m(A + D) - m(A)
```

同时该预留量从 `R` 中移除，因此刷写前后的 `F-R` 不变，特权与非特权 `Avail` 也不变。这就是 `Flush` 不需要再次做空间检查的原因：空间在写入时已经完成预留。

`Truncate(f,k)` 先从延迟块末尾删除，不足部分再从已分配块删除，并按新的 `A,D` 重算占用和预留。`Unlink(f)` 删除文件并释放其占用与预留；之后同编号文件再次写入会视为新文件。

查询接口为：

- `Free() = F`
- `Reserved() = R`
- `Avail(false) = max(0, F-R-S)`
- `Avail(true) = max(0, F-R)`
- `Dirty()`：按脏序号升序返回仍有延迟块的文件编号

## 脏序与自动回写

全局写入计数器初始为 0。每次成功 `Write` 先把计数器加 1：

- 如果写入前该文件 `D=0`，文件取得新的计数器值作为脏序号。
- 如果写入前 `D>0`，追加写入不会刷新原脏序号。
- 成功写入后若所有文件 `D` 之和严格大于 `W`，反复刷写脏序号最小的完整文件，直到 `D` 之和不大于 `W`。
- `D` 之和恰等于 `W` 不触发回写；`W=0` 时每次写入后都会刷完。
- 刚写入的文件也可能被自动回写；`Write` 返回自动刷写文件编号的有序序列。
- `Flush`、`Truncate`、自动回写使某文件 `D=0` 时清除脏序号；`Unlink` 同样清除。`Truncate` 只减少但未清零 `D` 时保留脏序号，也不触发自动回写。

所有操作由互斥保护，一次写入及其触发的全部自动回写对其他调用原子可见，结果等价于某个合法串行顺序。

## 拒绝顺序

操作被拒绝时不改变任何状态：不创建文件、不改变空间账本、不推进写入计数器、不触发自动回写。按以下顺序只返回第一个错误，可用 `errors.Is` 判断：

- 参数非法：文件编号为负，或写入/截断块数不大于 0，返回 `ledger.ErrInvalidArgument`。
- 文件不存在：`Flush`、`Truncate`、`Unlink` 指向从未成功写入或已删除的编号，返回 `ledger.ErrFileNotFound`。
- `Flush` 时文件 `D=0`，返回 `ledger.ErrNoDelayedBlocks`。
- `Truncate` 的 `k>A+D`，返回 `ledger.ErrFileTooLarge`。
- 写入预留 `δ` 超过写入前可用额度，返回 `ledger.ErrOutOfSpace`。

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
go test ./...

# 带竞态检测与详细输出（随机对照会打印输入、输出和判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 只运行 2000 组朴素模型随机序列对照
go test -run TestRandomOperationsMatchNaiveModel -v ./ledger

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

如果环境中没有全局 `go` 命令，可使用 `/usr/local/go/bin/go`；若默认 Go 缓存目录只读，可设置 `GOCACHE=/tmp/go-build-ontology`。
