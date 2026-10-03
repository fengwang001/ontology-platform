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

## ACME 证书订单状态机

根包通过 `ontology.New(Config)` 创建状态机。`Config` 字段为：

- `AuthPendingTTL` (`Ta`)：新 pending 授权的存活秒数。
- `AuthValidTTL` (`Tv`)：验证通过后 valid 授权的有效秒数。
- `OrderTTL` (`To`)：active 订单的有效秒数。
- `FailureWindow` (`H`)：验证失败的滑动窗口。
- `FailureThreshold` (`F`)：窗口内达到该失败次数即限流。
- `NonceCapacity` (`C`)：一次性 nonce 池容量。
- `PendingAuthLimit` (`Pm`)：每账户 pending 授权上限。

所有配置必须在题面要求的闭区间内；非法配置、空账户、非法或重复标识符、越界 `now` 等返回 `*ontology.Error`，其 `Kind` 可区分参数、时钟、nonce、对象、状态、CSR、限流和配额错误。所有方法以互斥保护，并发结果等价于某个串行顺序。

### 授权有效状态

授权存储状态为 `pending`、`valid`、`invalid`、`deactivated`。按查询或操作时刻 `now` 派生的有效状态为：

- 存储状态是 `pending` 或 `valid`，且 `now >= expires`：`expired`。
- 其他情况：等于存储状态。

因此 `now == expires` 的第一刻已经到期，`now == expires-1` 仍有效。到期只改变派生态，不回写存储状态；`Report` 遇到已到期 pending 授权会返回状态冲突且不改变对象。

### 订单状态

订单存储状态为 `active` 或 `valid`，派生顺序为：

1. 存储状态为 `valid`：始终派生为 `valid`，之后授权到期或被停用都不影响该订单。
2. `now >= order.expires`：派生为 `invalid`。
3. 任一授权有效状态为 `invalid`、`deactivated` 或 `expired`：派生为 `invalid`。
4. 所有授权有效状态均为 `valid`：派生为 `ready`。
5. 其余情况：派生为 `pending`。

`Finalize` 只接受派生态为 `ready` 的订单；CSR 标识符列表必须自身合法且与订单标识符列表作为集合完全相同，顺序不影响。成功后订单存储状态变为 `valid`，证书序号从 1 开始全局递增。

### 授权复用

`NewOrder` 按输入顺序处理标识符，只复用同一账户、同一标识符、当前有效状态为 `valid` 的授权。选择规则为：

1. `expires` 最大。
2. `expires` 相同时，授权编号最小。

没有可复用授权时才新建 pending 授权，新授权 `expires=now+Ta`。复用授权不计入待验证配额；多个订单可共享同一授权，所以该授权到期或被 `Deactivate` 时，所有仍 active 且引用它的订单都会按派生态变为 `invalid`。

内部按 `(account, identifier)` 维护最大 `expires`、最小编号的堆索引；查找时只清理该键下已失效的堆项，每项至多删除一次，同键外的历史授权不进入考察计数。pending 集合和失败队列同样惰性清理。

### Nonce 池

`Nonce()` 无参数且总是成功，依次发出 `1,2,3,...`。发出的值放入容量为 `C` 的有界池：

- 池满时先淘汰当前池中编号最小、最早发出且尚未消费的值。
- 带 nonce 的操作先检查它是否仍在池中；从未发出、已消费、已淘汰均为 nonce 无效。
- 只有操作最终被接受才消费 nonce；任何拒绝都保留 nonce。

池中用发出顺序链表和节点映射，放入、容量满淘汰和消费均为 O(1)。

### 限流与配额

失败记录按 `(account, identifier, failure_time)` 保存。`NewOrder` 在 nonce 检查后、配额检查前，按订单标识符顺序统计：

- `failure_time + H > now` 仍在窗口内。
- `failure_time + H == now` 已出窗口。

某标识符窗口内失败数达到 `F` 即返回限流，定位第一个触发的标识符和当前计数；此操作不创建授权或订单、不消费 nonce。

配额统计中，`p` 是该账户当前有效状态为 `pending` 的授权数，`q` 是本次标识符里没有可复用 valid 授权、需要新建授权的数量。`p+q > Pm` 时拒绝并返回 `p,q`；`p+q == Pm` 允许通过。验证成功、验证失败、停用、到期都会释放 pending 配额；其他账户互不影响。

### 时钟与只读操作

所有写操作共用一个单调时钟：被接受的写操作推进已见最大 `now`，更小的 `now` 是时钟回退。`Status` 与 `Authorization` 是只读操作，也拒绝回退时间，但不推进时钟。被拒绝的操作不修改授权、订单、失败记录、编号、证书计数、nonce 池或时钟。

### 本地验证

如果系统没有全局可用的 Go 工具链，可直接使用仓库环境中的 `/usr/local/go/bin/go`：

```bash
# 普通测试
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...

# 竞态检测
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...

# 随机朴素模拟（前 10 组的前 20 步打印输入、输出和判定依据）
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -v -run TestRandomizedNaiveSimulation ./...

/usr/local/go/bin/go vet ./...
```

随机测试共重放 2000 组、每组 90 个操作，并与直接扫描全部授权和失败记录的朴素模型逐项比较编号、对象、错误类别、定位信息与派生状态。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
