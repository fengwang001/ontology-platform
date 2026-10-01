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

## 优先级分层故障转移分配器

`ontology.FailoverAllocator`（见 `ontology/failover.go`）把 100 份流量
按优先级层（层号越小优先级越高）的健康主机比例分层分配，并带有过供给
放大、恢复迟滞与单层份额上限。所有登记、变更与查询方法均持有互斥锁，
并发调用结果等价于某个串行顺序。

### 构造与配置

```go
a, err := ontology.NewFailoverAllocator(L, F, delta, caps)
```

- `L`：层数，层号 `0..L-1`，合法范围 1–16。
- `F`：过供给因子（百分比整数，140 表示 1.4 倍），合法范围 100–1000。
- `delta`：恢复迟滞阈值 Δ，合法范围 1–100。
- `caps`：每层份额上限，长度必须为 L，每项 1–100，总和不小于 100。

任一条件不满足整体返回 `ErrInvalidConfig`。

主机管理：

- `AddHost(level, id, healthy)`：校验顺序为层号越界
  （`ErrLevelOutOfRange`）→ id 为空（`ErrEmptyID`）→ id 已存在
  （`ErrDuplicateID`），只报第一个。
- `RemoveHost(id)` / `SetHealth(id, healthy)`：id 不存在返回
  `ErrHostNotFound`；`SetHealth` 设成相同值也成功。

### 层健康度公式

第 p 层共 `t` 台主机、其中 `h` 台健康时：

```
raw_p = 0                                        （t = 0）
raw_p = min(100, floor(h * F / t))               （t > 0）
```

### 迟滞记忆更新规则

每层维护记忆值 `cur_p`（初为空，视为 0）。每次**成功的** `Loads`
（`PickLevel` 内部也会执行一次）按层更新：

- `cur_p` 为空、`cur_p == 0`、`raw_p <= cur_p`（下降立即生效）、
  或 `raw_p >= cur_p + Δ` 时，`cur_p = raw_p`；
- 仅当 `cur_p > 0` 且 `cur_p < raw_p < cur_p + Δ` 时保持 `cur_p`
  不变（恢复迟滞）。注意 `raw_p` 恰好等于 `cur_p + Δ` 时更新，差 1
  时保持；从 0 恢复不迟滞。

令 `S = sum(cur_p)`。

### 三种份额分配分支

1. `S >= 100`（贪心）：剩余量初值 100，按层号升序
   `load_p = min(剩余, cur_p)`。前面层可能吃光全部份额，后层为 0。
2. `1 <= S <= 99`（缩放）：`load_p = floor(cur_p * 100 / S)`，不足
   100 的余数全部加给**层号最小且 cur_p > 0** 的层（最小层 cur 为 0
   时顺延到下一个 cur > 0 的层）。
3. `S == 0`（恐慌）：100 份全部给层号最小且**至少有一台主机**（不
   要求健康）的层；没有任何主机时 `Loads` 返回 `ErrNoHosts`。

### 限额再分配

初分之后：

1. 按层号升序把每层超过 `Cap_p` 的部分截掉，截下的量累加为 `X`，该
   层份额变为 `Cap_p`。
2. 按层号升序把 `X` 依次补给有接收资格的层，每层至多补
   `Cap_p - load_p`，补完即止。接收资格：普通情况下 `cur_p > 0`；
   恐慌情况下 `t_p > 0`（有主机即可）。
3. 若 `X` 仍补不完，`Loads` 返回 `ErrOutOfCapacity`，且本次不更新
   任何 `cur_p`（先报 `ErrNoHosts`，再报容量不足）。

成功时各层份额非负、不超过各自 Cap、总和恒为 100。

### PickLevel

`PickLevel(r)` 要求 `r in [0,99]`，越界返回 `ErrInvalidPoint`（最先
检查）；随后内部执行一次同样更新记忆的 `Loads`（再依次可能得到
`ErrNoHosts`、`ErrOutOfCapacity`）。返回满足
`r < 前 p+1 层份额之和` 的最小层号，即第 p 层服务左闭右开区间：

```
[ load_0 + ... + load_{p-1}, load_0 + ... + load_p )
```

份额为 0 的层永远不会被返回。

被拒绝的操作（含容量不足）不改变任何主机、健康位与 `cur`。

### 本地验证

```bash
# 全量测试（确定性边界用例 + 2000 组随机朴素对照 + 并发）
go test ./...

# 竞态检测；随机对照的输入/输出/判定依据（raw、更新前 cur、
# 分配分支、再分配后余量）通过 -v 打印
go test -race -v ./ontology

go test -run TestNaiveOracleRandom -v ./ontology
gofmt -l .
go vet ./...
```

随机测试（`TestNaiveOracleRandom`）用固定种子生成 2000 组随机主机
集合与随机操作历史（登记 / 变更健康 / 移除 / `Loads` / `PickLevel`，
含各类非法参数），把实现的返回值、错误、份额、记忆与逐行照规则写成
的朴素模型 `naiveAllocator` 逐字段对照，失败时打印完整操作轨迹。
