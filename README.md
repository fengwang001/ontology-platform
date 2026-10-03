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

## 分层订阅偏好冲突解析器（`prefs` 包）

`prefs.Store` 对应一位用户及其所属组织，管理平台（layer=1）、组织
（layer=2）、用户（layer=3）三层偏好规则，并裁决某类通知在某渠道上是否
允许。规则键为 `(layer, cat, ch)`，同键 `Set` 覆盖旧规则并分配递增的
`seq`；规则总数上限 1000。

### 生效筛选

一条规则在时刻 `now` 生效，当且仅当：

- `exp == 0`（永不过期）或 `now < exp`（`exp == now` 时恰已失效）；
- 不是「用户层且 `ts <= tomb`」的规则（`tomb` 为最近一次
  `UnsubscribeAll` 的时刻，初值 -1；`ts == tomb` 恰失效；平台与组织层
  不受 `tomb` 影响）。

候选规则还需满足：`cat` 按段是查询类目的前缀（空串为根，匹配一切；
按段而非按字符串前缀，`billing` 不匹配 `billingx/a`），且 `ch` 为 `*`
或等于查询渠道。

### 裁决比较次序

- 若候选中存在锁定规则，只在锁定规则中依次比较：**层级号小者优先 →
  类目段数多者优先 → 渠道精确匹配优先**（平台锁优先于组织锁）。
- 否则在全部候选中依次比较：**类目段数多者优先 → 渠道精确匹配优先 →
  层级号大者优先**（更具体的组织规则胜过较宽的用户规则）。
- 无候选时裁决为 `deny` 且来源字段为空。

`Resolve` 返回 `Decision{Allowed, Layer, Cat, Ch, Seq}`，来源规则与
`seq` 可精确复现；相同操作序列重放得到完全相同的结果。

### UnsubscribeAll 语义

`UnsubscribeAll(ts)` 不删除任何规则，只把 `tomb` 推进到 `ts`，使用户层
`ts <= tomb` 的规则失效；之后以更大 `ts` 登记的用户层 `Set` 重新生效。

### 操作约束与拒绝原因

`Set`/`Remove`/`UnsubscribeAll` 的 `ts` 与 `Resolve` 的 `now` 均不得小于
已接受操作的最大 `ts`（`Resolve` 只读、不推进时钟）。拒绝原因按固定顺序
只报第一个（如 `Set`：参数非法 → 时钟回退 → 权限不足 → 已被锁定覆盖 →
容量不足），被拒绝的操作不改变任何规则、`seq`、`tomb` 与最大 `ts`。
所有方法可并发调用，效果等价于某个串行顺序。

### 本地验证

```bash
# 全部单元测试（含 2000 组随机规则集与朴素实现的对拍）
go test ./prefs/

# 查看对拍日志（输入、输出与判定依据）
go test ./prefs/ -run TestDifferential -v

# 竞态检测
go test -race ./prefs/
```
