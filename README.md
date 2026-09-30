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

## 分布式死锁检测器（`deadlock` 包）

跨站点的分布式死锁检测：各站点只掌握本地锁等待关系，通过沿等待边
转发探测消息发现跨站点等待环并选出牺牲者。

### 锁与等待模型

- 锁分属各站点，分共享（S）/排他（X）两种模式，仅 S+S 相容。
- 请求与持有者不相容、或已有人排队时进入队列；事务等待全部不相容
  持有者及排在它前面的不相容等待者。
- 授予按先来先服务：队首起，与当前全部持有者及更靠前的未授予等待者
  相容即可授予；再次请求自己已持有的锁视为成功。
- 等待边不做缓存，每次由当前锁表实时现算，因此已解除的等待不会被
  误判。

### 探测转发与丢弃规则

- 事务开始等待时发起探测，探测携带发起者与途经事务序列
  （`Path[0]` 即发起者），经注入的网络（`Network` 接口）延迟、
  乱序投递。
- 投递时按序判定：
  1. 所沿等待边此刻已不存在 → 丢弃（`reason=edge-gone`）；
  2. 到达序列中已有的非发起者事务（环不含发起者）→ 丢弃，不再
     转发（`reason=cycle-without-initiator`）；
  3. 否则把当前事务追加进序列，转发给它此刻等待的每个事务。

### 死锁确认条件

- 探测回到发起者时，须复核途经的**每一条**等待边此刻仍然存在，
  全部成立才判定死锁；任一缺失即丢弃（`reason=stale-cycle`）。
  因此被中止的事务在判定时刻必然处在真实存在的等待环上，且全部
  消息投递完毕后不存在等待环。

### 牺牲者选择

- 牺牲者为环上开始时间戳最大（最年轻）的事务；中止后释放其全部
  锁并按 FCFS 授予等待者。判定与中止在同一把互斥锁内完成，两个
  发起者同时发现同一环也只中止一个事务。

### 错误语义

请求不存在的站点/锁、已结束或正在等待的事务再发请求、释放未持有
的锁、事务号或开始时间戳重复，均以可区分的 `*deadlock.Error`
（`ErrCode`）整体拒绝，且不改变锁表或等待关系。

### 本地验证方法

```bash
# 全部场景：三站点成环、共享锁多持有者环、在途探测丢弃、
# 双发起者单牺牲者、不含发起者的环、非法操作拒绝、重放确定性
go test -v ./deadlock

# 并发调用 + 竞态检测
go test -race ./deadlock
```

测试注入 `ManualNetwork` 显式控制投递顺序（确定性重放）或
`AsyncNetwork` 注入随机延迟（并发压测）。运行期可用
`Manager.WaitsFor` / `Manager.HasCycle` / `Manager.Victims` 本地
核对等待关系与判定结果；日志逐条打印输入操作、输出结果与判定依据
（`op=... result=...`、`probe=send/forward/drop ... reason=...`、
`deadlock=confirmed cycle=... victim=... reason=max-start-ts`）。
