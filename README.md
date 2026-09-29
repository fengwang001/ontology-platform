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

## 一致性哈希重均衡迁移器（`rebalance` 包）

分区数变化时把需要迁移的记录逐条迁移，支持断点续迁与迁移期双路由。

### 归属计算

- 键的归属完全确定且可复现：`partition(key, n) = FNV-1a-64(key) mod n`，
  见 `rebalance/hash.go`，导出为 `rebalance.OwnerOf(key, n)`。
- 记录只允许存放在按当前规则重算出的归属分区中（“各居其位”）。
- `StartRebalance(targetN)` 扫描旧分区，凡 `owner(key, oldN) != owner(key, newN)`
  的键进入迁移清单；清单按键字典序排序（跨进程、跨实例字节级一致），
  游标 `cursor` 记录已迁移条数，`plan[:cursor]` 已在新归属，其余在旧归属。

### 逐条迁移与提交

- `Step()` 每次恰好迁移一条：先把记录**复制**到新归属分区，再从旧归属
  **删除**，最后游标加一。复制成功前不删除，因此迁移中途记录不会丢失。
- 清单全部迁完前，对外生效的分区数仍是旧值；只有 `Commit()` 在
  `cursor == len(plan)` 时才允许切换分区数并清空迁移态。
- 清单为空（分区数不变或无键需要移动）时可直接提交。

### 迁移期双路由

迁移期间键的“当前所在分区”由游标判定（`currentOwnerLocked`）：

- 键在清单中且下标 `< cursor`：已迁移，路由到 `owner(key, newN)`；
- 键在清单中且下标 `>= cursor`：未迁移，路由到 `owner(key, oldN)`；
- 键不在清单中：旧新归属相同，路由结果不变。

读（`Get`）与写（`Put`）都使用同一判定，因此更新总是落在当前所在分区，
迁移后读到的就是最新值。迁移期写入**全新键**会被整体拒绝
（`ErrNewKeyDuringMigration`），因为它没有“当前所在分区”且会使清单失效；
提交后恢复正常写入。

### 断点续迁

- `Snapshot()` 在锁内一次性导出全部记录、旧/新分区数、清单与游标；
  `Restore()` 恢复并重算校验所有不变量：
  - 清单必须恰好等于“归属改变”的键集合且按键排序；
  - `plan[:cursor]` 的键必须位于新归属，`plan[cursor:]` 必须位于旧归属；
  - 每条记录全局唯一（不会同时出现在两个分区），非清单键必须在旧归属。
- 校验失败则整体拒绝恢复，绝不返回半恢复状态。
- 恢复后继续 `Step()`：已迁移的键不会重复迁移，未迁移的键不会跳过。

### 失败的可区分原因（错误均为哨兵错误，可用 `errors.Is` 判定）

| 错误 | 触发场景 |
| --- | --- |
| `ErrInvalidPartitionCount` | 新建或重均衡目标分区数 `< 1` |
| `ErrRebalanceInProgress` | 迁移进行中再次发起重均衡 |
| `ErrNotMigrating` | 非迁移期调用 `Step` 或 `Commit` |
| `ErrMigrationIncomplete` | 清单未迁完就提交（含缩容后高位分区仍非空） |
| `ErrNewKeyDuringMigration` | 迁移期写入此前不存在的全新键 |

任何一次失败都发生在状态修改之前（或修改后立即回滚路径不可达），
记录、分区、游标与迁移态保持原样。

### 并发语义

`Get`/`Put`/`Progress`/`CurrentOwner`/`Snapshot` 使用读写锁保护，
可与迁移并发调用：迁移期并发读同一批键与“单线程在同一游标快照下
逐键顺序读”的结果一致；`Progress().Cursor` 单调不减。

### 本地验证方法

```bash
# 竞态检测 + 详细日志（日志含操作、各分区内容、游标与判定依据）
go test -race -v ./rebalance
```

重算归属归位核对（测试中已内置）：

1. 迁移完成并提交后，对每个键重算 `OwnerOf(key, newN)`，断言记录就在该分区；
2. 迁移中途任一刻，按游标判定依据（下标 `< cursor` 取新归属，否则旧归属）
   断言每条记录恰好位于其当前所在分区，且全局只出现一次；
3. 统计所有分区记录总数，断言迁移前后不变（恰好迁移一次，无重复无丢失）。
