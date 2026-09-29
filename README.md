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

## 反连接增量物化视图（`antijoin` 包）

对左右两侧行变更流按“不存在匹配（NOT EXISTS / anti-join）”语义增量维护结果集，
并输出结果成员的进入/离开变更日志。实现位于 `antijoin/antijoin.go` 与 `antijoin/log.go`。

### 空值（NULL）语义

- 键为 `nil` 表示 NULL；NULL 与任何值都不相等，**包括与另一个 NULL 也不相等**。
- 因此键为 NULL 的左行**永远属于结果**，没有任何右行能够匹配它。
- 键为 NULL 的右行**不匹配任何左行**：插入/删除它都不改变右侧计数与结果成员。
- 非空键必须是 Go 可比较类型（可作为 map 键），相等判定采用 Go 的 `==`。

### 成员资格判定

左行 `(id, k)` 在结果中，当且仅当：

1. `k` 为 NULL；或
2. 不存在任何键与 `k` 相等的存活右行（即该键的右侧计数为 0）。

视图为每个**非空键**维护右侧行数计数；NULL 键不计数。

### 变更日志规则

每条输入（`Change{Side, Op, ID, Key}`）原子提交，输出按以下规则产生：

| 输入 | 计数变化 | 输出 |
| --- | --- | --- |
| 插入左行，键为 NULL | — | 该左行 `enter` |
| 插入左行，非空键且计数为 0 | — | 该左行 `enter` |
| 插入左行，非空键且计数 > 0 | — | 无输出 |
| 删除属于结果的左行 | — | 该左行 `leave` |
| 删除不属于结果的左行 | — | 无输出 |
| 插入非空键右行，计数 0→1 | 越过零 | 所有同键左行 `leave` |
| 插入非空键右行，计数 1→2 等 | 保持为正 | 无输出 |
| 删除非空键右行，计数 1→0 | 越过零 | 所有同键左行 `enter` |
| 删除非空键右行，计数 2→1 等 | 保持为正 | 无输出 |
| 插入/删除 NULL 键右行 | 不计数 | 无输出 |

- 同一次输入产生多条输出时，**按左行标识字典序排序**，保证可复现。
- 只有右侧计数在 `0→1` 或 `1→0` 之间穿越零边界才产生结果变更，其余变化静默。
- `View.Changelog()` 保存已接受步骤（含逐条输出的判定依据 `Output.Reason`）。

### 边界与错误类别

输入在**任何状态修改之前**整体校验；一旦拒绝，两侧行、右侧计数、结果成员与
已提交变更日志均不发生任何变化（失败不留痕）。哨兵错误互可区分，用 `errors.Is` 判别：

| 错误 | 触发条件 |
| --- | --- |
| `ErrEmptyID` | 标识为空字符串 |
| `ErrDuplicateInsert` | 在同一侧插入已存在的标识（左右两侧各自独立） |
| `ErrDeleteNotExist` | 删除该侧不存在的标识 |
| `ErrRowLimit` | 插入会使两侧存活行数总和超过上限（`WithMaxRows`，默认 `DefaultMaxRows`） |
| `ErrInvalidInput` | 侧或操作类型非法 |

其他边界：右侧计数恒不为负；计数归零后对应 map 条目被删除；被拒绝的尝试只写入
注入的外部 `Logger`，不进入 `Changelog()`。

### 并发与正确性

- `View` 内部使用 `sync.RWMutex`：`Apply` 提交独占，`Result`/`Check`/`Changelog`/
  行快照等读操作共享读锁，多个执行体可并发调用，且自检可与提交并发。
- `Check()` 重扫两侧行重算计数与成员集合，与增量状态逐项比对。
- `Recompute(left, right)` 提供独立的批量重算基准。
- 已验证属性：**变更日志任意前缀**在全新视图上重放后，结果都等于对该前缀后
  两侧行集合的批量重算（随机序列 + 全前缀校验）。

### 日志

- `NewPrintLogger(w)`：逐行打印每步输入、接受/拒绝的判定依据、每条输出及其理由。
- `CollectLogger`：收集全部步骤（含被拒绝尝试），供测试断言。
- `NopLogger`（默认）：静默；`WithLogger` 注入。

### 本地验证

```bash
# 全量测试（带竞态检测）
go test -race -v ./antijoin/

# 全仓库
go test ./...

# 代码检查
gofmt -l .
go vet ./...
```

测试覆盖：计数穿越零（0→1→2→1→0）、NULL 语义、右侧先到、同输入多输出排序、
四类非法输入及拒绝后状态不变、随机序列每个前缀与批量重算一致、多执行体并发提交/查询/自检。
