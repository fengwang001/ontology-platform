# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## COW 快照空间账本

根包提供 `Ledger`，用块的出生事务号 `b`、死亡事务号 `d` 与快照事务号精确计算写时复制快照占用。

- 初始当前事务号 `cur=1`；`Alloc(size)` 创建 `b=cur,d=∞` 的块，成功后块编号从 1 连续增加。
- `Free(id)` 将仍存活块的 `d` 设为当前 `cur`；块在事务 `t` 存活当且仅当 `b <= t < d`。
- `Snapshot(name)` 在 `t=cur` 记录快照并立即将 `cur` 加 1；因此同事务先 `Free` 的块不进入快照，快照后再 `Free` 的块进入快照。
- `Referenced(name)` 是满足 `b <= t_name < d` 的全部块大小之和。
- 一个块被持有，当且仅当它仍存活（`d=∞`）或至少被一个未销毁快照包含。
- `Used` 是全部被持有未丢弃块的大小之和；`Alloc` 接受 `Used+size <= Cap`，相等允许。
- `Unique(name)` 是此刻立即销毁该快照会释放的字节：块必须 `d` 有限，并且包含它的未销毁快照只有 `name`。
- `Hold(name)`/`Release(name)` 维护独立持有计数；计数为 0 时 `Release` 返回 `ErrNotHeld`。
- 持有计数大于 0 的快照不能 `Destroy`；`Rollback` 不允许删除任何更晚事务号的被持有快照，但目标快照自身被持有不阻止回滚。
- `Rollback(name)` 原子删除所有 `t > t_name` 的未销毁快照，丢弃所有 `b > t_name` 的块，并将目标快照包含块的 `d` 恢复为 `∞`；`cur` 不变。
- 被回滚丢弃的编号永久占号且返回 `ErrBlockDiscarded`；从未分配到的编号返回 `ErrBlockNotFound`。销毁后的快照名可重新使用。

### API 与错误

构造函数为 `New(capacity int64) (*Ledger, error)`，容量必须满足 `1 <= Cap <= 2^50`。块大小必须满足 `1 <= size <= 2^40`。

所有错误均为可通过 `errors.Is` 匹配的哨兵错误：

- `ErrInvalidArgument`：容量或块大小越界、快照名为空、`Free(id)` 的 `id < 1`。
- `ErrOutOfSpace`：参数合法但 `Used+size > Cap`。
- `ErrBlockNotFound`、`ErrBlockDiscarded`、`ErrBlockDead`：分别对应编号从未分配、已被回滚丢弃、已死亡。
- `ErrSnapshotExists`、`ErrSnapshotMissing`：快照重名或名字不存在。
- `ErrNotHeld`、`ErrHeld`：释放计数为 0，或销毁/回滚路径上存在持有阻止。

拒绝按题目优先级只返回第一个错误，并且不修改编号、事务号、块状态、快照或持有计数。所有变更与查询都由单一读写锁串行化，因此并发结果等价于某个合法串行顺序，且回滚对查询原子可见。

### 测试模型

`TestRandomizedOracle` 重放 2000 组固定种子随机操作序列。测试中的朴素模型对每个块和快照逐条扫描，按定义重算持有关系、`Used`、`Referenced`、`Unique`、存活字节和持有计数，并与真实实现逐步比较；`-v` 日志包含每步输入、输出、错误、事务号和判定依据。

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

# 查看随机模型逐步判定日志
go test -v -run TestRandomizedOracle ./...

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
