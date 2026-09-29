# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 有序快照归并差分（`ontology` 包）

`ontology/` 包提供两份**按键严格升序**的键值快照之间的归并差分，产出
插入 / 删除 / 更新变更日志；日志按序重放后与新快照完全一致。

### 快照校验规则（`Validate`）

从前往后单次扫描，在**第一处违规**处停止，并按以下优先级判定错误类别：

1. 当前位置键为空 → `ErrEmptyKey`；
2. 当前键与前一键相等 → `ErrDuplicateKey`（重复键优先于“未排序”）；
3. 当前键小于前一键 → `ErrUnsorted`。

例如把升序输入对调成降序（`c,b,a`），第一处违规是 `c > b`，报“未排序”
而不是“重复键”。空切片与 `nil` 均为合法快照。

### 归并规则（`MergeDiff`）

双指针从两侧头部同步推进，比较当前键：

| 情形 | 输出 |
| --- | --- |
| 仅旧快照有键 | `delete`（记录旧值） |
| 仅新快照有键 | `insert`（记录新值） |
| 两侧都有、值不同 | `update`（记录旧值与新值） |
| 两侧都有、值相同 | 不输出 |

任一侧指针耗尽后，另一侧剩余条目依次输出（旧侧剩余为 `delete`，
新侧剩余为 `insert`）。变更日志按键严格升序，且每个键至多一条。

`Replay(base, changes)` 把日志按序应用到基础快照（二分定位），
可复现目标快照；日志与基础快照不匹配（键缺失、旧值不符、插入已存在键、
日志未排序）时整体报错，不产出部分结果。

### 边界与错误类别

五类互不相同、可用 `errors.Is` 区分的错误，任何拒绝都是**整体拒绝**：

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | `MaxChanges < 0`、未知变更类型、重放日志与基线不匹配 |
| `ErrEmptyKey` | 快照中存在空键 |
| `ErrDuplicateKey` | 相邻两键相等（第一处违规） |
| `ErrUnsorted` | 相邻两键逆序（第一处违规），或重放日志未按键升序 |
| `ErrTooManyChanges` | 变更条数超过配置上限（等于上限合法） |

`MaxChanges <= 0` 表示不限条数。被拒请求不留任何痕迹：当前快照、版本号、
累计统计全部保持不变。

### 并发与版本一致性（`Manager`）

- 当前快照以**不可变切片**整体发布，`Snapshot()` 返回深拷贝；
- 差分计算在读锁下进行，多个差分与查询、统计、自检可真正并发，
  仅最终提交短暂持有写锁；若提交前基线版本已被他人推进，则在写锁下
  对最新版本重算后再提交（串行化等价）；
- 读者读到的整份快照必然等于历史上某个已提交版本，绝不会新旧混合；
- `Stats()` 返回自创建以来的成功提交累计：`Commits`、`Inserts`、
  `Deletes`、`Updates`、`ChangesTotal`（满足
  `Inserts + Deletes + Updates == ChangesTotal`），拒绝不计数；
- `SelfCheck()` 校验当前快照严格升序及统计不变量；`Version()` 初始为 0，
  每次成功提交加一。

### 过程日志

通过 `Manager.WithLogger` 或 `MergeDiff(..., logger)` 注入 `Logger`，
每一步都会打印两侧指针位置、输入键、判定结果（`insert`/`delete`/
`update`/`none`）与判定依据（如 `identical key and value`），
拒绝时打印错误类别及“状态不变”说明。可用 `SlogLogger`（基于
`log/slog`）或 `NopLogger()`。

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

# 反复压测并发差分与读者一致性
go test -race -count=10 ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology
```

`ontology` 包测试覆盖：双指针主体、旧/新侧尾部剩余、值相同不输出、
与朴素 map+排序参照实现一致、重放等价、非法日志拒绝、对调输入报未排序、
空键/重复键/超限拒绝后快照与统计不变、调用方切片篡改隔离，以及并发下
读者只能读到整份版本（`-race`）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
