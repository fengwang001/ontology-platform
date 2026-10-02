# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## options：现金结算期权到期行权与指派引擎

`options` 包实现单一系列现金结算期权的到期行权与指派引擎
（`NewEngine(kind, K, Mu, T)`，支持认购 `Call` 与认沽 `Put`）。
引擎登记多空成对的持仓批次与空头保证金，到期结算时自动行权、
按开仓先后指派空头、按保证金处理支付缺口并由行权方分担损失，
全部行权、指派、违约与现金划转均可精确复现。

### 登记接口

- `Trade(buyer, seller, n)`：新增一个多头批次（买方、n、seq）与一个
  空头批次（卖方、n、seq），seq 从 1 起递增。同一账户可同时持有
  多头与空头批次，两者互不抵消。全部成功 Trade 的 n 之和不得超过 10^9。
- `Margin(acct, g)`：为账户追加保证金 g（1 到 10^12，累计不超过 10^15），
  可对任意非空账户（含从未交易过的账户）调用。
- `Abstain(acct)`：账户级放弃自动行权标志，作用于该账户的全部多头批次
  （含登记之后、到期之前新增的批次）。要求账户当前至少有一个多头批次；
  重复登记成功且无副作用。

### 到期结算 Settle(S)

`Settle(S)` 只能成功一次，成功后任何操作都报“已到期”。

- **自动行权门槛**：每单位内在价值 v 对认购为 `max(S−K, 0)`，对认沽为
  `max(K−S, 0)`。当 `v ≥ T`（恰等于 T 也行权）时，每个未放弃账户的全部
  多头批次自动行权，账户行权量 Qa 为其多头批次数量之和，总行权量
  Q 为全部 Qa 之和；`v < T` 则无人行权。
- **按批次序号的指派**：全部空头批次按 seq 升序依次分配 Q，每个批次分得
  `min(批次数量, 剩余待分配量)`；最后一个被触及的批次可能只被部分指派，
  其余未分配部分到期作废。被指派总量恒等于 Q。
- **保证金缺口**：行权账户应收 `recv_a = v×Qa×Mu`；被指派 q 的空头批次
  使其账户应付增加 `v×q×Mu`，按账户汇总为 owe_a。实付
  `pay_a = min(保证金_a, owe_a)`（账户自己的应收不抵扣应付），缺口
  `D_a = owe_a − pay_a`，总缺口 `Δ = Σ D_a`。
- **损失分摊**：Δ > 0 时由行权账户按应收比例分担，
  `loss_a = floor(Δ×recv_a/ΣT)`（ΣT 为全部 recv 之和，恰等于全部 owe
  之和；乘积 Δ×recv_a 可超 64 位，实现中用 big.Int 计算）；余量
  `ρ = Δ − Σ loss_a`（小于行权账户数）按账户字节序升序每个行权账户
  各加 1 直到分完。账户净额 = `recv_a − loss_a − pay_a`，
  全部账户净额之和恒为 0。
- **返回**：按账户字节序升序的行权清单（账户、Qa）、按 seq 升序的指派
  清单（seq、账户、分得量，含全部空头批次）、按账户字节序升序的违约
  清单（账户、D_a，仅 D_a > 0）与按账户字节序升序的净现金表（含全部
  出现过的账户）。

### 错误语义

参数非法（构造参数、数量、金额、账户名、结算价越界等）统一拒绝；
其余原因可区分：已到期（`ErrExpired`）、自成交（`ErrSelfTrade`）、
无多头持仓（`ErrNoLong`）。多种原因同时成立时按参数非法、已到期、
自成交或无多头持仓的顺序只报第一个。被拒绝的操作不改变任何状态，
因参数非法被拒的 Settle 不使系列到期。所有接口可并发调用，结果等价于
某个串行顺序；并发的多次 Settle 中恰有一次成功。

### 本地验证

```bash
# 单元测试（门槛边界、认购/认沽、部分指派、账户级放弃、应收不抵扣
# 应付、保证金恰等于 owe、余量按字节序分配、Δ=ΣT、64 位溢出大数等）
go test ./options -v

# 2000 组随机序列与朴素大整数模拟对照（日志打印输入、输出与判定依据）
go test ./options -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./options
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
