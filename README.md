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

## conflict 包：diff3 冲突标记文档解析与逐块解决

`conflict` 包把带 diff3 风格冲突块的文本解析为"正文段与冲突块"序列，支持按块解决（Resolve）、化简（Normalize）并重新渲染（Render）。

### 标记行判定

- 标记行：以**恰好 L 个**连续同种标记字符（`<`、`|`、`=`、`>` 之一）开头，其后紧跟行尾或一个空格；`L >= 7`。
- 连续字符多于或少于 L、或其后紧跟其他字符的行一律视为正文。
- 标签 = 标记行第一个空格之后的全部文本（可含空格），渲染前丢弃尾部空格；`<<<<<<< ` 与 `<<<<<<<` 的标签都是空串。
- 冲突块结构：`<`×L 起始行、ours 行、可选的 `|`×L 行与 base 行、`=`×L 行、theirs 行、`>`×L 结束行；三段行数均可为 0。

### 解析错误优先级

错误带 1 起算的行号（`*LineError`，可 `errors.Is` 判定种类）：

1. `ErrBadL`：L < 7，先于一切。
2. 语法错误按**行号最小者**优先；同行并列时 `ErrNoNewline` 排最后：
   - `ErrStray`：块外出现 `|`、`=`、`>` 标记行（取该行行号）；
   - `ErrNested`：块内再出现起始行（取该行行号）；
   - `ErrOrder`：块内次序错（`|` 重复或在 `=` 后、`=` 重复、`>` 在 `=` 前，取该行行号）；
   - `ErrUnterminated`：到文件末未闭合（取块起始行行号）；
   - `ErrNoNewline`：非空文本末行无 `\n`（取最后一行行号）。
3. `ErrTooLarge`：语法判定通过后，渲染行数（含标记行）超过 MaxLines（1 到 10^6）。

### 块序号语义

`Blocks()` 返回当前未解决块数；`Resolve(i, choice, custom)` 中 `i` 是当前未解决块的 0 起算序号，解决一个块后其后的块序号整体前移 1。`choice` 取 `Ours` / `Theirs` / `Both`（ours 接 theirs，不去重）/ `Base`（无 `|` 行报 `ErrNoBase`，有但为空则取空）/ `Custom`（custom 每行不得含 `\n`，否则 `ErrBadLine`）。拒绝优先级：`ErrNoSuchBlock` > `ErrNoBase` > `ErrBadLine` > `ErrTooLarge`；被拒绝的操作不改任何状态。

### 化简次序

`Normalize()` 对每个块：先把 ours 与 theirs 的最长公共前缀行移出到块前正文，再对剩余行取最长公共后缀移到块后正文（先前缀后后缀）。base 仅当其开头恰与被移出的前缀逐行相同时去掉同样行数；再对剩余 base 当其末尾恰与被移出的后缀相同时同样处理（两步各自独立判断，不满足则保持不动）。ours 与 theirs 都化简为空则该块消失。`Normalize` 幂等。

### 标记长度公式

`Render()` 时设 m 为全部内容行（正文及各块 ours、base、theirs 行，不含标记行）行首连续同种标记字符的最大个数，则整篇统一取 `L' = max(7, m+1)`，随解决/化简重新计算（`MarkerLen()` 返回当前值）。渲染结果用 `Parse(·, L')` 再解析并渲染必得相同文本。

### 本地验证

```bash
go test ./conflict/            # 全部测试（含 2000 组随机文档与朴素实现对照）
go test -race -v ./conflict/   # 竞态检测 + 打印每组输入/输出/判定依据
go vet ./conflict/ && gofmt -l conflict/
```
