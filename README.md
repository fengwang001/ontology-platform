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

## 邮件抑制名单（`suppression` 包）

按投诉 > 硬退信 > 退订 > 软退信 > 无 的等级维护每个规范化地址的抑制状态，
支持同域硬退信聚合触发域级抑制，并经带时效的确认令牌恢复发送。

### 构造参数

`New(Config)` 参数越界返回 `ErrInvalidParam`：

- `SoftThreshold` S：软退信阈值，1 到 16
- `SoftWindow` W：软退信窗口，1 到 1e9
- `SoftTTL`：软抑制时长，1 到 1e9
- `TokenTTL`：确认令牌有效期，1 到 1e9
- `DomainThreshold` Kd：域级硬退信地址数，1 到 1000
- `DomainWindow` Wd：域级窗口，1 到 1e9
- `DomainTTL` DomTTL：域级抑制时长，1 到 1e9

所有事件携带 `now`（须不小于已接受事件的最大 now，否则 `ErrClockBackwards`）。
被拒绝的操作不改变任何地址、域名状态与最大 now，也不执行惰性恢复。

### 地址规范化

输入须恰含一个 `@`，本地部分与域名非空，域名至少含一个 `.` 且无空标签，
不含不大于 0x20 的字节与 0x7f，总长不超过 254。规范化按固定顺序：

1. 全部 ASCII 字母转小写；
2. 域名 `googlemail.com` 改为 `gmail.com`；
3. 本地部分从第一个 `+` 起截断（截后为空则非法）；
4. 域名为 `gmail.com` 时去掉本地部分全部 `.`（去后为空则非法）。

例如 `a.b+x@Gmail.com` 与 `AB@googlemail.com` 同为 `ab@gmail.com`。

### 事件对等级与时刻的影响

每个操作开始时，若原因为 soft 且 `now >= softUntil`，先惰性恢复为无并清空软退信时刻表。

- `Soft`：原因不为无时忽略；否则保留时刻表中严格大于 `now-W` 的时刻并追加 `now`，
  个数不小于 S 时原因置 soft、`since=now`、`softUntil=now+SoftTTL`、时刻表清空。
- `Hard`：`lastHard` 先置 `now`（无论后续是否忽略）；原因等级低于 hard 时置 hard、
  `since=now`、清空时刻表，否则 `since` 不变；随后做域级聚合（见下）。
- `Complaint`：原因等级低于 complaint 时置 complaint、`since=now`。
- `Unsub`：原因为无或 soft 时置 unsub、`since=now`，否则忽略。

原因等级不会因事件降低（除 soft 惰性恢复与 `Confirm` 外）。

### 域级聚合

`Hard` 之后统计该域名下 `lastHard` 严格大于 `now-Wd` 的地址数（只扫描该域名的地址），
不小于 Kd 时 `domUntil = max(domUntil, now+DomTTL)`；若触发前域级未在抑制中
（`domUntil <= now`）则 `domSince = now`。

### 令牌恢复

- `RequestConfirm(addr, now)`：原因为 complaint 返回 `ErrRecoveryForbidden`（禁止恢复）；
  原因不是 hard 或 unsub 返回 `ErrRecoveryUnneeded`（无需恢复）；否则签发令牌
  `(全局递增编号, now)`，覆盖旧令牌并返回编号。编号全局递增不复用。
- `Confirm(addr, seq, now)` 依次检查（只报第一个）：无令牌、编号不是最新、
  `now >= issuedAt+TokenTTL`（恰等即过期）、原因为 complaint、原因不是 hard 或 unsub、
  `issuedAt <= since`（令牌早于最近一次抑制起点）。通过后原因置无、清空时刻表与令牌、
  `confirmedAt = now`。
- `IsSuppressed(addr, now)`：惰性恢复后原因不为无则返回对应原因；否则若所在域名
  `domUntil > now` 且 `confirmedAt < domSince` 返回域级抑制；否则不抑制。

### 本地验证

```bash
# 规则单元测试 + 2000 组随机序列与朴素模拟对拍（-v 打印输入、输出与判定依据）
go test ./suppression/
go test -race -v ./suppression/
go test -run TestFuzzAgainstModel -v ./suppression/
```
