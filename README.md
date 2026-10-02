# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双速率颜色感知三色标记器（`ontology` 包）

`ontology/marker.go` 实现 RFC 4115 风格的双速率三色标记器（trTCM 变体），
带承诺桶向超额桶的溢出耦合、红色惩罚期与在线改配置。所有方法持有互斥锁，
并发调用结果等价于某个串行顺序；相同操作序列重放结果完全一致。

### 构造参数（越界即拒绝，状态不变）

| 参数 | 含义 | 范围 |
| --- | --- | --- |
| `CIR` | 每毫秒补充字节数 | 1 ~ 10^6 |
| `CBS` | 承诺桶深 | 1 ~ 10^9 |
| `EBS` | 超额桶深（可为 0） | 0 ~ 10^9 |
| `W` | 红记录窗口（毫秒） | 1 ~ 10^6 |
| `K` | 红色阈值 | 1 ~ 1000 |
| `Pn` | 基础惩罚时长（毫秒） | 1 ~ 10^6 |

初始状态：`Tc=CBS`、`Te=EBS`、`last=0`、`until=lastUntil=0`、连击 `s=0`、红队列空。
`now ∈ [0,10^12]`、`b ∈ [1,10^6]`；乘积用 64 位高低位乘法显式防 int64 溢出。

### 补充与溢出规则（`Refill`）

1. `add=(now-last)×CIR`，`Tc += add`。
2. 若 `Tc>CBS`，溢出 `o=Tc-CBS`，令 `Tc=CBS`，`Te=min(EBS, Te+o)`；超出 `EBS` 的部分丢弃。
3. `last=now`。**超额桶只接收承诺桶的溢出，不直接补充。**

批量公式与「逐毫秒各补 CIR、逐毫秒溢出」的朴素模拟逐步等价（有 2000 组随机序列对照测试）。

### `Mark(now, color, b)` 的判定与降级

固定步骤：先 `Refill(now)`（惩罚期内同样结算）。

- **惩罚期内**（`now<until`）：任何输入色一律输出 `Red`，不扣令牌、不记红；
  返回 `Penalized=true`。`now==until` 即为出惩罚期（左闭右开）。
- `Green` / `Blind`（色盲按 Green 处理）：
  `Tc>=b` → `Green`（扣 Tc）；否则 `Te>=b` → `Yellow`（降级，扣 Te）；否则 `Red`。
- `Yellow`：`Te>=b` → `Yellow`（只扣 Te）；否则 `Red`。**绝不触碰 Tc。**
- `Red`：直接 `Red`，不扣令牌、不记红。

输入字节恰计入某个输出色（字节守恒），并分别累计各输出色的报文数与字节数。

### 红记录与惩罚期边界

- 记红条件：输入色不是 `Red`、本次输出 `Red`、且不在惩罚期。
- 入队前先剔除 `t+W<=now` 的旧记录（**恰等即剔除**）。
- 队列长度（含本条）`>=K` 即触发惩罚并清空红队列，本次输出仍为 `Red`。
- 连击递增：若 `lastUntil>0` 且 `now-lastUntil<W`（上一次惩罚结束后未满一个窗口，
  **恰等不算**），`s=min(s+1,3)`，否则 `s=0`；`dur=Pn×2^s`，
  `until=now+dur`、`lastUntil=until`。`s` 封顶 3，最长惩罚 `8×Pn`。
- 红队列为带队首下标的单调队列并按需压缩到新切片，剔除摊还 O(1)。

### 在线改配置（`Reconfigure`）

先按**旧参数**在 `now` 结算，再 `Tc=min(Tc,CBS')`、`Te=min(Te,EBS')`
（截去部分直接丢弃，**不转入超额桶**），随后切换为新 `CIR/CBS/EBS`。
惩罚状态（`until/lastUntil/s`）与红队列保持不变。

### 错误区分

参数非法优先于时钟回退，各类原因分别由哨兵错误标识：
`ErrInvalidCIR/ErrInvalidCBS/ErrInvalidEBS/ErrInvalidWindow/ErrInvalidThreshold/
ErrInvalidPenaltyDur/ErrInvalidColor/ErrInvalidBytes/ErrInvalidNow` 与
`ErrClockRollback`（`now<last`）。被拒绝的调用不 `Refill`，`last` 与任何状态均不变。

### 可观测性

`Mark` 返回的 `Result` 含输出色、`Penalized`、扣费后 `Tc/Te`、是否仍在惩罚期、
`until`、红队列长度、`s` 以及人类可读的判定依据 `Reason`（测试日志打印输入、
输出与判定依据，便于精确复现）。`State(now)` 可在任意时刻先结算再取完整快照。

### 本地验证

```bash
# 普通测试（含 2000 组随机序列 vs 逐毫秒朴素模拟、200 组守恒界、确定性重放）
go test ./...

# 竞态检测 + 详细日志（输入/输出/判定依据）
go test -race -v ./...

# 仅跑规格示例
go test -run TestSpecExample -v ./...

go vet ./...
gofmt -l .
```

说明：若环境的 `GOCACHE` 只读，可 `export GOCACHE=/tmp/go-cache` 后再运行。

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
