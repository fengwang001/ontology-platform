# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 卫生宏展开会话

`hygiene` 包实现可并发调用的卫生宏会话：

```go
s := hygiene.NewSession()
err := s.DefMacro("add2", []string{"x"}, "(+ x 2)")
output, err := s.Expand("(add2 5)") // (+ 5 2)
n := s.N()                        // 输出中的 lam 参数总数
x := s.X()                        // 成功展开次数
```

### 卫生规则

- 宏模板 lam 参数表中的符号是本次展开新引入的绑定；体中的同名符号解析到该绑定，最终重命名为 `基名#k`，不会捕获用户同名标识符。
- 模板中其他非参数符号是宏引入的全局引用；它们不会被用户 lam 或外层宏展开引入的绑定捕获，同名宏也不会被用户局部绑定遮蔽。
- 参数位置原位替换为用户实参，实参内符号仍按用户位置解析；当参数出现在 lam 参数表时，该 binder 属于用户实参，体中解析到它的用户引用会被一起重命名。
- `(quote 项)` 整体原样输出；quote 内的宏不展开、绑定不重命名，但模板参数仍按规范在原位替换为实参。
- 嵌套模板 lam 同名时按静态模板词法作用域取最内层；同一次模板内同一 binder 及其引用共享内部身份，不同展开实例互不相同。

### 编号与确定性

- 每个成功 `Expand` 按先序、同表从左到右为输出中的 lam 参数分配全局编号。
- 最终名字是 `原基名#k`；全局引用和未绑定自由符号保持原名。
- 成功后 `N` 增加本次输出的 lam 参数总数，`X` 增加本次宏展开次数。
- 整个 `Expand` 在互斥区内原子提交；被拒绝时宏表、`N`、`X` 都不变，因此同序列重放得到相同输出和计数。

### 拒绝原因与次序

- `DefMacro` 返回：`ErrInvalidDefinition`、`ErrDuplicateMacro`、`ErrMacroTableFull`，按“参数/模板非法 → 重名 → 宏表数量超限”的第一个原因报告。
- `Expand` 返回：`ErrInvalidForm`、`ErrArgumentCount`、`ErrDepthLimit`、`ErrSizeLimit`，按先序外到内、同层左到右遇到的第一个违规报告。
- 宏实参个数先于深度检查；深度 20 允许，第 21 层拒绝；输出节点数 2000 允许，达到 2001 拒绝。

### 本地验证

```bash
go test ./hygiene -v
go test -race ./hygiene
go test ./hygiene -run TestRandomNaiveComparison -v
```

随机测试使用固定种子运行 2000 组程序，以独立的用户/引入绑定/全局引用标签朴素模型为 oracle，并通过另一个独立解析器检查最终文本中每个重命名引用的词法绑定；`-v` 日志包含输入、输出、拒绝状态、`N`、`X` 和判定依据。

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
