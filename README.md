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

## 曝光点击对账器（`attribution` 包）

`attribution.Reconciler` 把一批曝光（`Impression(user, ad, t, v)`）与点击
（`Click(user, ad, t)`）按事件时间处理成九类结论，支持并发调用、批次原子
生效与确定性重放。

### 构造参数（单位毫秒，取值 `[0, 10^9]`）

- `V`：可见时长门槛，曝光 `v < V` 记为不可见（`v == V` 可见）。
- `C`：同广告冷却，距当前锚点 `< C` 的可见曝光记为冷却（恰等于 `C` 可计数）。
- `A`：归因窗口，点击 `g = t - 锚点时刻 > A` 为已过期（恰等于 `A` 有效）。
- `Tmin`：最短点击间隔，`g < Tmin` 为过快（恰等于 `Tmin` 有效）。
- `Lk`：归因后锁定期，归因后曝光在 `t < e = t_click + Lk` 内冷却（`t == e` 不锁定）。
- `Tu`：用户级归因间隔，`t - u < Tu` 为串扰，`u` 为该 user 跨所有 ad 的最近
  归因时刻（`t < u` 也成立；恰等于 `Tu` 可归因；`Tu = 0` 时该检查退化关闭）。

### 排序与状态

- `Submit` 先将批内事件按「`t` 升序 → 同刻曝光先于点击 → 输入次序」稳定排序，
  返回结果仍与输入位置一一对应。
- 每个 `(user, ad)` 对只保留常数状态：事件水位（见过的最大 `t`，初值无）、
  锚点（最近一次**已计数**曝光的时刻及是否已归因）、锁定截止 `e`（初值无）；
  每个 user 只保留跨 ad 的 `u`。

### 判定次序

- 曝光，按序只报第一个：`v < V` 不可见 → 距锚点 `< C` 冷却 → `t < e` 锁定冷却
  → 已计数（锚点更新为本曝光、未归因）。不可见与冷却都不移动锚点。
- 点击，按序只报第一个：无锚点无曝光 → `g > A` 已过期 → `g < Tmin` 过快
  → 锚点已归因重复 → `t - u < Tu` 串扰 → 归因。
- 只有「归因」会改状态：锚点标记已归因、`e = t + Lk`、`u = max(u, t)`；
  过快、重复、已过期、串扰均不改锚点与 `e`，点击总是归给最近一次已计数曝光。

### 批次拒绝（整批原子回滚）

拒绝原因按序只报第一个：

1. 参数非法（`ErrInvalid`）：构造参数越界、`user`/`ad` 为空、`t` 越界
   `[0, 10^15]`、曝光 `v` 越界 `[0, 10^9]`。
2. 乱序（`ErrOutOfOrder`）：排序后任一事件的 `t` 小于所属对的事件水位
   （恰等于水位允许）。

被拒绝的批次不改变任何水位、锚点、`e`、`u` 与计数。

### 并发与不变量

- `Submit`/`Stats` 内部互斥，一批事件在工作副本上推演并在全部合法后一次性
  提交，观察者不会看到处理了一半的批。
- 恒等式：曝光总数 = 已计数 + 冷却 + 不可见；点击总数 = 归因 + 重复 + 已过期
  + 过快 + 无曝光 + 串扰；归因数 ≤ 已计数曝光数，每个已计数曝光至多归因一次。
- 相同批序列重放得到完全相同的结论序列与计数。

### 本地验证

```bash
# 全部测试（含 -v 可查看每组输入、输出与判定依据日志）
go test -race -v ./attribution

# 仅跑题面示例与 2000 组随机批序列对照
go test -run 'TestSpecExample|TestRandomAgainstNaive' -v ./attribution

# 覆盖率
go test -coverprofile=coverage.out ./attribution
go tool cover -html=coverage.out
```

随机对照测试（`TestRandomAgainstNaive`）用独立的朴素逐事件模拟器重放
2000 组随机参数、多用户/多广告、多批次序列，逐事件比较结论并校验计数恒等式；
`TestReplayDeterminism` 校验同序列重放的确定性。
