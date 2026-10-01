# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 主机选择器（`ontology` 包）

`ontology/selector.go` 实现按标签子集路由的主机选择器 `HostSelector`，支持恐慌阈值、
降键回退与全局回退策略。构造入口为
`NewHostSelector(selectors []SelectorSpec, policy FallbackPolicy, defaultLabels map[string]string, panicThreshold int)`。

### 键集合相等判定

`Route(labels)` 只在请求标签的键集合 `K` **与某个选择器的键集合恰好集合相等** 时命中该选择器：

- 请求键是选择器键集合的超集或子集都不算命中（即使标签内容能对上）。
- 两个选择器不允许配置成相同的键集合（构造期拒绝）。
- `K` 为空，或与所有选择器键集合都不相等时，直接进入全局回退。

### 子集评估规则（恐慌阈值与健康子集）

对一次评估 `(K', l')`（`K'` 为键集合、`l'` 为标签）：

- `A`：对 `K'` 中每个键，主机标签都存在且与 `l'` **精确相等**（含空串值；缺键不匹配）的全部主机，
  包含不健康主机。
- `H`：`A` 中的健康主机。两者均按主机 id 升序排列。
- 若 `A` 非空且 `|H|*100 < Pt*|A|`（严格小于），判定为**恐慌**：
  选择 `A[c mod |A|]`，其中 `c` 是该子集的轮转计数；因此恐慌时可能选中不健康主机，
  但它一定满足该子集的全部标签条件。
- 否则若 `H` 非空：选择 `H[c mod |H|]`。
- 相等边界（`|H|*100 == Pt*|A|`）**不恐慌**；`Pt=0` 时永不恐慌。
- `A` 为空，或非恐慌但 `H` 为空：本次评估落空。

### 降键回退与全局回退次序

命中选择器后严格按以下次序，每一级只在评估落空时进入：

1. 直接评估 `(K, labels)`。
2. 该选择器配置了非空降级键集合 `FK`（必须是 `K` 的非空真子集）时，
   评估 `(FK, labels 限制到 FK 的键)`，**只降一级，不再递归降级**
   （即使 `FK` 恰好等于另一个选择器的键集合，也不会沿那个选择器的 `FK` 继续降）。
3. 仍落空，进入全局回退；全局回退**不适用恐慌阈值**。

全局回退策略（`FallbackPolicy`）：

- `FallbackNone`：`K` 与任何选择器键集合不相等（含 `K` 为空）报 `ErrNoMatchingSelector`；
  `K` 相等但两级评估全部落空报 `ErrNoHealthyHost`。
- `FallbackAny`：候选为全部健康主机，按 id 升序，用全局计数 `g` 轮转；候选为空报 `ErrNoHealthyHost`。
- `FallbackDefaultSubset`：候选为含全部默认标签（每键精确相等）的健康主机，按 id 升序，
  用默认计数 `d` 轮转；候选为空报 `ErrNoHealthyHost`。该策略要求默认标签非空且不含空键。

### 计数推进规则

- 子集身份为“按键排序的 `(键, 值)` 列表”。直接命中与降键到达同一子集时**共用同一个计数 `c`**。
- 只有成功选出主机才推进计数：推进的是所用那一级的计数（子集计数 / `g` / `d` 三者之一）。
- 评估落空、路由报错、被拒绝的操作都不推进任何计数，也不改变主机状态。
- 候选集合随 `AddHost`/`RemoveHost`/`SetHealth` 变化后，取模始终按**当前**候选数计算，
  因此重放相同操作序列可得到完全相同的选择序列。

### 非法配置与操作拒绝

构造期整体拒绝（返回 `ErrInvalidConfig`）：选择器列表为空；选择器键集合为空、含空键或含重复键；
两个选择器键集合相同；`FK` 不是对应键集合的非空真子集；策略非法；`Pt` 不在 0–100；
默认子集策略的默认标签为空或含空键。

`AddHost` 按顺序只报第一个原因：id 为空或标签含空键（id 空报 `ErrEmptyID`，否则 `ErrEmptyLabelKey`）；
id 已存在报 `ErrHostExists`。`SetHealth`/`RemoveHost` 对不存在的 id 报 `ErrHostNotFound`，
设成相同健康值也视为成功。`Route` 的标签含空键报 `ErrInvalidLabels`。

所有方法在内部互斥锁下串行执行，可被并发调用，结果等价于某个串行顺序。

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

# 仅运行主机选择器测试（含 2000 组随机对照）
go test -race -v ./ontology/

# 随机对照会生成判定依据日志，测试结束时输出路径，例如：
#   differential log: /tmp/ontology-diff-XXXXXX.log
# 日志逐条记录每次操作的输入、输出与判定依据
# （direct / fallback-key / global-any / global-default、A、H、所用计数、是否 PANIC、选中主机）

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
