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

## inodeledger：inode 生命周期账本

`inodeledger` 包实现带孤儿链表与两阶段截断的 inode 生命周期账本。
构造参数为块池总块数 `P`、孤儿链表容量 `K`、链接数上限 `L`，任一小于 1 则整体拒绝
（`ErrInvalidArgument`）。所有操作与查询（`Used` / `Orphans` / `InfoOf`）均可并发调用，
内部以互斥锁串行化，结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果与回收次序。

### 生命周期操作

- `Create(b)`：创建占用 `b` 块的 inode，编号从 1 起连续，链接数 1、打开数 0；`已占用 + b > P` 报 `ErrInsufficientSpace`。
- `Link(i)` / `Unlink(i)`：链接数加减 1；超过 `L` 报 `ErrLinkLimit`。
- `Open(i)` / `Close(h)`：打开数加减 1，句柄编号从 1 起连续，`Close` 后句柄失效。
- `Shrink(i, nb)`：立即把块数缩到 `nb` 并归还块池。
- `Used` 为所有存在 inode（含孤儿）的块数之和，恒不超过 `P`。

### 两阶段截断

- `BeginShrink(i, nb)`：登记「待完成截断到 `nb`」，块数与块占用暂不变化，并使 inode 进入孤儿链表。
- `FinishShrink(i)`：把块数缩到登记的 `nb`、归还块池并清除登记。
- 已有登记时再 `Shrink` / `BeginShrink` 报 `ErrShrinkInProgress`；无登记时 `FinishShrink` 报 `ErrNoPendingShrink`。

### 孤儿链表进入与摘除

成员恰为满足「链接数为 0 且打开数大于 0」**或**「有待完成截断登记」的 inode：

- 首次进入时头插（链表头为最近进入者）；已在链表中位置不变，即使同时满足两个条件。
- 两个条件都不再满足时才摘除，其余成员相对次序不变；长度恒不超过 `K`。
- `Unlink` 降到 0：打开数为 0 则立即释放全部块并删除 inode（连同截断登记与链表位置），否则入链表。
- `Close` 使打开数降为 0 且链接数为 0：立即释放块、删除 inode 并摘除。
- `FinishShrink` 后仍是「链接 0 且打开大于 0」则留在链表原位，否则摘除。
- `Unlink` / `BeginShrink` 需要首次进入链表而链表已有 `K` 个成员时报 `ErrOrphanListFull`，已在链表中的 inode 不受此限。

### Crash 回收次序

`Crash()` 模拟断电重启：所有句柄失效、所有打开数清零，然后自链表头（最近进入者）到尾逐个处理——
链接数为 0 的释放全部块并删除 inode（记为「删除」）；链接数不小于 1 的（必有待完成截断）
把块数缩到登记的 `nb` 并保留 inode（记为「截断」）。返回按此次序排列的 `[]CrashRecord`，
之后链表为空、全部登记清除，仅剩链接数不小于 1 的 inode；不在链表中的 inode 不受影响。

### 拒绝顺序

被拒绝的操作不改变任何状态（inode 编号与句柄编号不被消耗），拒绝原因可用 `errors.Is` 区分，
按下列顺序只报第一个：

1. `ErrInvalidArgument`：`b` / `nb` 为负；`Shrink` 与 `BeginShrink` 的 `nb` 大于现有块数（在 inode 存在后才判）。
2. `ErrNotFound`：inode 或句柄从未创建、inode 已删除、句柄已失效。
3. `ErrNoLinks`：`Link` / `Open` / `Unlink` 作用于链接数为 0 的孤儿。
4. `ErrShrinkInProgress`：已有待完成登记时再 `Shrink` / `BeginShrink`。
5. `ErrNoPendingShrink`：`FinishShrink` 作用于无登记的 inode。
6. 各操作自有原因：`ErrLinkLimit`（链接数超过 `L`）、`ErrOrphanListFull`（首次入链表且链表满）、`ErrInsufficientSpace`（块池不足）。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与朴素模型对照
go test ./inodeledger/

# 打印每个随机操作的输入、输出与判定依据
go test -run TestRandomModelComparison -v ./inodeledger/

# 竞态检测
go test -race ./inodeledger/
```
