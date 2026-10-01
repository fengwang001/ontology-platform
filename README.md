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

## upvalue 包：闭包捕获变量的开闭管理器

`upvalue` 包在值栈上管理闭包捕获变量（upvalue）的开放与关闭，
所有方法可并发调用，结果等价于某个串行顺序。

### 开放与关闭状态

- `Push(v)` 把值压入值栈，返回从 0 起的槽号，栈顶加一。
- `Capture(slot)` 捕获槽 `slot`：同一槽上已有开放的捕获变量时返回
  同一句柄（共享表保证同一槽至多一个共享的开放变量），否则新建句柄
  （句柄从 1 起递增、不复用）；每次捕获使该变量持有数加一。
- 开放变量的 `Read`/`Write` 直接作用于栈槽，栈槽的
  `ReadStack`/`WriteStack` 直接读写对其同样可见。
- `Close(level)` 对槽号不小于 `level` 的全部开放变量，把当前槽值复制进
  自身存储并转为关闭，随后栈顶设为 `level`；`level` 恰等于栈顶是合法的
  空操作。关闭后读写只作用于自身存储，不再被栈修改影响；同一槽之后重新
  压栈并捕获得到新的句柄。

### 持有数与摘除规则

- `Release(h)` 使持有数减一；减到 0 时若变量仍开放，则从共享表中摘除，
  之后同槽再捕获得到新句柄。
- 已摘除或已释放完的句柄再读写均无效，报 `ErrHandleReleased`；
  从未存在的句柄报 `ErrHandleNotFound`，两者互斥。
- 捕获槽号不小于栈顶（`ErrSlotOutOfRange`）、关闭层越界
  （`ErrInvalidLevel`）、栈槽读写越界（`ErrStackOutOfRange`）均整体拒绝；
  被拒绝的操作不改变栈、栈顶与任何捕获变量。

### 本地验证

```bash
# 规则覆盖 + 朴素模拟对照 + 并发（含竞态检测），日志打印输入/输出/判定依据
go test -race -v ./upvalue/
```
