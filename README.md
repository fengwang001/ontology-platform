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

## 分区重均衡迁移器（`rebalance` 包）

当分区数 `N` 改变时，逐条迁移归属改变的记录，支持迁移期双路由、断点续迁与可复现校验。

### 归属计算

- 键 `k` 的归属分区固定为 `FNV-1a64(k) mod N`（见 `HomePartition`），哈希与取模均确定，结果只依赖键本身，可独立复现。
- `BeginRebalance(target)`：扫描现有分区，收集 `FNV-1a64(k) mod target != 当前分区` 的键，按键排序得到迁移清单，游标置 0，进入 `migrating` 态。扩容时预先创建新分区；缩容时数据全部迁出后多余分区保留为空。

### 逐条迁移与双路由

- `MigrateSteps(max)`：按排序清单逐条执行「复制到新归属分区 → 从旧分区删除 → 游标 +1」；`max<=0` 表示迁完剩余全部。任一时刻每条记录恰存在于一个分区。
- 迁移期间（`phase=migrating`），键的**当前所在分区**由清单下标与游标判定，读 (`Get`) 与写 (`Put`) 都路由到该分区：
  - 在清单中且下标 `i < cursor`（已迁）：新归属 `FNV-1a64(k) mod target`
  - 否则（未迁，或本就不需迁移）：旧归属 `FNV-1a64(k) mod N`
- 迁移期写入**全新键**整体拒绝（`ErrNewKeyWhileMigrating`）；更新已存在键允许，且同样按游标路由，迁移后值不丢。
- `Commit()` 仅当 `cursor == len(list)` 时把活动分区数切换为 `target`；未迁完返回 `ErrMigrationIncomplete`。

### 断点续迁

- `Snapshot()` 序列化分区内容、`n`、`target`、`phase`、迁移清单与游标；新进程用 `New` + `Restore` 恢复。
- 路由完全由「清单下标 + 游标」推导，因此恢复后已迁键按新布局读、未迁键按旧布局读；继续 `MigrateSteps` 只处理游标之后的后缀，已迁键不重复、未迁键不跳过。

### 失败原子性（可区分的错误原因）

| 场景 | 返回错误 |
| --- | --- |
| 目标分区数 `<1` 或与当前相同 | `ErrInvalidPartitionCount` |
| 迁移中再次 `BeginRebalance`（参数无论合法与否） | `ErrRebalanceInProgress` |
| 非迁移期调用 `MigrateSteps`/`Commit` | `ErrNotMigrating` |
| 未迁完就 `Commit` | `ErrMigrationIncomplete` |
| 迁移期写入此前不存在的键 | `ErrNewKeyWhileMigrating` |

任何被拒绝的调用都不修改记录、分区、游标与迁移态（测试通过失败前后快照字节级比对验证）。

### 并发语义

全库单一 `sync.RWMutex`：迁移/写入持写锁，`Get`/`Progress` 持读锁。因此每个读都等价于在某个游标位置的单线程顺序读；游标只增不减，`Progress` 单调。

### 测试与本地核对

```bash
GOCACHE=/tmp/gocache go test -race -v .
```

用例覆盖：双路由读写（`TestDualRouteReadsAndWrites`）、断点续迁与恰好一次（`TestExactlyOnceAndResume`）、未迁完提交及各类非法操作的原子性（`TestCommitBeforeCompleteRejected`、`TestInvalidTargetsAreAtomic`）、并发读与单线程读一致（`TestConcurrentReadsMatchSequential`）、进度单调（`TestProgressMonotonic`）、缩容（`TestShrinkRebalance`）。

测试日志对每次 `GET/PUT/MIGRATE/BEGIN/COMMIT` 打印操作、各分区键列表、`cursor/total` 以及判定依据（`old=hash%N`、`new=hash%target`、`inList/index/moved`）。

**重算归属归位核对**：测试辅助 `assertPlaced` 独立用 `FNV-1a64(k) mod N` 重算每个键的期望分区，断言：

1. 键确实出现在期望分区；
2. 全库只出现一次（无残留副本、无丢失）。

自己核对时可在提交后遍历 `Partitions()`，对每个键调用 `HomePartition(key)`（等价于 `FNV-1a64(k) mod N`）比对所在分区，并校验键总数与迁移前一致。
