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

## otdoc：服务端文本 OT 文档

`otdoc` 包实现带修订历史与去重序号的服务端文本操作变换（OT）文档。

### 模型

- `New(MaxLen)` 构造空文档：`rev=0`、`floor=0`，`MaxLen ∈ [1, 10^6]` 为文档字符数上限（按 rune 计）。
- 操作是组件序列 `Retain(n)` / `Insert(s)` / `Delete(n)`（`n≥1`，`s` 非空且为合法 UTF-8）。
  基长 = 全部 `Retain` 与 `Delete` 的 `n` 之和，必须等于其基修订的文档长度（显式覆盖整篇文档，含末尾 `Retain`）。
- `history[i]` 是把修订 `i` 变成修订 `i+1` 的操作；`Submit` 返回 `(Rev, Applied, 变换后的操作)`。

### 规范形式

相邻同类组件合并；相邻的 `Insert` 与 `Delete` 一律 `Insert` 在前。输入先规范化再使用，
`History` 与全部返回值都是规范形式。注意：只有提交输入与最终结果做规范化，
逐步变换的中间结果保持双指针原始输出，以保证与"基修订每个字符位置逐一映射到当前修订"的语义一致。

### 变换规则

`Submit(site, seq, baseRev, op)` 把 `op` 依次对 `history[baseRev..rev)` 变换（已入库操作 `s` 在先、新操作 `c` 在后），
逐组件双指针：

1. `s` 当前组件是 `Insert` 时先处理：`c` 输出等长 `Retain` 跳过它（同一位置两个 `Insert`，入库者在左）；
2. 其次 `c` 当前组件是 `Insert` 时原样输出（落在 `s` 删除区间内的 `c` 的 `Insert` 保留在删除点）；
3. 其余按较短长度同步消耗：`c` 的 `Retain`/`Delete` 遇 `s` 的 `Retain` 保留、遇 `s` 的 `Delete` 消失（重叠只删一次）。

对单个入库操作的一次变换，组件处理步数不超过 `len(c)+len(s)`（测试中用非导出计数器断言）。
变换后若全是 `Retain` 则为空操作：不产生新修订、`rev` 不变、`Applied=false`，但该 site 的 `seq` 照常推进。

### 序号与错误优先级

每个 site 记已受理最大 `seq`（初值 0）：`seq=last+1` 受理；`seq=last` 为重复提交，原样返回当时记录的
结果（含 `Applied`、`Rev` 与变换后操作）且不改任何状态；`seq<last` 报 `ErrStaleSeq`；`seq>last+1` 报 `ErrSeqGap`。
每个 site 只留最近一次结果。拒绝按固定优先级只报第一个：

1. `ErrInvalid`：参数非法（site 空、`seq<1`、`baseRev<0`、组件非法或列表为空）；
2. 序号类：重复提交在此直接返回记录结果（其后各项不再校验），否则 `ErrStaleSeq` / `ErrSeqGap`；
3. `ErrFuture`：`baseRev > rev`；
4. `ErrTooOld`：`baseRev < floor`；
5. `ErrLength`：基长与 `baseRev` 修订的文档长度不等；
6. `ErrTooLarge`：变换后的文档长度超过 `MaxLen`（按变换后的操作计，不按原操作计）。

被拒绝的提交不改 `rev`、`history`、`seq` 与文档。

### Compact 与 History

`Compact(newFloor)` 仅当 `floor ≤ newFloor ≤ rev` 成功（否则 `ErrBadFloor`），丢弃修订号小于
`newFloor` 的历史；`baseRev = floor` 仍合法，各 site 最近结果在 Compact 后仍可作重复返回。
`History(from, to)` 返回把修订 `[from, to)` 逐步推进的入库操作：`from < floor` 报 `ErrTooOld`，
`from > to` 或 `to > rev` 报 `ErrBadRange`（`from > to` 优先）。

### 并发与重放

所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；`Doc`/`Rev` 不会观察到半完成的提交。
相同提交序列重放得到完全相同的 `history`、文档与每次返回（`TestReplayDeterministic`）。

### 本地验证

```bash
# 全部单测（含规范示例、边界、MaxLen、floor、步数断言）
go test ./otdoc

# 2000 组随机多 site 提交序列 vs 朴素位置映射模拟，打印输入/输出/判定依据
go test ./otdoc -run TestRandomAgainstNaiveOracle -v

# 竞态检测（含并发提交测试）
go test -race ./otdoc
```
