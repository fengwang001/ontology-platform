# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 标签子集主机选择器（`ontology.HostSelector`）

`selector.go` 提供一个并发安全的主机选择器：按“请求标签的键集合是否与某个选择器的键集合**精确相等**”路由，支持恐慌阈值、降键回退与全局回退，轮转计数可精确复现。

### 构造

```go
NewHostSelector(selectors []SelectorSpec, policy FallbackPolicy, defaultLabels map[string]string, panicThreshold int)
```

- `SelectorSpec{KeySet, FallbackKeys}`：`KeySet` 为非空键集合；`FallbackKeys`（记为 FK）可省略，若给出必须是 `KeySet` 的非空真子集。
- `policy`：`FallbackNone`（不回退）、`FallbackAnyEndpoint`（任意健康端点）、`FallbackDefaultSubset`（默认标签子集）三者之一。
- `defaultLabels`：仅默认子集策略使用，该策略下必须非空且不含空键；其余策略忽略。
- `panicThreshold`（Pt）：闭区间 `[0,100]` 的百分数；`Pt=0` 永不恐慌。
- 任一配置非法时整体拒绝（返回哨兵错误），不产生半成品实例。

### 键集合相等判定

- `Route(labels)` 的键集合 `K` 必须与某个选择器的键集合**集合相等**；`K` 是其超集或子集都不算匹配。
- `K` 为空或没有相等选择器时，不做任何子集评估，直接进入全局回退。
- 选择器键集合互不允许相同。

### 子集评估（恐慌阈值与健康子集）

对评估 `(K′, ℓ′)`，取：

- `A`：每个 `(键,值)` 都与 `ℓ′` 精确相等的全部主机（含不健康，值必须完全相同，空串是合法值）；
- `H`：`A` 中的健康主机；两者均按主机 id 升序。

然后：

- 若 `A` 非空且 `|H|*100 < Pt*|A|`（**严格小于**；相等不恐慌），进入恐慌：选 `A[c mod |A|]`；
- 否则若 `H` 非空，选 `H[c mod |H|]`；
- 否则本次评估落空。

子集以“按键排序的 `(键,值)` 列表”标识，每个子集有独立计数 `c`；无论经直接命中还是降键到达，**同一 (键,值) 子集共用同一计数**。

### 降键与全局回退次序

1. 键集合精确命中选择器：先评估 `(K, labels)`；
2. 落空且该选择器有 FK：只评估一次 `(FK, labels 限制到 FK 的键)`，**不再递归降级**；
3. 仍落空（或 `K` 为空 / 无相等选择器）进入全局回退，全局回退**不适用恐慌阈值**：
   - `FallbackNone`：`K` 与选择器不相等报 `ErrNoSelector`；`K` 相等但直接/降键全部落空报 `ErrNoHealthyHost`；
   - `FallbackAnyEndpoint`：候选为全部健康主机（id 升序），用全局计数 `g`；
   - `FallbackDefaultSubset`：候选为含全部默认标签的健康主机（id 升序），用默认计数 `d`；
   - 全局候选为空时报 `ErrNoHealthyHost`，否则选 `候选[计数 mod 候选数]`。

### 计数推进规则

- 只有**成功选出主机**才推进对应的那一个计数（子集计数 / `g` / `d` 三选一）；
- 评估落空、降键落空、路由报错、被拒绝的操作都不推进任何计数；
- 恐慌选中的主机可以不健康，但一定满足所用子集的全部标签条件；非恐慌选中的主机一定健康；
- 候选集合随 `AddHost` / `SetHealth` / `RemoveHost` 变化，取模始终按**当前**候选大小；
- 相同操作序列重放得到完全相同的选择序列（`math/rand` 固定种子的随机对照测试亦显式重放校验）。

### 变更与错误

- `AddHost(id, labels, healthy)`：拒绝原因按顺序只报第一个——id 为空或标签含空键（分别为 `ErrEmptyID` / `ErrInvalidLabel`）、id 已存在（`ErrDuplicateHost`）。
- `SetHealth` / `RemoveHost`：id 不存在报 `ErrHostNotFound`；设成相同健康值也接受。
- `Route`：请求标签含空键报 `ErrInvalidLabel`。
- 所有登记、变更与路由均在互斥锁下完成，结果等价于某个串行顺序。
- `SubsetCount` / `GlobalCount` / `DefaultCount` / `HostCount` 为观测与测试用的只读访问器。

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

主机选择器的本地验证（若 Go 缓存目录只读，可指定 `GOCACHE=/tmp/gocache`）：

```bash
# 定向用例：边界恐慌、降键、三种回退、拒绝不改状态、并发等
go test -v -run 'TestWorkedExample|TestPanic|TestFallback|TestGlobal|TestConcurrent' ./...

# 2000 组随机主机集合与请求，与按规则逐步写成的朴素模型对照并重放；
# -v 日志打印输入、输出与判定依据（恐慌取模、降键、全局回退候选等）
GOCACHE=/tmp/gocache go test -v -run TestRandomNaiveComparison ./...

# 竞态检测
go test -race -count=1 ./...
```
