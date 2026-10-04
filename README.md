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

## ot 文档（服务端 OT 文本）

`otdoc` 包实现带修订历史与去重序号的并发安全服务端操作变换（OT）。

构造：`New(maxLen)`，文档初始为空串，`rev=0`、`floor=0`，`maxLen` 取
`1..1_000_000`（按 rune 计）。

### 操作与规范形式

操作是组件序列 `Retain(n)`、`Insert(s)`、`Delete(n)`：

- `n >= 1`；`Insert.Text` 非空且为合法 UTF-8。
- **基长** = 所有 `Retain` 与 `Delete` 的 `n` 之和，必须**等于**其基修订
  的文档长度（显式覆盖整篇文档，含末尾 `Retain`）。
- 规范化：相邻同类组件合并；相邻的 `Insert` 与 `Delete` 一律排成
  `Insert` 在前。输入先规范化再使用，`History` 与所有返回操作均为规范形式。

### 变换规则（s 为入库操作，c 为新操作）

实现把客户端操作展开为“逐基修订字符位置的保留/删除意图 + 按边界锚定的
插入”（见 `xform_pos.go`），对每个入库操作逐字符位置映射：

- 入库 `Insert`：客户端输出等长 `Retain` 跳过（同位置两个 `Insert`，入库者
  在左）；
- 客户端 `Insert`：原样保留在其锚点；
- `Retain/Retain` → 保留；`Retain/Delete` → 删除；
- `Delete/Retain` → 客户端的保留消失；`Delete/Delete` → 重叠只删一次；
- 落在入库删除区间内的客户端 `Insert` 折叠保留在删除点，区间内多个插入按
  到达顺序排列。
- 对单个入库操作的一次变换，组件处理步数用非导出计数器断言不超过
  `len(s)+len(c)`。

`Submit` 依次对 `history[baseRev..rev)` 逐跳映射；客户端意图在多跳之间以
位置文档形式保留（插入锚点不受规范化 `Insert/Delete` 重排影响），最后一跳
才转回规范 `Op`。变换后若全为 `Retain` 则为空操作：不产生新修订、`rev`
不变，但该 site 的 `seq` 照常推进、结果照常记录。

### 序号与去重

每个 site 记录已受理最大 `seq`（初始 0）：

- `seq == last`：重复提交，原样返回当时记录的结果（`Applied`、`rev`、
  变换后操作），不改任何状态；
- `seq < last`：`ErrStaleSeq`；`seq > last+1`（或新 site 非 1）：
  `ErrSeqGap`。每个 site 只保留最近一次结果，`Compact` 后仍可重复返回。

### 错误固定优先级

1. 参数非法（空 site、`seq<1`、`baseRev<0`、非法/空组件）→ `ErrInvalid`
2. 序号类：重复在此返回，否则 `ErrStaleSeq`、`ErrSeqGap`
3. `baseRev > rev` → `ErrFuture`
4. `baseRev < floor` → `ErrTooOld`
5. 基长与基修订文档长度不等 → `ErrLength`
6. 变换后文档长度超过 `MaxLen`（按变换后操作计，不按原操作）→
   `ErrTooLarge`

被拒绝的提交不改变 `rev`、历史、文档与该 site 的序号。

### Compact / History / 并发

- `Compact(newFloor)`：仅当 `floor <= newFloor <= rev` 成功（否则
  `ErrBadFloor`），丢弃低于 `newFloor` 的历史；`baseRev == newFloor` 仍合法。
- `History(from,to)`：`from < floor` → `ErrTooOld`；`from > to` 或
  `to > rev` → `ErrBadRange`；否则返回变换 `from -> to` 的规范操作。
- 所有方法以互斥锁串行化，`Text/Rev` 等读不会观察到半完成提交，结果等价于
  某种全序；相同提交序列重放结果完全一致。

### 本地验证

```bash
go test ./otdoc                                   # 全部单元/差分测试
go test ./otdoc -run TestRandomDifferential -v    # 2000 组随机多 site 对照
go test -race ./...                               # 竞态检测
gofmt -l . && go vet ./...
```

随机差分测试生成 2000 组随机多 site 提交序列（含乱序/重复序号、旧基修订、
压缩、容量边界），与一个独立的“把基修订每个字符位置逐一映射到当前修订”的
朴素模型逐提交比对文档、修订号、变换后操作、拒绝类别，并重放全部历史校验；
每个输入、输出与判定依据都打印到测试日志。
