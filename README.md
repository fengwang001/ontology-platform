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

## 变更日志按键压缩（`changelog` 包）

`changelog.Log` 是并发安全的只追加日志（append-only log）压缩合并器，
位于 `changelog/changelog.go`。

### 序号与可见性规则

- 每条写入（`Append`）分配一个从 **1** 开始连续递增的序号；
  `NextSeq()` 返回下一序号，当前最后一条序号为 `NextSeq()-1`。
- 位点读取 `Read(key, position)` 只看序号 `<= position` 的**可见**条目，
  返回其中序号最大（最后写入）的一条；没有任何可见条目时返回
  `ErrNotFound`，而不是其它错误。
- “可见”指该条目未被后续压缩合并掉。压缩不会移动、改写保留条目，
  因此被合并清空的位点之后，任意历史位点仍可寻址：读到的是
  该位点视角下最后存活的同键条目。
- 区间内没有任何写入的键不产生压缩记录；区间外条目原样保留。

### 压缩区间边界

`Compact(left, right)` 处理的是**闭区间** `[left, right]`：

| 非法情形 | 返回错误 |
| --- | --- |
| `left < 1` | `ErrRangeLeftTooSmall` |
| `right > NextSeq()-1`（超过最后序号） | `ErrRangeRightTooLarge` |
| `left > right`（左右倒置） | `ErrRangeInverted` |
| 追加或读取使用空键 | `ErrEmptyKey` |
| `position < 1` 或 `position > NextSeq()-1` | `ErrPositionOutOfRange` |

所有校验在修改状态之前完成；一次失败整体拒绝，日志内容、位点映射与
`LastCompactRecords()` 均保持不变。同一键在一次压缩中只保留区间内
最后一条可见写入，其序号不变，并标记为一条 `CompactRecord`。
`[left, right]` 允许退化为单点（`left == right`）；多次压缩允许区间重叠，
规则对当前仍可见的条目再次适用。

### 并发模型

内部使用 `sync.RWMutex`：`Read` 与 `SelfCheck` 持读锁可彼此并发；
`Append` 与 `Compact` 持写锁与读者互斥，所以读永远不会观察到压缩的
中间状态。并发读取结果与“某一串行历史”的参照逐值相同（见并发测试）。

### 本地验证方法（批量参照）

推荐用一个**只看区间内条目**的独立批量参照核对结果，与实现互不共享代码：

1. 保存全部原始追加（不变）与一个存活标记数组。
2. 参照压缩：扫描区间内仍存活的条目，按键记录序号最大者；
   区间内其余同键条目标记为不存活（区间外不动）。
3. 参照读：在序号 `<= position` 的存活条目中取同键序号最大者，
   没有则视为“不存在”。
4. 对压缩后位点 `1..NextSeq()-1` 的每个键，把 `Read` 的返回值
   （含 `ErrNotFound`）与参照逐值比对；实现内置的 `SelfCheck()`
   会做等价的结构不变量与全量位点核对，可在读并发进行时调用。

测试用例位于 `changelog/changelog_test.go`，覆盖区间内取最后一条、
区间外不动、压缩后任意位点仍可寻址、非法输入整体拒绝、
追加/压缩进行期间的并发读取；`go test -v` 日志会打印每次输入、
读位点、返回值与判定依据。
