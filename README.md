# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 曝光点击对账器（reconcile 包）

`reconcile` 把一批批曝光 `Impression(user, ad, t, v)` 与点击 `Click(user, ad, t)`
事件按事件时间处理成已计数曝光与归因点击，每个事件的归属结论与各类计数可精确复现。

### 构造参数

`New(V, C, A, Tmin, Lk, Tu)`，均为 `[0, 10^9]` 的 int64（毫秒），越界返回 `ErrInvalidParam`：

- `V` 可见时长门槛：`v < V` 的曝光记为不可见
- `C` 同广告冷却：`t - 锚点 < C` 的曝光记为冷却
- `A` 归因窗口：`g > A` 的点击记为已过期（恰等于 `A` 有效）
- `Tmin` 最短点击间隔：`g < Tmin` 的点击记为过快
- `Lk` 归因后锁定期：归因后 `e = t + Lk`，`t < e` 的曝光记为冷却
- `Tu` 用户级归因间隔：`t - u < Tu` 的点击记为串扰

### 状态

- 每对 `(user, ad)` 独立维护常数个状态：事件水位（见过的最大 `t`）、
  锚点（最近一次已计数曝光的时刻及其是否已被归因）、锁定截止时刻 `e`。
- 每个 `user` 只维护其全部已归因点击时刻的最大值 `u`（跨该 `user` 的所有 `ad`）。
- 状态量不随事件总数增长；相同的批序列重放得到完全相同的结论序列与计数。

### 排序与判定次序

`Submit(批)` 先把批内事件按 **(t 升序，同刻曝光先于点击，再按输入次序)** 稳定排序，
然后依次处理，整批原子生效；返回的结论序列与排序后的事件一一对应。

- 曝光：`v < V` → 不可见；否则若已有锚点且 `t - 锚点 < C`，或 `t < e` → 冷却；
  否则 → 已计数，锚点改为本曝光且未归因。不可见与冷却都不改锚点。
- 点击只看该对锚点，依次判定、只报第一个原因：
  无锚点 → 无曝光；`g = t - 锚点 > A` → 已过期；`g < Tmin` → 过快（不改状态）；
  锚点已被归因 → 重复；`u` 存在且 `t - u < Tu`（`t < u` 时也成立）→ 串扰（不改状态）；
  否则 → 归因，锚点标为已归因，并令 `e = t + Lk`、`u = max(u, t)`。
- 点击总是归给最近一次**已计数**曝光（锚点），而不是最近一次曝光；
  归因窗口 `g` 按锚点起算。

### 批级拒绝

按此顺序只报第一个（可用 `errors.Is` 区分）：

1. `ErrInvalidParam`：构造参数越界、`user` 或 `ad` 为空、`t ∉ [0, 10^15]`、`v ∉ [0, 10^9]`；
2. `ErrOutOfOrder`：排序后任一事件的 `t` 小于其所属对的事件水位（`t` 等于水位允许）。

被拒绝的批不改变任何水位、锚点、锁定截止、`u` 与计数；批内事件不会单独被拒绝。

### 并发与计数

`Submit` 与 `Stats` 可并发调用，结果等价于某个串行顺序，一批事件原子生效。
`Stats()` 返回九类结论计数，恒有：曝光总数 = 已计数+冷却+不可见，
点击总数 = 归因+重复+已过期+过快+无曝光+串扰，归因数 ≤ 已计数曝光数，
每个已计数曝光至多被归因一次。

### 本地验证

```bash
# 单元测试：边界（恰等于/差 1）、判定次序、规范示例、拒绝与重放
go test ./reconcile/

# 随机对拍：2000 组随机批序列 vs 逐事件朴素模拟，日志含输入/输出/判定依据
go test ./reconcile/ -run TestRandomAgainstNaive -v

# 并发与竞态
go test -race ./reconcile/
```

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
