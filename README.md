# ontology-platform

Go 模块当前提供 `hygiene` 包：一个确定性、可并发调用的卫生宏展开会话。

## 卫生宏语义

- 宏模板引入的 lam 绑定只与模板中同一次展开的同名引入引用相连；实参或用户表达式中的同名自由标识符不会被捕获。
- 模板中未被同次展开 lam 绑定的符号是全局引用；它不会被用户 lam 或外层展开的局部绑定捕获，也不会因用户局部同名而失去作为宏名的资格。
- 宏实参保持用户来源。若实参符号进入模板 lam 参数表，它就是用户绑定；实参体内对该名字的用户引用会解析到这个绑定。非符号实参进入参数表时，展开结果为形式非法。
- `(quote 项)` 原样输出；其中的符号不替换、不展开宏，也不参与重命名。
- 模板内嵌套 lam 重名时，引用按词法最内层绑定解析；同一表达式中多次展开同一个宏会得到彼此独立的新绑定。

## 绑定编号

- 成功的 `Expand` 是一个原子步骤。
- 输出中的每个 lam 参数按先序、同一参数表从左到右命名为 `基名#k`。
- `k` 从全局绑定计数 `N+1` 开始逐个递增；成功后 `N` 增加本次输出的 lam 参数总数。
- `X` 记录成功展开中的宏展开总次数；被拒绝的操作不改变 `N`、`X` 或宏表。
- 相同操作序列在新会话中重放，得到完全相同的输出文本、`N` 与 `X`；输出中任意两个 lam 参数最终名字互不相同。

## 拒绝次序

`DefMacro` 只返回以下第一类错误：

1. 参数非法：名字或参数名不是合法符号、使用 `lam`/`quote`、参数重复、参数个数越界、模板非法或模板超过 200 个节点。
2. 重名：宏已经登记。
3. 数量超限：宏表已有 100 个宏。

`Expand` 按先序外层到内层、同层从左到右处理，并以遇到的第一个违规为准：

1. 形式非法：空列表、非法核心形式、lam 参数为空/非符号/重名/保留字等。
2. 宏实参个数不符。
3. 深度超限：宏展开深度大于 20，深度 20 允许、21 拒绝。
4. 规模超限：输出节点数大于 2000，2000 允许、2001 拒绝。

## 用法

```go
session := hygiene.NewSession()

template, _ := hygiene.Parse("((lam (t) (if t t b)) a)")
if err := session.DefMacro("my-or", []string{"a", "b"}, template); err != nil {
    panic(err)
}

input, _ := hygiene.Parse("(my-or (f) t)")
output, err := session.Expand(input)
text, _ := hygiene.Render(output)
// text == "((lam (t#1) (if t#1 t#1 t)) (f))"
```

公开类型与错误：

- `NewSession() *Session`
- `(*Session).DefMacro(name string, params []string, template *Term) error`
- `(*Session).Expand(input *Term) (*Term, error)`
- `(*Session).BindingCount() int`
- `(*Session).ExpansionCount() int`
- `Parse(text string) (*Term, error)`：只接受任务规定的输入符号。
- `Render(term *Term) (string, error)`：输出规范文本，接受 `基名#编号`。
- `DefError.Kind`：`DefInvalid`、`DefDuplicate`、`DefLimit`。
- `ExpandError.Kind`：`FormInvalid`、`ArityMismatch`、`DepthExceeded`、`SizeExceeded`。

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
GOCACHE=/tmp/go-cache go test ./...

# 定点、边界、并发和 2000 组随机朴素模型对照
GOCACHE=/tmp/go-cache go test -v ./hygiene

# 带竞态检测
GOCACHE=/tmp/go-cache go test -race ./...

# 覆盖率
GOCACHE=/tmp/go-cache go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

随机测试使用独立的朴素标签模拟器，并使用独立解析器复核输出中的每个 `#k` 引用都能词法绑定到对应 lam；通过 `go test -v ./hygiene` 可查看每组输入、输出与判定依据。

## 代码检查

```bash
/usr/local/go/bin/gofmt -l .
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
```
