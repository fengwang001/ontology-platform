# initsession

包级变量初始化次序求解会话：按源码次序登记变量初始化单元与函数声明，求解唯一确定的初始化次序，无法完成时给出可区分的错误。

## 快速上手

```go
s, _ := initsession.New("println")                 // 预声明标识符
s.AddVariableUnit([]string{"a"}, nil)              // 单元 0：a 无依赖
s.AddVariableUnit([]string{"b", "c"}, []string{"a", "f"})
s.AddFunction("f", []string{"a"})

res, stats, err := s.Solve()
// res.Order          -> 初始化的单元下标次序
// res.Dependencies[i] -> 单元 i 的传递依赖变量（名字升序，不含自身变量与 "_"）
// stats               -> 可验证的工作量统计
```

## API

- `New(predeclared ...string) (*Session, error)`：预声明标识符视为已就绪，不得被重名。
- `(*Session).AddVariableUnit(variables, refs []string) (int, error)`：登记变量单元，返回源码次序下标；非法参数优先于重名；被拒登记不占位置、不改状态。
- `(*Session).AddFunction(name string, refs []string) error`：登记函数，函数名不得为 `_`，与变量共用命名空间。
- `(*Session).Solve() (*Result, Stats, error)`：不修改会话，可并发调用；错误为 `*initsession.Error`，其 `Kind` 可取
  `KindInvalidArgument` / `KindRedeclared` / `KindUndeclared` / `KindInitCycle`。

## 错误语义

- 未声明引用优先于初始化环：覆盖全部已登记声明（含未被使用的函数），报告登记次序最早的声明及其中字典序最小的未声明标识符。
- 初始化环报告全部无法完成的非空白变量，按源码次序排列。
- 没有任何声明时求解成功，次序为空。

## 设计与验证

- 设计说明（关键取舍、被放弃方案、复杂度论证）：`docs/design.md`。
- 朴素逐轮扫描参照模型与 400 组随机差分（含被拒绝登记）：`naive_test.go`、`diff_test.go`。
- 并发登记/求解交错：`concurrent_test.go`（配合 `go test -race`）。
- 开销统计断言与基准：`stats_test.go`、`bench_test.go`。
- 每次输入/输出/判定依据日志：`go test -v ./initsession`，并落盘到 `testdata/logs/initsession-trace.log`。

```bash
go test -race ./...
go test -bench . -benchmem ./initsession
```
