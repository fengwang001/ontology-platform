# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 写入流折叠组件（`changelog` 包）

`changelog.Coordinator` 把按批到达的写入流（put / delete）规整为**撤回式变更日志**
（retract-then-upsert changelog）。下游按日志顺序应用（retract 删除键、upsert 写入键），
始终得到与组件当前表逐字段一致的结果。

### 折叠规则

每个键要么不存在、要么有一个值。对每一批写入，只看每个被触及键在**批开始前**与
**批结束后**的净状态，批内的中间过程一律折叠掉：

| 批前状态 | 批后状态 | 输出条目 |
| --- | --- | --- |
| 不存在 | 存在 `v` | `upsert(k, v)` |
| 存在 `old` | 不存在 | `retract(k, old)` |
| 存在 `old` | 存在 `v`，且 `old != v` | `retract(k, old)` 后紧跟 `upsert(k, v)` |
| 状态相同（含值相等） | — | 不输出 |

因此“批内多次覆盖”“批内 put 后又 delete（净效果为不存在）”“删除一个本来就不存在的键”
“写入与当前值相同的值”都不会产生多余条目。

### 输出顺序

- 同一批内涉及多个键时，按各键在批中**首次出现的位置**排序（与批后 map 的随机遍历无关）。
- 同一个键的值变更输出两条，顺序固定为**先撤回旧值，再写入新值**。
- 条目带全局连续序号 `Seq`（从 0 开始），序号顺序即日志顺序；同一输入序列反复计算，
  输出完全一致。

### 拒绝规则（可区分原因）

整批原子校验，任何一条非法都会拒绝整批，**当前表与已产生的日志不变**，之后仍可继续使用：

- `empty_key`：出现空字符串键（错误携带批内下标）。
- `invalid_op`：操作类型既非 `put` 也非 `delete`（携带批内下标、键与非法操作值）。
- `too_many_live_keys`：批结束后存活键数超过 `New(maxLiveKeys)` 设定的上限
  （`<=0` 表示不限；携带存活数与上限）。上限按**批结束后**的存活数判定，
  批内瞬时超限但批结束前回落不算超限。

错误以 `*BatchError` 返回，可通过 `Reason` 字段区分以上原因。

### 并发语义

- `Apply` 可被并发调用，每批整体原子（单一互斥保护，校验与提交之间不释放锁）。
- `Snapshot` 返回表与日志的深拷贝，两者来自同一原子时刻；并发只读永远看到
  “重放日志 == 当前表”、日志序号连续的状态。

### 最小用法

```go
import "ontology/changelog"

c := changelog.New(0) // 0 表示不限制存活键数
entries, err := c.Apply([]changelog.Write{
    {Key: "a", Op: changelog.OpPut, Value: "1"},
    {Key: "a", Op: changelog.OpPut, Value: "2"}, // 批内覆盖，被折叠
})
// entries: [upsert(a=2)]
table, log := c.Snapshot()
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

## 测试与本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（可看到每个用例打印的输入、输出条目与判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./changelog
go test -run TestInBatchOverwrite ./changelog

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

> 若 `go` 不在 PATH 中（如本机安装在 `/usr/local/go/bin`），先执行
> `export PATH=$PATH:/usr/local/go/bin`。

测试覆盖：批内覆盖 / 自抵消、删除不存在的键、值相等不输出、多键首次出现顺序、
三类非法输入的拒绝与原子性、拒绝后继续使用、日志顺序重放与当前表一致、
重复计算确定性以及高并发读写一致性（`-race`）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
