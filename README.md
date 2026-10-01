# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## settlement：单一系列现金结算期权到期引擎

`settlement` 包实现单一期权系列的到期行权、指派、违约与现金划转，全部结果可精确复现。

### 构造与登记

- `NewEngine(kind, K, Mu, T)`：`kind` 为 `Call` 或 `Put`；行权价 `K`∈[1,10^6]，合约乘数 `Mu`∈[1,1000]，自动行权门槛 `T`∈[1,10^6]。
- `Trade(buyer, seller, n)`：开一对多/空批次，共享从 1 递增的批次序号 `seq`；`n`∈[1,10^6]，全部成功 Trade 的 `n` 之和 ≤ 10^9；同一账户可同时持有多空批次，互不抵消。
- `Margin(acct, g)`：追加保证金，`g`∈[1,10^12]，单账户累计 ≤ 10^15；可对任意非空账户调用。
- `Abstain(acct)`：账户级放弃自动行权，作用于该账户当前及之后新增的全部多头批次；要求账户当前至少有一个多头批次；重复登记成功且无副作用。

### 到期结算 `Settle(S)`

`S`∈[1,10^6]，只成功一次，之后所有变更操作报 `ErrExpired`。

- 每单位内在价值：认购 `v = max(S−K, 0)`，认沽 `v = max(K−S, 0)`。
- **自动行权门槛**：`v ≥ T`（恰等于 `T` 也行权）时，每个未放弃账户的全部多头批次自动行权，`Qa` 为其多头数量之和；`v < T` 则无人行权。总行权量 `Q = ΣQa`。
- **按批次序号指派**：全部空头批次按 `seq` 升序，依次分 `min(批次数量, 剩余Q)`；最后一个被触及的批次可能部分指派，其余部分作废。指派只落在 `seq` 升序的一个前缀上。
- **保证金缺口**：行权方应收 `recv_a = v×Qa×Mu`；被指派 `q` 的空头账户应付 `owe_a = v×q×Mu`（按账户汇总）；实付 `pay_a = min(保证金_a, owe_a)`（应收不抵扣应付）；缺口 `D_a = owe_a − pay_a`，总缺口 `Δ = ΣD_a`。
- **损失分摊**：`Δ > 0` 时按应收比例分担，`loss_a = floor(Δ×recv_a / Σrecv)`（`Δ×recv_a` 可超 64 位，实现用 `math/big` 128 位以上精度）；余量 `ρ = Δ − Σloss_a`（小于行权账户数）按账户字节序升序每个行权账户各加 1 直到分完。
- **账户净额** `= recv_a − loss_a − pay_a`；全部账户净额之和恒为 0。

`Settle` 返回：按账户字节序的行权清单、按 `seq` 升序的指派清单（含分得 0 的全部空头批次）、按账户字节序的违约清单（仅 `D_a > 0`）、含全部出现过账户的净现金表。

### 错误语义

按以下顺序只报第一个（均可用 `errors.Is` 区分）：

1. `ErrInvalidParam`：参数越界、空账户名、累计超限；
2. `ErrExpired`：系列已到期；
3. `ErrSelfTrade`：买方等于卖方；
4. `ErrNoLongPosition`：`Abstain` 的账户无多头批次。

被拒绝的操作不改变任何状态；参数非法的 `Settle` 不使系列到期。所有方法可并发调用，结果等价于某个串行顺序，并发 `Settle` 恰有一次成功。

### 本地验证

```bash
go test ./settlement/            # 单元测试 + 2000 组随机序列与朴素大整数模拟对照
go test -race ./settlement/      # 竞态检测
go test -v ./settlement/ -run TestRandomDifferential   # 打印每组输入、输出与判定依据
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
