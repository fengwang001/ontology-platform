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

## 累计计数器增量统计器（`counter` 包）

`counter.Tracker` 按序列接收累计读数 `(t, v)`（`t` 为 int64，`v` 为非负 int64），
并对左开右闭窗口 `(a, b]` 精确求增量与重置次数。

### 重置判定

- 相邻读数 `(p, q)`：`q.v >= p.v` 时增量为 `q.v - p.v`。
- `q.v < p.v` 判为一次重置，增量为 `q.v`（视为重置后从 0 起算，
  重置前未上报的部分不补）。
- 序列首个读数没有前驱，不产生增量、不计重置。

### 窗口归属口径

- 增量与重置都归属于相邻对的**右端点 `q`**：`q.t` 落在 `(a, b]` 内才计入，
  `p` 可以在窗口之外。因此读数恰在 `a` 不计入、恰在 `b` 计入。
- 对任意 `a < m < b`，`(a,b]` 的结果恒等于 `(a,m]` 与 `(m,b]` 之和，
  窗口可任意拆分相加；序列存在但窗口内无相邻对时返回 0（不是错误）。

### 写入与拒绝规则

- 时间戳等于最新读数且数值相同视为幂等重复：成功且不改变状态。
- 以下情况整体拒绝且不改状态，错误可区分（`errors.Is`）：
  `ErrNegativeValue`（数值为负，先于时间次序检查）、
  `ErrTimestampOrder`（时间戳回退）、`ErrConflictingDuplicate`（同时间戳数值不同）、
  `ErrInvalidWindow`（`a >= b`，先于序列不存在检查）、
  `ErrUnknownSeries`（查询未出现过的序列）、`ErrOverflow`（窗口增量和溢出 int64）。
- 所有方法并发安全（`sync.RWMutex`），效果等价于某个串行顺序；
  相同读数序列重放得到完全相同的结果。

### 本地验证

```bash
# 单元测试（含边界、重置、幂等、溢出、随机拆分恒等式对照朴素实现）
go test -v ./counter

# 竞态检测
go test -race ./counter
```
