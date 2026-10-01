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

## inode 生命周期账本

`NewLedger(P, K, L)` 创建容量分别为 `P` 个块、`K` 个孤儿成员、`L` 个硬链接的线程安全账本；任一参数小于 1 都会返回 `ErrInvalidArgument`。

### 孤儿链表

孤儿链表成员恰好是满足以下任一条件的 inode：

- 链接数为 0 且打开数大于 0。
- 存在尚未完成的两阶段截断登记。

成员第一次满足条件时头插；已经在链表中的 inode 即使同时满足两个条件，位置也保持不变。成员不再满足任一条件时摘除，摘除不改变其他成员的相对次序。链表长度始终不超过 `K`；只有让新 inode 首次进入链表的操作会收到 `ErrOrphanListFull`，已在链表中的 inode 不受容量限制。

`Unlink` 把链接数减到 0 时：

- 打开数为 0：立即归还全部块、删除 inode，并清除截断登记和链表位置；相关旧句柄在后续 `Close` 时按不存在处理。
- 打开数大于 0：保留 inode 与块占用，并按需头插到孤儿链表。

`Close` 只会使对应句柄失效。当最后一个句柄关闭且链接数已经为 0 时，inode 立即删除并释放块。

### 两阶段截断

- `Shrink(i, nb)` 立即把块数缩到 `nb` 并归还差额，`nb` 必须在 `[0, 当前块数]`。
- `BeginShrink(i, nb)` 只登记目标块数，不改变当前块数和 `Used()`，同时使 inode 进入孤儿链表。
- `FinishShrink(i)` 执行登记的截断、归还差额并清除登记。完成后若 inode 仍为“链接数 0、打开数大于 0”，则保留在链表原位；否则摘除。

### Crash 恢复

`Crash()` 模拟断电重启：

- 所有句柄立即失效，所有打开数清零，句柄编号从 1 重新开始。
- 从链表头到尾依次处理成员；头是最近进入者，所以三个依次进入的孤儿按进入次序的反序回收。
- 链接数为 0 的成员释放全部块并删除，动作记录为 `CrashDelete`。
- 链接数至少为 1 的成员一定存在待完成截断；账本执行登记截断、保留 inode，动作记录为 `CrashTruncate`。
- 恢复结束后链表为空、所有截断登记清除；不在链表中的 inode 不处理。

### 拒绝顺序

操作被拒绝时不消耗 inode 编号或句柄编号，也不改变任何状态。哨兵错误可用 `errors.Is` 区分，检查顺序如下：

1. `ErrInvalidArgument`：负块数，或存在 inode 后发现 `nb > 当前块数`。
2. `ErrNotFound`：inode 从未创建、已经删除，或句柄从未创建或已经失效。
3. `ErrNoLinks`：对链接数为 0 的孤儿执行 `Link`、`Open`、`Unlink`。
4. `ErrShrinkInProgress`：已有截断登记时再执行 `Shrink` 或 `BeginShrink`。
5. `ErrNoPendingShrink`：无登记时执行 `FinishShrink`。
6. 操作专有错误：`ErrLinksFull`、`ErrOrphanListFull`、`ErrNoSpace`。

所有方法和查询均由同一把互斥锁保护，调用结果等价于某个合法串行顺序。

### 本地验证

常规验证：

```bash
go test -v ./...
go test -race ./...
go vet ./...
```

随机模型对照会运行 2000 组操作序列；失败日志包含每个 trial 的输入、实际/朴素模型输出、Crash 判定和状态比较依据：

```bash
go test -run TestRandomSequencesAgainstNaiveModel -v ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
