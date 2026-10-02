# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## shares：实例流量份额计算器

`shares` 包实现带慢启动爬坡与单实例份额上限的实例流量份额计算器，
构造参数为慢启动窗口 `Wslow`（1 到 1e9）、总份数 `T`（1 到 1e6）与
单实例份数上限 `Y`（1 到 T），任一越界则整体拒绝。

### 实例生命周期

- `AddHost(id, weight, now)` 登记实例，登记后为健康且上线时刻 `join=now`；
  同一 id 移除后再登记视为新实例。
- `SetWeight(id, weight, now)` 只改权重，保留 `join`。
- `SetHealth(id, healthy, now)` 设置健康位；仅当从不健康变为健康时把
  `join` 重置为 `now`（即恢复后重新爬坡），设为相同值不改任何状态。
- `RemoveHost(id, now)` 移除实例。

### 有效权重

实例在 `now` 的有效权重：不健康为 0；健康时令 `e = now - join`，
`e >= Wslow` 取 `weight`，否则取 `max(1, floor(weight * e / Wslow))`
（爬坡下限为 1，但份数可以为 0）。

### 份额分配

`Shares(now)` 返回按 id 字节序升序的全部实例份数，分轮进行：

1. 活动集合 `A` 初为全部健康实例，剩余份数 `Trem` 初为 `T`。
2. 每轮对 `A` 用最大余数法分 `Trem` 份：设有效权重和为 `E`，
   底数 `floor(eff * Trem / E)`，其余份数按 `(eff * Trem) mod E`
   从大到小、并列取 id 小者分配，每个实例至多加一份。
3. 若本轮有份数大于 `Y` 的实例，则把本轮所有这类实例一次性固定为
   `Y` 份、从 `A` 中移除，`Trem` 减去固定份数后进入下一轮
   （下一轮的底数与余数基于新的 `Trem` 与剩余实例的有效权重）；
   否则本轮结果即最终份数，结束。
4. `A` 为空而 `Trem` 仍大于 0 时报容量不足；不健康实例份数为 0。

每轮至少固定一个实例，轮数不超过健康实例数；成功时各项非负、
总和恒为 `T`、每项不超过 `Y`。

### 校验顺序与并发

各变更与查询操作按以下顺序只报第一个错误：参数非法（id 为空、
weight 不在 1 到 1e6）→ 时间非法（now 不在 0 到 1e15）→ 时钟回退
（now 小于已接受操作见过的最大 now，初值 0）→ 状态非法（id 已存在 /
不存在 / 无健康实例 / 容量不足）。所有被接受的操作（含因无健康实例或
容量不足而报错的 `Shares`）推进最大 now；被拒绝的操作不改变任何状态。
全部方法可并发调用，效果等价于某个串行顺序；相同操作序列重放得到
完全相同的份额序列。

### 本地验证

```bash
# 单元测试（规格示例与边界用例）
go test ./shares/

# 随机对拍：2000 组随机操作序列与朴素参照实现逐步对照，
# -v 日志打印每步输入、输出与判定依据（有效权重、每轮底数/余数/固定过程）
go test -run TestRandomSequencesAgainstNaive -v ./shares/

# 竞态检测
go test -race ./shares/
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
