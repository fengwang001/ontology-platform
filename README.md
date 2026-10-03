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

## 邮件抑制名单与双重确认恢复器

### 地址规范化

`NewManager(S, W, SoftTTL, TTL, Kd, Wd, DomTTL)` 创建线程安全的抑制状态机。除 `S` 范围为 `1..16`、`Kd` 范围为 `1..1000` 外，其余构造参数范围均为 `1..10^9`；越界返回 `ErrInvalidParameter`。

地址必须恰有一个非空本地部分和非空域名，域名至少有一个点且无空标签，不能包含 `<=0x20` 或 `0x7f` 字节，总长度不能超过 254。规范化严格按以下顺序执行：

1. 全部 ASCII 字母转小写。
2. 域名 `googlemail.com` 视为 `gmail.com`，因此地址状态键合并。
3. 本地部分从第一个 `+` 起截断；截断后为空则非法。
4. 对 `gmail.com` / `googlemail.com` 的本地部分删除全部 `.`；删除后为空则非法。

因此 `a.b+x@Gmail.com` 与 `AB@googlemail.com` 都规范化为 `ab@gmail.com`。非 Gmail 系域名保留本地部分中的点。

### 原因等级与事件

等级从低到高为 `None`、`Soft`、`Unsub`、`Hard`、`Complaint`；域级原因 `Domain` 只用于查询返回。所有操作的 `now` 必须在 `0..10^12` 且不小于已接受事件的最大时刻。地址非法先于时钟回退报告；被拒绝的操作不改变任何状态，也不触发软抑制惰性恢复。

- `Soft`：只在当前无抑制时记录。先保留严格大于 `now-W` 的历史时刻，再追加 `now`；数量达到 `S` 后置为 `Soft`，记录 `since=now`、`softUntil=now+SoftTTL` 并清空时刻表。
- 惰性恢复：后续事件或查询开始时，若原因为 `Soft` 且 `now >= softUntil`，恢复为 `None` 并清空时刻表。`now == softUntil` 已恢复。
- `Hard`：总是先刷新 `lastHard=now`；当前原因低于 `Hard` 时升级为 `Hard`、更新 `since` 并清空软退信日志；已经是 `Hard/Complaint` 时 `since` 不变。
- `Complaint`：当前原因低于 `Complaint` 时升级；投诉永远不能通过确认恢复。
- `Unsub`：仅当原因为 `None` 或 `Soft` 时置为 `Unsub`；在 `Hard/Complaint` 下被忽略。

### 同域硬退信聚合

每次硬退信后，只扫描该规范化域名自己的地址集合，统计 `lastHard` 严格大于 `now-Wd` 的不同规范地址数。数量达到 `Kd` 时：

- `domUntil = max(domUntil, now+DomTTL)`；
- 若触发前域级抑制未生效（`domUntil <= now`），设置 `domSince=now`；
- 若抑制已在生效，则只延长截止时刻，不改变原来的 `domSince`。

内部非导出计数器记录最近一次硬退信扫描考察的地址数；该计数器只遍历同域地址集合，因此不会超过该域名的地址数。

### 双重确认恢复

`RequestConfirm(addr, now)` 仅允许 `Hard` 或 `Unsub`：

- `Complaint` 返回 `ErrRecoveryForbidden`；
- 其他原因返回 `ErrNoRecoveryNeeded`；
- 通过后签发全局递增、从 1 开始且不复用的编号，保存 `(seq, issuedAt)`，新请求覆盖旧令牌。

`Confirm(addr, seq, now)` 严格按顺序检查：无令牌、编号不是最新、`now >= issuedAt+TTL`（恰等即过期）、当前为投诉、当前不是 `Hard/Unsub`、`issuedAt <= since`。全部通过后清空原因、软退信日志和令牌，并记录 `confirmedAt=now`。

`IsSuppressed(addr, now)` 先执行软抑制惰性恢复；地址原因不是 `None` 时返回该原因。否则若 `domUntil > now` 且 `confirmedAt < domSince`，返回 `Domain`；`confirmedAt == domSince` 已豁免。

### 本地验证

```bash
# 全量测试与竞态检测
GOCACHE=/tmp/ontology-go-cache go test -race -v ./...

# 随机规则对拍（2000 组确定性序列；失败日志包含输入、输出和判定依据）
GOCACHE=/tmp/ontology-go-cache go test -v -run TestRandomSequencesAgainstNaiveModel ./...

# 格式与静态检查
/usr/local/go/bin/gofmt -w .
GOCACHE=/tmp/ontology-go-cache go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
