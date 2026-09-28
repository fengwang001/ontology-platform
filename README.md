# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 变更日志折叠组件（`changelog` 包）

`changelog.Folder` 把按批到达的写入流规整为撤回式变更日志（retraction-style changelog）。
每个键要么不存在，要么持有一个值；`PUT` 使其存在，`DELETE` 使其不存在。

### 折叠规则

一批写入只比较每个被触及键在**批开始前**与**批结束后**的净状态，批内的中间过程不出现在日志中：

| 批前状态 | 批后状态 | 输出条目 |
| --- | --- | --- |
| 不存在 | 不存在（如删除不存在的键，或批内先建后删） | 无 |
| 不存在 | 值 `v` | `UPSERT(k, v)` |
| 值 `old` | 不存在 | `RETRACT(k, old)` |
| 值 `old` | 值 `new`，且 `old == new` | 无（值相等不输出） |
| 值 `old` | 值 `new`，且 `old != new` | `RETRACT(k, old)` 后紧接 `UPSERT(k, new)` |

下游只需按日志顺序应用——`RETRACT` 删除对应旧值，`UPSERT` 写入新值——即可始终重建出正确的当前表。

### 输出顺序

- 同一批内多个键按其在批中**首次出现**的顺序排列；
- 同一键发生值变化时，撤回条目一定在写入条目之前；
- 批与批之间按 `Apply` 串行提交的先后追加。

### 拒绝规则（可区分原因）

出现以下任一情况时整批被拒绝，当前表与已产生日志**均不改变**，组件之后仍可继续使用：

- `ErrEmptyKey`：批中存在空键；
- `ErrInvalidOp`：批中存在既非 `OpPut` 也非 `OpDelete` 的操作类型；
- `ErrTooManyLiveKeys`：批结束后存活键数超过 `New(maxLive)` 设定的上限（只检查批后净状态，批内中间态不检查；`maxLive <= 0` 表示不限制）。

### 并发与确定性

- `Apply` 以整批为粒度加锁并原子提交，可被多 goroutine 并发调用；
- `Snapshot` / `Table` / `Log` 在同一把锁下返回深拷贝，并发读者看到的表与日志逐字段一致；
- 同一输入序列在全新 `Folder` 上反复计算，得到完全相同的日志与错误。

### 用法示例

```go
f := changelog.New(100) // 最多 100 个存活键，0 表示不限
entries, err := f.Apply([]changelog.Mutation{
    {Key: "a", Op: changelog.OpPut, Value: "1"},
    {Key: "a", Op: changelog.OpPut, Value: "2"}, // 批内覆盖，只看净效果
})
// entries = [UPSERT(a, "2")]
table, log := f.Snapshot() // 彼此一致的深拷贝
```

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

# 带竞态检测与详细输出（-v 会打印每批的输入、输出条目与判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology
go test -race -run TestApply ./changelog

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 本地验证清单

```bash
gofmt -l .            # 无输出即格式正确
go vet ./...          # 无输出即静态检查通过
go test -race ./...   # 全部通过且无 data race
```

`changelog` 包测试覆盖：批内覆盖（含先建后删、先删后建）、删除不存在的键、
值相等不输出、多键首次出现顺序、空键 / 非法操作 / 存活键超限三类拒绝及拒绝后可继续使用、
日志顺序重放重建当前表、重复输入的确定性，以及并发写入下的原子性与快照一致性。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
