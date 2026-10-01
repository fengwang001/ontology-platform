# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## fdb：带注入时钟的以太网学习交换机转发表

`fdb` 包（`ontology/fdb`）实现按源地址学习、按目的地址转发的 FDB。
所有变更类调用（`Frame` / `AddStatic` / `FlushPort`）都携带注入时刻
`t`（纳秒），处理顺序固定为：**校验参数 → 时刻校验 → 清除过期动态
表项 → 学习 → 转发**。被拒绝的调用不改变任何表项、时钟与计数。

### 构造与基本规则

- `New(N, A, C)`：端口数 `N`（1–64，编号 `0..N-1`）、老化时长 `A`
  （纳秒，须为正）、动态表项容量 `C`（须为正；静态表项不占容量）。
  参数非法返回 `ErrInvalidConfig`。
- 表项键为 `(vlan, mac)`，值为端口与最近见到时刻 `seen`。VLAN 取
  1–4094；MAC 首字节最低位为 1 者是组播（全 FF 广播也算组播）。
- 动态表项在 `t - seen >= A` 时过期（左闭区间）；静态表项永不过期。

### 学习（Frame 的第二步）

- 源为组播：不学习，帧仍继续转发。
- 无表项：新建动态表项 `(p, seen=t)`，`Learned++`；若动态表项已达
  `C` 个，先淘汰 `seen` 最早者，并列时取 `(vlan, mac)` 按字节序最小
  者（先比 VLAN，再逐字节比 MAC），`Evictions++`。
- 已有动态表项：端口改为 `p`、`seen` 置为 `t`（无论端口是否变化都
  刷新）；端口变化时 `Moves++`。
- 已有静态表项：不改动；若入端口不等于其端口，本帧丢弃（返回空
  列表，`SecurityDrops++`，不再转发）。

### 转发（Frame 的最后一步）

- 目的为组播，或 `(vlan, d)` 无表项：泛洪到除入端口外的全部端口
  （升序），`Floods++`。
- 有表项且其端口等于入端口：过滤（返回空列表），`Filtered++`。
- 否则只发往该表项端口。源与目的相同的单播帧先学习后被过滤。

### 其他接口

- `AddStatic(v, mac, port, t)`：mac 不得为组播
  （`ErrMulticastStaticMAC`）；覆盖同键动态表项（`Overridden++`），
  已有静态表项则改端口。
- `FlushPort(port, t)`：删除该端口全部动态表项（`Flushed` 按条数
  累加），静态表项不受影响。
- `Lookup(v, mac, t)`：只读；已过期者视为未命中；不改任何状态与
  计数。
- `Len()`：当前存放的动态表项数（含已过期但尚未被清除者）。
- `Counters()`：计数快照。任意时刻满足守恒式
  `Len() + Evictions + Expired + Flushed + Overridden == Learned`。

### 时刻与错误

时刻不得早于上一次成功调用的时刻，否则返回 `ErrClockBackward`；
端口越界 `ErrPortOutOfRange`、VLAN 越界 `ErrVLANOutOfRange`、构造
参数非法 `ErrInvalidConfig`，均可用 `errors.Is` 区分。全部方法可
并发调用，结果等价于某个串行顺序；相同调用序列重放得到完全相同
的返回值与计数；返回的列表不与内部状态共享存储。

### 本地验证

```bash
go test ./fdb/                 # 单元测试 + 与朴素逐条扫描实现的差分对照
go test -race -v ./fdb/        # 竞态检测；差分日志打印输入、输出与判定依据
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
