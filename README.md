# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## LWW 元素集合（`lwwset` 包）

最后写入者胜出（Last-Writer-Wins）的元素集合 CRDT，支持多副本独立增删、
两两合并同步，并在并列时间戳时稳定偏向删除，使各副本收敛到一致且可复现的结果。

### 数据模型

- 每个副本对每个元素保存两条记录：最新添加时间 `Add` 与最新删除时间 `Remove`（0 表示不存在）。
- 元素在集合中当且仅当 `Add > 0` 且 `Remove < Add`；`Remove == Add` 视为删除（并列偏删除）。

### 操作规则

- **添加** `Add(elem, ts)`：将 `Add` 更新为 `max(Add, ts)`，旧时间戳到达不会把记录改小。
- **删除** `Remove(elem, ts)`：将 `Remove` 更新为 `max(Remove, ts)`；删除不存在的元素会留下墓碑记录。
- **整份合并** `Merge(src)`：只修改目标副本，对每个元素的两条记录分别取两侧较大值；
  合并满足交换律、结合律与幂等性。
- **增量合并** `MergeIncremental(src)`：每个副本维护单调递增的变更序号与变更日志，
  目标副本按源副本编号记录合并位置，只应用位置之后的变更，结果与整份合并一致。
- **并发安全**：所有方法（增删、合并、判存在、记录查询、自检）均可并发调用；
  合并先在源副本读锁下拷贝快照再释放，然后在目标副本写锁下应用，
  任意时刻最多持有一把锁，互逆方向合并同时进行不会死锁。

### 边界与错误类别

所有非法输入整体拒绝，且拒绝后不改变记录、变更序号或合并位置（失败不留痕）。
错误为可 `errors.Is` 区分的哨兵错误，互不相同：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidReplicaID` | 副本编号为空 |
| `ErrInvalidLimit` | 构造时元素上限非正 |
| `ErrEmptyElement` | 元素为空串 |
| `ErrNonPositiveTimestamp` | 时间戳非正 |
| `ErrTooManyElements` | 记录元素数超过上限（含合并前预检，整体拒绝） |
| `ErrNilReplica` | 合并的源副本为 nil |
| `ErrSelfMerge` | 副本与自身合并 |

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
go test ./lwwset
go test -run TestTiePrefersRemove ./lwwset

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`lwwset` 测试覆盖：时间戳相等并列偏删除、乱序到达不改小记录、增量合并与整份合并等价、
合并交换/结合/幂等律、非法输入拒绝后状态不变、多执行体并发双向合并不死锁并收敛，
并与朴素参照模型逐副本比对；日志打印每步输入、记录快照与判定依据（`go test -v` 查看）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
