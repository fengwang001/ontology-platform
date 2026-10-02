# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 日志快照与截断协调器

`coordinator.go` 实现领导者侧的本地模拟，只维护日志、应用进度、跟随者复制计划与在途快照，不包含网络 I/O。使用 `New(T, Thr, L)` 创建：

- `T`：截断后仍保留的尾部条目数，必须 `T >= 0`。
- `Thr`：自动快照阈值，必须 `Thr >= 1`；`Apply(idx)` 在 `idx-snapIndex >= Thr` 时取快照，恰等也触发。
- `L`：近邻保护允许的落后条数，必须 `L >= 0`。

### 状态量

- 日志索引从 `1` 开始，每条记录一个任期。
- `last`、`commit`、`applied` 分别是最后日志、已提交、已应用索引，初值均为 `0`。
- `snapIndex`、`snapTerm` 是最新快照的索引与任期，初值均为 `0`。
- `baseIndex`、`baseTerm` 是被截掉前缀的最后索引及任期；仍存储的第一条是 `first = baseIndex+1`。
- `TermAt(baseIndex)` 返回 `baseTerm`；小于 `baseIndex` 返回 `ErrCompacted`，大于 `last` 返回 `ErrRange`。
- 非导出计数器 `removed` 记录累计删除条目数，始终等于 `baseIndex` 的总增量。
- 任何时刻都满足 `baseIndex <= snapIndex <= applied <= commit <= last`。

### 快照与截断

取快照时先执行：

```text
snapIndex = applied
snapTerm  = TermAt(applied)
```

随后立即执行 `Compact`。截断点从 `cut = max(0, snapIndex-T)` 开始，并受以下固定点限制：

- 对每个有在途快照的跟随者，执行 `cut = min(cut, s)`，其中 `s` 是该快照索引；这保证在途快照起点之后的条目不被删除。
- 对没有在途快照的跟随者，仅当 `last-match <= L` 时执行 `cut = min(cut, match)`；恰等 `L` 也保护，落后 `L+1` 不保护。
- `Ack` 只更新 `match/next`，不触发压缩；近邻条件只在执行 `Compact` 时评估。
- 只有 `cut > baseIndex` 才删除条目；删除前记录 `baseTerm = TermAt(cut)`，`baseIndex` 只增不减。
- 取快照、`FinishSnapshot`、`AbortSnapshot`、`DropPeer` 后都会重新执行一次 `Compact`。

`Apply` 连同其自动触发的快照与压缩是一个原子步骤；所有公开方法都由同一把互斥锁保护，并发执行的结果等价于某个合法串行顺序。

### 跟随者计划

`AddPeer(id)` 初始化 `match=0`、`next=last+1`。

- `Ack(id,m)` 要求当前 `match <= m <= last`，成功后 `match=m`、`next=m+1`。
- `Retreat(id,n)` 要求 `match < n <= 当前 next`，成功后只修改 `next=n`。
- `Plan(id)` 的判定顺序为：
  1. 有在途快照：`Installing`，携带在途快照索引和任期。
  2. `next <= baseIndex`：`NeedSnapshot`，携带当前 `snapIndex/snapTerm`。
  3. 否则追加条目：`PrevIndex=next-1`、`PrevTerm=TermAt(PrevIndex)`、`From=next`、`To=last`；`From > To` 表示空追加。

注意分界是取等的：`next == baseIndex` 所需条目已经被截掉，必须安装快照；`next == baseIndex+1` 第一条仍可追加，此时前一条索引正好是 `baseIndex`，前一条任期取 `baseTerm`。

`StartSnapshot(id)` 只在计划为 `NeedSnapshot` 时成功，固定的在途快照索引是调用时的当前 `snapIndex`。每个跟随者至多一个在途快照。`FinishSnapshot(id)` 先解除固定、把 `match=max(match,s)` 并设置 `next=match+1`，然后压缩，因此可能解除固定后立刻推进到新的截断边界。`AbortSnapshot(id)` 只解除固定并压缩，不改变 `match/next`。

### 错误次序

- 构造参数非法或空 `id` 返回 `ErrParam`，参数检查优先于其他状态判断；`term < 1` 也是 `ErrParam`。
- 任期非递减冲突返回 `ErrTerm`；普通索引越界返回 `ErrRange`；已压缩索引返回 `ErrCompacted`。
- 手动快照没有新应用进度时返回 `ErrNoProgress`。
- 跟随者重复创建返回 `ErrExists`，不存在返回 `ErrUnknownPeer`。
- `StartSnapshot` 依次检查 `ErrUnknownPeer`、已有在途快照的 `ErrInFlight`、计划不是 `NeedSnapshot` 的 `ErrNotNeeded`。
- `FinishSnapshot` 与 `AbortSnapshot` 没有在途快照时返回 `ErrNotInFlight`。
- 被拒绝的操作不会修改任何状态，也不会触发压缩。

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

### 协调器验证

```bash
# 全量单元测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机操作序列的输入、输出与每条操作的判定依据
go test -v -run TestRandomCompare2000

# 指定一个随机种子对应的子序列
go test -v -run 'TestRandomCompare2000/seed_4$'
```

随机对照测试使用测试内逐步实现的朴素模型，独立维护完整日志 map、快照边界和跟随者状态；每步同时比对所有状态、计划、错误以及 `removed` 计数。
