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

## hpack 包：HPACK 式头部压缩（`./hpack`）

带动态表尺寸协商的头部压缩编码器（`Encoder`）与解码器（`Decoder`），两端动态表在逐条插入、驱逐、尺寸更新与出错回滚下逐项一致。

### 表与索引

- 静态表固定 4 项：`1=(:method,GET)`、`2=(:method,POST)`、`3=(:path,/)`、`4=(:scheme,https)`。
- 动态表条目大小公式：`len(name) + len(value) + 32`（字节数）；`used` 为所有条目大小之和。
- 动态索引从 5 起，最新插入者为 5，越旧索引越大。
- 构造参数 `1 ≤ C0 ≤ L0 ≤ 65536`，否则 `ErrParam`。
- 名字须为 1–256 字节，仅含小写字母、数字与 `- : _ .`；值为 0–1024 字节。

### 插入、驱逐与清空

- `Insert(name, value)`：若条目大小大于当前容量 `C`，清空整张表且不加入（不是错误）。
- 否则从最旧项起逐条驱逐，直到 `used + 大小 ≤ C`，再作为最新项加入。
- `Resize(C')`（`1 ≤ C' ≤ 当前上限`）与 `SetLimit(L')`（`1 ≤ L' ≤ 65536`，`L' < C` 时立即令 `C = L'`）都会按同一规则缩表。

### 编码规则与索引解析时机

- 逐项决定，前一项的插入对后一项可见；索引一律在本项 `Insert` 之前解析（解码方同样在本项 `Insert` 之前解析）。
- 敏感名（构造时给定，精确匹配）输出 `LiteralNever`，不插入，但名字索引照常解析。
- 否则先找 `(name, value)` 全匹配：静态表优先，其次动态表取最新者，命中输出 `Indexed(i)`。
- 无全匹配则输出 `LiteralIndexed` 并 `Insert`；`nameIdx` 取仅名字匹配（静态最小索引优先，其次动态最新者），无则为 0 并携带字面名字。

### 尺寸协商（updates 生成规则）

记 `base` 为上一次 `Encode` 结束时的容量（构造后为 `C0`），`mn` 为自 `base` 起容量出现过的最小值。`Encode` 开头生成：

- `mn < base` 且 `mn < 当前C`：`updates = [mn, 当前C]`（先降后升）；
- 否则 `当前C ≠ base`：`updates = [当前C]`；
- 否则为空。`Encode` 成功后 `base = 当前C` 并重置 `mn`。

### 错误类别与回滚语义

- `ErrParam`：构造参数越界、`Resize`/`SetLimit` 越界、`Encode` 遇到非法头部；被拒绝的操作不改变任何状态（不插入、不改 `base`/`mn`）。
- `ErrUpdate`：解码方先校验全部 updates（`1 ≤ v ≤ Ld`），任一越界则整体拒绝。
- `ErrSyntax`：字面指令语法错误（`nameIdx>0` 却带字面名字、`nameIdx=0` 名字非法、值非法）；同一指令上先于 `ErrIndex` 判定。
- `ErrIndex`：`Indexed(0)`、索引超出表长、`nameIdx` 无法解析。
- 解码任一错误都使整个块回滚：动态表、容量与输出恢复到调用前，不输出任何头部。

### 并发与确定性

`Encoder` 与 `Decoder` 内部以互斥锁串行化所有调用，可安全并发使用，结果等价于某个串行顺序；相同输入序列产生完全相同的块与表。

### 本地验证

```bash
# 全部用例（含随机一致性对照与并发用例），打印输入/输出/判定依据
go test -race -v ./hpack

# 指定用例，如驱逐示例
go test -v -run TestInsertAndEviction ./hpack
```
