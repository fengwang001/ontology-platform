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

## 地址租约服务器（`lease` 包）

`lease.Server` 从整数地址闭区间 `[Low, High]` 中为字符串客户端分配地址，
所有时间参数与调用方传入的 `now` 均为整数时刻（区间统一左闭右开）：

- `L = LeaseTime`：租约时长，租约为 `[起, 起+L)`。
- `H = ReserveTime`：暂留（预留）时长，暂留为 `[起, 起+H)`。
- `Q = Quarantine`：拒收隔离时长，隔离为 `[拒收时刻, 拒收时刻+Q)`。

构造：

```go
srv := lease.New(lease.Config{
    Low: 0, High: 99,
    LeaseTime: 3600, ReserveTime: 60, Quarantine: 300,
    Logger: lease.DefaultLogger(os.Stdout), // 可选；记录输入/输出/判定依据
})
```

API（`now` 必须单调不回退；被拒绝的操作不改变任何状态）：

- `Discover(now, client, requested *int64) (addr, error)`：发现并暂留。
- `Confirm(now, client, addr) error`：确认暂留或续租，租约终点恒为 `now+L`。
- `Release(now, client, addr) error`：仅持有者可释放。
- `Reject(now, client, addr) error`：持有者或暂留者可拒收并隔离地址。

### 地址空闲判定

某地址在时刻 `t` 空闲，当且仅当同时满足：

- 无有效租约（`t >= 租约终点` 即失效，恰在终点失效）；
- 无有效暂留（`t >= 暂留终点` 即失效）；
- 不在隔离期（`t >= 拒收时刻+Q` 即解除，恰在终点解除）。

### Discover 选址优先次序（按序取第一个满足者）

1. 该客户端**已有**的有效地址：有效租约优先；否则沿用其有效暂留，
   且不刷新暂留起点。
2. 该客户端**最近一次持有过**的地址（`lastHeld`）空闲。
3. 请求地址在池内且空闲。
4. 空闲地址中「租约终止时刻」最早者；从未有过租约者视为最早；
   并列取地址小者。

「租约终止时刻」的三种取值：

- 租约自然过期：取租约终点；
- 被释放：取释放时刻；
- 持有者拒收：取拒收时刻；暂留者拒收不改变它。

### Confirm / Release / Reject 规则

- Confirm 要求 `addr` 是该客户端的有效暂留或有效租约；成功后租约为
  `[now, now+L)`（续租从 `now` 重新起算，不叠加剩余时间），并清除暂留。
- Release 仅当前有效租约的持有者可调用；释放后地址立即空闲（不隔离）。
- Reject 允许持有者或暂留者：清除其租约/暂留，地址隔离 `[now, now+Q)`。

拒绝顺序（只报第一个错误）：

- Discover：时钟回拨、客户端为空、请求地址不在池内、池已耗尽。
- Confirm：时钟回拨、地址不在池内、地址被他人持有或暂留、
  该客户端无有效暂留或租约。
- Release/Reject：时钟回拨、地址不在池内、非持有者（拒收含暂留者）。

实现以单一互斥锁串行化状态变更；选址仅依赖传入的整数时间，无内部随机
或异步时钟，因此相同操作序列重放结果完全相同。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./lease

# 查看带判定依据的操作日志：测试即使用 lease.DefaultLogger
go test -v -run TestLoggingContent ./lease

# 覆盖率
go test -cover ./lease
```

覆盖场景：历史地址优先于请求地址、按租约终止时刻最早选址（过期终点
与释放时刻的区别）、租约恰在终点失效、续租不叠加剩余、拒收隔离后不可选、
暂留过期后可被他人取得、拒绝优先级与状态原子性、并发不变量、重放确定性。
