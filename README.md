# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## conflict 包：diff3 冲突标记文档的解析与逐块解决

`conflict` 包把带 diff3 风格冲突块的文本解析成“正文段与冲突块”序列，
支持按块解决（Resolve）、化简（Normalize）与重新渲染（Render）。

### 标记行判定

- 文本由若干以 `\n` 结尾的行组成；空文本合法，非空而末行缺 `\n` 报
  `ErrNoNewline`（行号取最后一行）。
- 标记行：以**恰好** L 个连续同种标记字符（`<`、`|`、`=`、`>` 之一）
  开头，其后紧跟行尾或一个空格。连续字符多于或少于 L、或其后紧跟其他
  字符的行一律是正文。
- 标签 = 标记行第一个空格之后的全部文本（可含空格，尾部空格在解析时
  丢弃）；`<<<<<<< ` 与 `<<<<<<<` 的标签都是空串。
- 冲突块结构：`<`×L 起始行、ours 行、可选的 `|`×L 行与 base 行、
  `=`×L 行、theirs 行、`>`×L 结束行；三段行数均可为 0。

### 解析错误优先级

错误带 1 起算的行号；多种错误同时成立时取行号最小者，同行并列时
`ErrNoNewline` 排最后。`L<7` 为 `ErrBadL`，先于一切。其余：

- `ErrStray`：块外出现 `|`、`=`、`>` 标记行；
- `ErrNested`：块内再出现 `<` 起始行；
- `ErrOrder`：块内次序错（`|` 重复或出现在 `=` 之后、`=` 重复、
  `>` 出现在 `=` 之前）；
- `ErrUnterminated`：到文件末仍未闭合，行号取该块起始行；
- `ErrNoNewline`：末行缺换行，行号取最后一行。

语法判定先于大小判定：语法通过后才检查渲染行数是否超过 MaxLines
（`ErrTooLarge`）。

### 块序号语义

`Blocks()` 返回当前未解决块数；`Resolve(i, choice, custom)` 的 `i` 是
当前未解决块的 0 起算序号，解决一个块后其后的块序号整体前移 1。
choice 取 `Ours`、`Theirs`、`Both`（ours 接 theirs，不去重）、`Base`
（无 `|` 行报 `ErrNoBase`，base 为空则取空）、`Custom`（每行不得含
`\n`，否则 `ErrBadLine`）。解决后该块被所选行替换并与相邻正文合并。
Resolve 的拒绝按 `ErrNoSuchBlock > ErrNoBase > ErrBadLine >
ErrTooLarge` 只报第一个，被拒绝的操作不改任何状态。

### 化简次序（Normalize）

对每个块：先把 ours 与 theirs 的最长公共前缀行移出到块前正文，再对
剩余行取最长公共后缀移到块后正文（先前缀后后缀）。base 仅当其开头恰
与被移出的前缀行逐行相同时才去掉同样行数，再对剩余 base 当其末尾恰
与被移出的后缀行相同时同样处理，否则保持不动。ours 与 theirs 都化简
为空则该块消失。Normalize 幂等。

### 标记长度公式（Render）

设 m 为全部内容行（正文及各块 ours、base、theirs 行，不含标记行）中
“行首连续同种标记字符”的最大个数，则整篇文档统一的标记长度
`L' = max(7, m+1)`，随解决而重新计算。标记行为标记串，标签非空时再
接一个空格与标签；`|` 行仅在块有 base 时输出。输出满足
`Parse(Render(), L').Render() == Render()`。

### 构造参数与并发

`Parse(text, L, maxLines)`：`L >= 7`；`maxLines`（1 到 10^6，越界会被
钳到范围内）限制渲染行数（含标记行）。所有方法可并发调用，结果等价
于某个串行顺序；相同操作序列重放得到完全相同的文本与错误。

### 本地验证

```bash
go test ./conflict/          # 单元测试 + 2000 组随机对照测试
go test -race ./conflict/    # 竞态检测
go test -v -run TestDifferentialAgainstNaive ./conflict/
# 对照测试与 naive_test.go 中按规则逐步写成的朴素实现逐步比对，
# 日志打印每组的输入、输出与判定依据
```

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
