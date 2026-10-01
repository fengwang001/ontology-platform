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

## 优先级分层故障转移负载分配器（`failover` 包）

`failover.Allocator` 把固定的 100 份流量按优先级层（层号越小优先级越高）
的健康主机比例分配到各层，含过供给因子、恢复迟滞、单层份额上限与恐慌回退。

### 构造与校验

`New(L, F, D, Cap)`：

- `L`：层数，层号 `0..L-1`，合法范围 1..16。
- `F`：过供给因子（百分比整数，`140` 即 1.4 倍），合法范围 100..1000。
- `D`（Δ）：恢复迟滞阈值，合法范围 1..100。
- `Cap`：每层份额上限，长度必须为 `L`，每项 1..100，总和不小于 100。

任一条件不满足整体返回 `ErrInvalidConfig`，不产生任何分配器对象。

### 主机操作

- `AddHost(level, id, healthy)`：登记主机。`id` 为全局唯一的非空字符串。
  拒绝顺序只报第一个：层号越界（`ErrLevelOutOfRange`）→ id 为空
  （`ErrEmptyID`）→ id 已存在（`ErrHostExists`）。
- `RemoveHost(id)` / `SetHealth(id, healthy)`：id 不存在返回
  `ErrHostNotFound`；把健康位设成相同值也成功。
- 所有方法持有互斥锁，并发调用结果等价于某个串行顺序；被拒绝的操作
  （含容量不足）不改变任何主机、健康位与迟滞记忆。

### 层健康度公式

设第 `p` 层共 `t` 台主机、其中 `h` 台健康：

```
raw_p = 0                                          （t = 0）
raw_p = min(100, floor(h * F / t))                 （t > 0）
```

### 迟滞记忆更新规则

每层维护记忆值 `cur_p`（初值为空）。每次**成功**的 `Loads`（`PickLevel`
内部同样执行一次）按旧记忆计算新值：

- 记忆为空、`cur_p == 0`、`raw_p <= cur_p`（下降立即生效）、或
  `raw_p >= cur_p + Δ` 时：`cur_p = raw_p`。
- 仅当 `0 < cur_p < raw_p < cur_p + Δ`（恢复但未越过迟滞带上沿）时保持
  旧 `cur_p`。

记 `S = Σ cur_p`。`Loads` 被容量不足拒绝时不更新任何 `cur_p`；成功之后
`cur_p == 0` 当且仅当此刻 `raw_p == 0`。

### 三种份额分配分支（100 份）

先做无主机检查：没有任何主机返回 `ErrNoHosts`。

1. `S >= 100`（贪心）：按层号升序，`load_p = min(剩余, cur_p)`，剩余初值
   100；后层可能被前层吃光而得 0。
2. `1 <= S <= 99`（缩放）：`load_p = floor(cur_p * 100 / S)`；缩放后不足
   100 的余数全部加给**层号最小且 `cur_p > 0`** 的层（前面层 `cur=0`
   时顺延到下一个正层）。
3. `S == 0`（恐慌回退）：100 份全部给层号最小且至少有一台主机的层
   （第 0 层无主机则顺延到第 1 层……）。

### 限额再分配

1. 按层号升序把每层超过 `Cap_p` 的部分截到 `Cap_p`，截下的总量记为 `X`。
2. 再按层号升序把 `X` 依次补给有资格的层，每层至多补到 `Cap_p`，补完为止：
   - 正常分支资格为 `cur_p > 0`；
   - 恐慌分支资格为该层有主机（`t_p > 0`）。
3. `X` 仍未补完时返回 `ErrInsufficientCapacity`（在无主机检查之后才可能
   发生），且本次不更新任何记忆。

成功时各层份额非负、不超过各自 `Cap`，总和恒为 100。

### PickLevel 区间

`PickLevel(r)`：先校验 `r` 必须在 `0..99`（否则 `ErrInvalidPoint`），再
执行一次 `Loads`（同样更新记忆，因此之后再可能返回无主机 / 容量不足）。
返回满足 `r < Σ_{q<=p} load_q` 的最小层号，即第 `p` 层服务左闭右开区间：

```
[ Σ_{q<p} load_q , Σ_{q<=p} load_q )
```

份额为 0 的层区间为空，永远不会被返回。错误检查顺序为：点非法 → 无主机
→ 容量不足。

### 本地验证

```bash
# 全量测试（含 2000 组随机主机集合与随机操作历史，对照独立朴素模型）
go test -race -v ./failover/

# 仅看随机对照与判定日志
go test -run TestRandomDifferential -v ./failover/

# 代码检查
gofmt -l .
go vet ./...
```

随机测试与固定用例通过 `t.Logf` 打印输入（`L/F/D/Cap`、主机集合、操作
序列）、输出（各层份额、错误码、选中层）与判定依据（raw/cur、所在分支、
余数归属、截断与补给量），用 `-v` 可查看。
