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

## 有序快照归并差分（`snapshot` 包）

`snapshot` 包把一份**新快照**与**当前快照**做有序归并，产出按键升序、每键至多一条的变更日志（插入 / 删除 / 更新），日志按序重放后与新快照完全一致。

### 快照校验规则

- 快照是 `[]Entry{{Key, Value}}`，键按字典序**严格升序**。
- 校验从前往后单次扫描，以**第一处违规**决定错误类别：
  - 当前键等于前一个键 → 重复键（`ErrDuplicateKey`）；
  - 当前键小于前一个键 → 未排序（`ErrUnsorted`）。因此把有序输入相邻对调会报“未排序”，而不是“重复键”。
- 差分前先整体校验新、旧两侧快照；任一非法都不会产出任何变更。

### 归并规则（双指针）

- 两侧各一个指针从头开始比较键：
  - 仅旧侧有 → 输出 `delete`；
  - 仅新侧有 → 输出 `insert`；
  - 两侧都有且值不同 → 输出 `update`；
  - 两侧都有且值相同 → 不输出；
  - 任一侧耗尽后，另一侧剩余条目依次输出（尾部剩余的删除 / 插入）。
- 变更日志天然按键升序，且每个键至多出现一条。
- `Replay(oldSnap, changes)` 把日志按序应用回旧快照，结果与新快照逐元素相等，因此结果可复现。

### 边界与错误类别

| 类别哨兵 | 触发条件 |
| --- | --- |
| `snapshot.ErrInvalidConfig` | `Config.MaxChanges < 0`（`0` 表示不限制） |
| `snapshot.ErrDuplicateKey` | 快照扫描到相邻相等键 |
| `snapshot.ErrUnsorted` | 快照扫描到相邻逆序键 |
| `snapshot.ErrTooManyChanges` | 归并产出的变更条数超过 `MaxChanges` |

- 四类错误互不相同、可区分，统一为 `*snapshot.DiffError`，可用 `errors.Is(err, snapshot.ErrUnsorted)` 等匹配。
- **失败不留痕**：任一类拒绝都不会改变当前快照，也不会改变累计统计（`Applied/Inserted/Deleted/Updated`）；拒绝时不返回部分日志。
- `MaxChanges` 为严格上限：变更数 `== MaxChanges` 允许通过，`>` 立即拒绝。

### 并发与一致性

- `snapshot.Store` 用不可变快照 + 原子指针持有当前版本：
  - `Snapshot()` / `Stats()` / `Verify()` 可在另一执行体差分期间被并发调用；
  - 每个读者拿到的整份快照都等于某一时刻的完整版本，绝不会读到新旧混合；
  - 返回的是副本，调用方修改不影响内部状态；
  - 多个写者串行提交，后提交者基于最新快照重新归并。

### 逐步日志

通过 `snapshot.WithLogger(ctx, logger)` 注入任意 `Printf` 风格日志器，归并过程会打印：

- 每步输入（双指针位置、两侧键值）；
- 每条变更（`insert` / `delete` / `update`）及其判定依据（仅旧有、仅新有、同键改值、值相同不变）；
- 尾部剩余处理与拒绝原因。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./...

# 仅跑快照包
go test -race -v ./snapshot

# 覆盖率
go test -coverprofile=coverage.out ./snapshot
go tool cover -html=coverage.out
```

测试覆盖：尾部剩余（新侧插入 / 旧侧删除）、值相同不输出、对调输入报未排序、四类非法输入及拒绝后快照与统计不变、重放等价、与朴素 map 参照实现一致、200 组随机模糊对照、读写并发版本一致性，以及逐步日志内容校验。
