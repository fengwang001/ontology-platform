# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前包含 `group` 包：消费组的**粘性分区分配器**（sticky partition
assignment），把固定编号的分区在成员加入/离开时重分配，目标是均衡、迁移
最少、结果确定可复现，且可被多执行体并发调用。

## 环境要求

- Go 1.26+（`go version` 确认）

若 `go` 不在 PATH，可使用 `/usr/local/go/bin/go`；构建缓存目录只读时设置
`GOCACHE=/tmp/gocache`。

## 运行

```bash
# 拉取依赖
go mod tidy

# group 为库包，无需独立运行入口；在代码中引入：
#   import "ontology/group"
#   g, _ := group.New(分区数, 成员上限, logger)
#   res, err := g.Apply([]group.Change{{Type: group.Join, Member: "m1"}})
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

`group` 包测试覆盖：加入/离开交错脚本、均衡与迁移最少（每步与暴力枚举最优
解对照）、释放/补齐的判定依据、同批顺序无关与重放确定性、各类非法输入整批
拒绝且代数/分配不变、空组边界、多 goroutine 并发读写与自检（`-race`）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 粘性分区分配规则（`group`）

### 批与原子性

- 变更以**批**提交：`Apply([]Change)` 先把一批 `Join`/`Leave` 整体作用于
  成员集合，然后**只做一次**重分配。成功后代数（generation）加一。
- 成员集合的变化是集合运算，因此同一批内变更的书写顺序不可观察：同一批
  变更集合无论怎么排列，逐分区结果完全一致。
- 任何非法输入都**整批拒绝**，返回可 `errors.Is` 区分的哨兵错误；成员集合、
  分配、代数均不改变（失败不留痕）。

### 配额

设批后成员数为 `M`、分区总数为 `N`：

- 基础配额 `q = N / M`（整除），余数 `r = N % M`。
- 先给每个成员发 `q`；`r` 个余数名额按**批前持有数降序**排序，持有数并列
  时按**成员标识升序**，前 `r` 名各得 `q+1`。
- 因此批后任意两成员持有数之差不大于一。

### 释放与补齐

- 离开成员的分区直接进入无主池。
- **释放**：持有数超过自身配额的成员，按分区编号**从大到小**释放超出部分
  （即尽量保留小编号分区）。
- **补齐**：无主分区按编号**升序**逐个处理，每次交给**当前配额缺口最大**
  （配额 − 当前持有数）的成员；缺口并列时取**标识最小**者，直到缺口清零。

### 迁移计数

- 一个分区计入迁移，当且仅当它**批前有属主**且**批后换主**（属主变为另一
  成员；成员全部离开时变为无主也计为换主）。
- 批前无主（如首次分配）的分区不计迁移。
- 迁移只来自两类强制移动：离开者持有的分区、超配额成员必须释放的分区，
  因此该计数是下界，即迁移最少；`Result.Migrations` 即此值。

### 边界

- 分区总数为 `N`，编号 `0..N-1`；允许 `N=0` 与成员全部离开（空组，所有分区
  无主），随后重新加入可再次铺满。
- 成员上限由 `New` 第二个参数给出，`<=0` 时用 `DefaultMaxMembers`（64）。
- 空批（无变更）合法：执行一次重分配，分配不变、迁移为 0、代数加一。

### 错误类别

| 错误 | 触发条件 |
| --- | --- |
| `ErrEmptyMemberID` | Join/Leave 使用空标识 |
| `ErrDuplicateJoin` | 加入已在组内的成员，或同一批重复加入 |
| `ErrMemberNotFound` | 离开不在组内的成员，或同一批重复离开 |
| `ErrConflictingChange` | 同一成员在一批中既加入又离开 |
| `ErrTooManyMembers` | 批后成员数超过上限 |
| `ErrUnknownChangeType` | 变更类型不是 Join/Leave |
| `ErrInvalidConfig` | 构造参数非法（如负分区数） |

错误判定按固定优先级、与批内顺序无关；错误信息附带具体成员便于定位。

### 并发

- `Apply` 取写锁；`Assignment`/`OwnerOf`/`Owner`/`Members`/`Generation`/
  `Snapshot`/`SelfCheck` 取读锁，可与彼此并发。
- `SelfCheck` 校验：owner 表与各成员持有表一致、每个分区唯一有主、有成员时
  全部分区有主、任意两成员持有数之差不大于一、空组时全部无主。

### 日志

`New` 接收 `*slog.Logger`（传 nil 默认丢弃日志）。每步都会打印：批输入与
批前成员、配额与余数名额排序、每个成员的释放分区、每个无主分区的补齐去向与
缺口、迁移分区列表与计数、最终分配；拒绝批打印原因及“state unchanged”。
