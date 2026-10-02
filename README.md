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

## gdsfcache：代价感知的构建制品缓存

`gdsfcache` 包在字节容量限制下按 Greedy-Dual-Size-Frequency（GDSF）
优先级驱逐缓存条目。所有优先级与通胀值均用 `math/big.Rat` 精确有理数
表示，全程无浮点运算，因此通胀推进、命中重算与并列打破都可精确复现。

### 优先级 H 与通胀值 L

- 缓存维护全局通胀值 `L`（有理数，初始为 0）与逻辑序号 `tick`
  （从 0 起，每次成功的 `Put` 插入与每次命中的 `Get` 各加 1）。
- 每个条目记录 `freq`（插入时为 1，每次命中加 1）、优先级
  `H = L + freq×cost/size`（用插入或命中那一刻的 `L` 计算并保存），
  以及 `last`（最近一次插入或命中的 `tick`）。
- 命中时 `freq` 加 1，并用**当前** `L` 重算 `H`、刷新 `last`。

### 驱逐与并列规则

- 插入前若 `已用字节 + size > Cap`，反复驱逐 `H` 最小的条目；
  `H` 相等时取 `last` 较小者。每驱逐一个，`L` 被赋值为被驱逐者的 `H`
  （逐个单调推进）。`已用字节 + size == Cap` 时不驱逐。
- 不做准入过滤：新条目的 `H` 低于现有条目也照常插入。
- 驱逐在最小堆上进行，单次 `Put`/`Get` 的 `H` 比较次数为对数级
  （测试以非导出计数器验证不超过 `8×(⌊log2 n⌋+2)`）。

### 覆盖写语义

`Put` 的键已存在时，先移除旧条目（释放其字节，不改变 `L`），再按新
参数走普通插入流程；`freq` 重置为 1，`last` 取新 `tick`。

### 错误优先级

构造时 `Cap <= 0` 返回 `ErrNonPositiveCapacity`。`Put` 按以下顺序只报
第一个错误：键为空（`ErrEmptyKey`）、`size <= 0`
（`ErrNonPositiveSize`）、`cost < 1`（`ErrInvalidCost`）、
`size > Cap`（`ErrSizeExceedsCapacity`）。`Get`/`Peek` 键为空返回
`ErrEmptyKey`，`Peek` 键不存在返回 `ErrNotFound`。被拒绝的操作不改变
任何条目、`L` 与 `tick`。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与线性扫描朴素实现对拍 + 并发竞态检测
go test -race ./gdsfcache/

# 查看对拍日志（输入、输出与判定依据）与比较次数统计
go test -race -v -run 'TestDifferentialAgainstNaive|TestComparisonCountIsLogarithmic' ./gdsfcache/
```
