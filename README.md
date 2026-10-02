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

## 缩进敏感布局层

布局层位于 `layout` 包，`New(Dm, Bm)` 创建状态机，`Feed([]byte)` 逐物理行返回 `INDENT`、`DEDENT`、`NEWLINE` 事件，`Close()` 在末尾返回剩余 `DEDENT` 与 `ENDMARKER`。

- **缩进栈**：初始为列宽 `(0,0)` 的栈底；每项保存显示列宽 `c`、备用列宽 `a` 与该层最近一个已接受逻辑行的首词 `h`。空格使 `c` 与 `a` 各加一；制表符把 `c` 提升到下一个 8 的倍数，并使 `a` 加一。若已经位于 8 的倍数，制表符仍跳到下一个 8 列。
- **制表符一致性**：同级与缩减必须与目标层的 `(c,a)` 完全一致；缩进要求 `c` 变大时 `a` 也必须严格变大，否则报制表符不一致。缩减时先弹到目标列，不存在该列则报非法缩减，一次可产生多个 `DEDENT`。
- **括号与续行**：引号和注释之外，`(`、`[`、`{` 压栈，`)`、`]`、`}` 必须匹配栈顶；括号栈非空时不判缩进。引号不跨行，反斜杠只跳过字符串内后一个字节；行尾最后一个语法字节为反斜杠时续行，其后存在空白之外的字节则不是续行。
- **字符串与注释**：单、双引号只匹配同种引号；未闭合字符串或字符串最后一个有效字节是反斜杠均为字符串错误。引号外的 `#` 开启注释，括号、冒号、反斜杠在引号或注释内均无语法效果。
- **冒号块期望**：逻辑行完成时，若引号与注释之外的最后有效字符是 `:`，则下一新逻辑行必须缩进。注释、未闭合括号或反斜杠续行都会阻止逻辑行完成；冒号期望不会被空白行或纯注释行清除。
- **悬垂分支**：`elif` 的生效父层首词必须是 `if` 或 `elif`；`else` 必须是 `if`、`elif`、`for`、`while` 或 `except`；`except` 必须是 `try` 或 `except`；`finally` 必须是 `try`、`except` 或 `else`。比较使用整串，`elsewhere` 与 `else_` 都不是 `else`。
- **拒绝次序**：`Feed` 依次检查已关闭、行过长、新逻辑行的缩进错误、缺少缩进、意外缩进、缩进过深、悬垂分支，最后才是行内词法错误。`Close` 依次检查已关闭、输入未完成、缺少缩进块。被拒绝的物理行不会改变任何状态。
- **可复现性**：所有公开方法由互斥保护，可并发重放同一行序列；相同输入始终得到相同事件、错误和快照。内部扫描计数器只累计已接受物理行的字节数，拒绝行不计数，延续行也不会重扫此前物理行。

本地验证布局层：

```bash
GOCACHE=/tmp/go-build go test -race -v ./layout
GOCACHE=/tmp/go-build go test -cover ./...
GOCACHE=/tmp/go-build go vet ./...
```
