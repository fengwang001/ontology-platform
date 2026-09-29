# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 表快照过期与可删除数据文件判定（`snapshot` 包）

`snapshot` 包（见 `snapshot/store.go`）在每次提交时生成新快照、按规则
过期旧快照，并删除不再被任何保留快照引用的数据文件，保证保留快照始终
可读、文件存储无泄漏且结果可复现。

### 快照链

- 每次 `Commit` 基于当前快照生成一个新快照：
  `新文件集 = 当前快照文件集 - removed + added`。
- 快照 ID 从 1 起严格递增；提交时间必须严格递增（`t.After(last)`）。
- 新增文件名在表的整个生命周期内永不复用（即使文件后来已被物理删除）。
- `removed` 必须全部包含在当前快照文件集内。
- 当前快照即链上最后一个现存快照；`GetSnapshot`/`ListSnapshots`/
  `FileExists`/`ListFiles`/`CheckInvariants` 均为读操作，与提交过期
  可并发（`sync.RWMutex`，多读单写）。

### 保留条件（三者取并）

提交后，一个现存快照被保留当且仅当满足以下任一条件：

1. 它是最新的 `retainCount` 个快照之一（`retainCount<=0` 时该条件不保留任何快照）；
2. 它的提交时间严格大于 `expireAfter` 阈值；
3. 它是当前（最新）快照——当前快照永不过期。

其余快照在本次提交中被过期移除。

### 可删除文件判定

- 为每个现存文件维护引用计数，其值恒等于现存快照对该文件的引用次数：
  新快照加入时对其引用的每个文件 `+1`，快照过期移除时对其引用的
  每个文件 `-1`。
- 本次过期后引用计数归零的文件，即“仅被本次过期快照引用、不被任何
  保留快照引用”的文件，从文件存储中物理删除，并按文件名升序返回。
- 不变式（`CheckInvariants` 校验）：每个现存快照引用的文件都在文件
  存储中；文件存储恰为全部现存快照文件集之并；引用计数与实际引用一致。

### 边界与错误类别

所有非法输入都会被**整体拒绝**，返回的错误哨兵互不相同、可用
`errors.Is` 区分；拒绝不改变快照链、引用计数与文件存储（失败不留痕）：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | `retainCount<0`；`added`/`removed` 含空文件名或内部重复；`added` 与 `removed` 相交 |
| `ErrTimeNotIncreasing` | 提交时间不严格晚于最新现存快照（相等也算） |
| `ErrFileNameReused` | `added` 中的文件名在表生命周期内已出现过 |
| `ErrFileNotInCurrent` | `removed` 中的文件名不在当前快照文件集内 |
| `ErrSnapshotNotFound` | `GetSnapshot` 查询的快照已过期或从未创建（非状态变更错误） |

其他边界：首次提交时当前文件集为空（只能 `added`）；`retainCount`
大于现存快照数时等价于全部保留；时间比较使用纳秒精度的 `time.Time`。

### 判定日志

默认向 stderr 打印每步输入（提交时间、added/removed、retainCount、
阈值）、每个快照的保留/过期依据（最近 N 个 / 时间戳严格大于阈值 /
当前快照 / 两者同时满足）、被删文件及最终结果；可用
`snapshot.SetLogOutput(w)` 重定向或静默。

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

# 快照过期与文件判定（含竞态检测、朴素参照对照与覆盖率）
go test -race -v ./snapshot
go test -race -count=10 ./snapshot
go test -cover ./snapshot

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
