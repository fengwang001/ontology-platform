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

## 分层订阅偏好冲突解析器（`pref` 包）

一个 `Store` 对应一位用户及其所属组织，管理三层偏好规则并裁决某类
通知在某渠道上是否允许。

### 规则模型

- 层级 `layer`：1 平台、2 组织、3 用户。
- 类目 `cat`：`/` 分隔的 1–4 段，每段 1–16 个小写字母、数字或下划线；
  空串为根，匹配一切类目。匹配按段取前缀（`billing` 不匹配 `billingx/a`）。
- 渠道 `ch`：`*`、`email`、`sms`、`push`；效果 `eff`：`allow` / `deny`。
- `locked` 仅平台与组织层可为真；`exp` 为 0 表示永不过期，否则须大于 `ts`。
- 规则键为 `(layer, cat, ch)`，同键 `Set` 覆盖旧规则并分配递增序号 `seq`。
- 时钟：`Set`/`Remove`/`UnsubscribeAll` 的 `ts` 不得小于已接受操作的最大
  `ts`（初值 0）；`Resolve` 的 `now` 同样不得小于它，但不推进它。

### 生效筛选（时刻 `t`）

规则生效当且仅当：

1. `exp == 0` 或 `t < exp`（`exp == t` 时已过期）；
2. 不属于「用户层且 `ts <= tomb`」，其中 `tomb` 是最近一次
   `UnsubscribeAll` 的时刻（初值 -1，恰等即失效）。平台与组织层不受
   `tomb` 影响。

候选规则：生效规则中 `cat` 按段是查询类目的前缀（含相等与根），且
`ch` 为 `*` 或等于查询渠道。

### 裁决次序

- 若候选中存在锁定规则，只在锁定规则中依次比较：**层级号较小者**（平台
  锁优先于组织锁）→ **类目段数较大者** → **渠道精确匹配者**。
- 否则在全部候选中依次比较：**类目段数较大者** → **渠道精确匹配者** →
  **层级号较大者**（更具体的组织规则胜过较宽的用户规则）。
- 完全并列时以 `seq` 较大者胜，保证结果与 map 迭代顺序无关、可精确复现。
- 无候选时裁决为 `deny`，来源字段为空。

### UnsubscribeAll 语义

`UnsubscribeAll(ts)` 不删除任何规则，只把 `tomb` 推进到 `ts`：所有
`ts <= tomb` 的用户层规则失效；之后 `ts` 更大的用户层 `Set` 重新生效。

### 锁定覆盖（Set/Remove 的前置校验）

用户层操作若被某条在该时刻生效的锁定规则覆盖（锁定规则的 `cat` 是其
前缀或相等、`ch` 为 `*` 或相等）则被拒绝。覆盖判定是非对称的：具体渠道
的锁定规则不覆盖用户 `*` 规则的 `Set`，但在裁决中仍于该渠道胜出。

### 拒绝原因（按序只报第一个）

- `Set`：`ErrInvalidArgument` → `ErrClockRegression` →
  `ErrPermissionDenied` → `ErrLockedOverride` → `ErrCapacityExceeded`
  （规则总数上限 1000，覆盖已有键不算新增）。
- `Remove`：`ErrInvalidArgument` → `ErrClockRegression` →
  `ErrLockedOverride`（仅用户层）→ `ErrRuleNotFound`。
- `UnsubscribeAll`：`ErrInvalidArgument` → `ErrClockRegression`。
- `Resolve`：`ErrInvalidArgument` → `ErrClockRegression`。

被拒绝的操作不改变任何规则、`seq`、`tomb` 与最大 `ts`。所有方法可并发
调用（内部读写锁），结果等价于某个串行顺序；`Resolve` 只读。

### 本地验证

```bash
# 全部单元测试 + 2000 组随机规则集与朴素实现对拍
go test ./pref/

# 查看对拍日志（输入、输出与判定依据）
go test -run TestDifferential -v ./pref/

# 竞态检测
go test -race ./pref/
```
