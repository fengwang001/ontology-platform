# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 带可见性映射的堆表

根包 `heap` 提供按页存储的堆表、有序索引、每页全可见位和仅索引扫描：

- `NewHeap(P, C)`：创建 `P` 页、每页 `C` 个固定槽；`P<1` 或 `C<1` 返回 `ErrInvalidSize`。
- `Insert(key, xid)`：放入编号最小页中的最小空槽，同时按 `(key, page, slot)` 登记有序索引，并清除该页全可见位。
- `Delete(page, slot, xid)`：给槽内行写入非零 `xmax`，并清除该页全可见位；不会立即释放槽位或删除索引项。
- `Vacuum(page, h)`：物理移除该页满足 `xmax!=0 && xmax<h` 的行及其索引项，随后重算该页全可见位。
- `Scan(lo, hi, snapshotID)`：按键、页、槽升序扫描 `lo <= key < hi`，返回行、回表次数和免回表次数。

### 全可见位条件

全可见位只在成功的 `Vacuum(page, h)` 后重算：

- 置真：该页物理清理后的每一行（包括空页的零行）都满足 `xmin < h && xmax == 0`。
- 置假：仍有任一行满足 `xmin >= h` 或 `xmax != 0`；`xmax == h` 的行不会被移除，因此位为假。
- 清除：任何成功的 `Insert` 或 `Delete` 都把目标页位清除。即使删除事务号不小于当前全局最大已用 `h`，Delete 也会保守清位。

全局最大已用 `h` 只在 Vacuum 成功后按 `max(h)` 更新。不变量是：任何时刻全可见页上的所有现存行都满足 `xmin < globalH && xmax == 0`。

### 快照与 Vacuum

- `Snapshot(s)` 要求 `s>0` 且 `s >= globalH`；被拒绝时不分配快照编号。
- 成功快照返回从 1 开始连续递增的编号，编号只有在 `Release(id)` 后注销。
- `Vacuum(page, h)` 要求 `h>0`、页号合法，并且不存在仍注册且值满足 `s<h` 的快照；因此 `s==h` 允许，`s==h-1` 拒绝。
- Vacuum 被拒绝时不移除行、不改全可见位、不推进全局 `h`。

### 扫描与计数

对索引范围内的每个现存条目：

- 条目所在页全可见位为真：直接产出索引中的 `(key, page, slot)`，`IndexOnlyReads` 加 1，不回表。
- 全可见位为假：`HeapFetches` 加 1，并读取堆行按朴素可见性判定：`xmin < s && (xmax == 0 || xmax >= s)` 才产出。

因此回表次数严格等于扫描范围内、位于全可见位为假页上的索引条目数。全可见页免回表的产出集合与这些行总是回表后的产出集合一致。

所有公开方法由同一个互斥保护，调用虽然可以并发发起，但结果等价于某个合法的串行执行顺序。

### 错误

各操作按参数和状态检查返回可区分的哨兵错误，顺序与方法文档一致：

- 构造：`ErrInvalidSize`
- 插入：`ErrInvalidTransactionID`、`ErrNoEmptySlot`
- 删除：`ErrInvalidTransactionID`、`ErrInvalidPage`、`ErrInvalidSlot`、`ErrEmptySlot`、`ErrAlreadyDeleted`
- 快照：`ErrInvalidSnapshotValue`、`ErrSnapshotTooOld`
- 释放：`ErrUnknownSnapshot`
- 清理：`ErrInvalidVacuumHorizon`、`ErrInvalidPage`、`ErrActiveSnapshotTooOld`
- 扫描：`ErrInvalidKeyRange`、`ErrUnknownSnapshot`

被拒绝的操作不会改变堆、索引、全可见位、快照编号或全局 `h`。

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

### 本地验证堆表语义

```bash
# 普通全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机序列的输入、输出与判定依据（日志较多）
go test -run TestRandomSequencesMatchAlwaysFetchOracle -v ./...
```

随机测试使用固定种子，比较真实实现与独立的“总是回表”朴素模型：逐项比较产出集合、错误、回表次数、免回表次数，并验证回表次数等于扫描到的非全可见页条目数。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
