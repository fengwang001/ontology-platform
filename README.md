# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 虚拟同步成员视图安装器

`NewInstaller(members)` 用非空且互不相同的成员标识创建安装器。初始视图编号为 `1`，初始状态为稳定态。

### 成员划分与旧多数

调用 `BeginChange(newMembers)` 时：

- 幸存者：同时存在于旧成员集合与 `newMembers` 的成员，负责提交刷新报告。
- 新加入者：存在于 `newMembers`、但不存在于旧成员集合的成员，不提交报告，也没有补交付列表。
- 旧多数按旧视图判断：必须满足 `len(survivors)*2 > len(oldMembers)`，不是按新视图成员数判断。

因此旧视图 4 名成员时幸存 3 名通过、幸存 2 名拒绝；旧视图 2 名成员时幸存 1 名拒绝，必须全部幸存。

### 刷新报告与一致集合

`Report(member, counts)` 只接受幸存者一次提交。`counts` 将发送者映射到该幸存者已连续交付的最大序号，缺省为 `0`；发送者必须属于当前旧视图。

全部幸存者报告后，`Complete()` 对每个旧成员发送者（包括变更后不再存活的成员，缺省值为 `0`）计算：

```text
agreed[sender] = max(各幸存者报告的 counts[sender])
```

这里取最大已交付序号，因此得到的是各发送者已交付前缀的并集；不是最小值，也不是交集。

对每个幸存者，补交付消息按以下确定性顺序展开：

1. 发送者标识按字典序升序。
2. 同一发送者的序号从 `counts[sender]+1` 到 `agreed[sender]` 升序。

新加入者不会出现在 `Plan.CatchUps` 中。成功安装后：

- 新视图编号为旧视图编号加一。
- 新成员按标识字典序写入 `Plan.Members`。
- 安装器进入新视图稳定态。
- 新视图内所有发送者计数从 `0` 开始；下一次变更以新成员为旧成员。
- 返回的 `Plan`、成员切片、补交付切片和 map 均为独立拷贝，不与内部状态别名。

`Abort()` 放弃当前变更，清空幸存者和报告，视图编号与成员集合保持不变，回到稳定态。

### 错误优先级

被拒绝的操作不会修改任何状态。

- `BeginChange` 依次报告第一个错误：已在变更中、新成员为空、存在重复成员、存在空标识、新成员集合与旧成员完全相同、幸存者不满足旧多数。
- `Report` 依次报告第一个错误：不在变更中、提交者不是幸存者、该成员已提交、`counts` 含不属于旧成员的发送者。
- `Complete` 先检查不在变更中；处于变更中但仍有幸存者未报告时返回 `ErrReportPending`。
- `Abort` 不在变更中时返回 `ErrNotInChange`。

所有方法由互斥锁保护，并发调用等价于某个合法串行顺序。多个并发 `Complete` 中只有一个成功，其余返回 `ErrNotInChange`。

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
# 本环境若未把 Go 放入 PATH，可显式使用：
# PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/ontology-gocache

# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 查看随机对照场景的输入、输出与“按消息集合并集”判定日志
go test -run TestRandomScenariosAgainstNaiveUnion -v

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
