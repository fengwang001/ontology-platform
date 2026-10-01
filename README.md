# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`stackcheck/`](stackcheck/)：字节码函数栈深校验器。逐函数注册指令序列，用 FIFO
  工作表推导每条可达指令的入口栈深，保证汇合一致、不下溢、不超上限、`RET` 入口恰为
  1；拒绝原因可区分且拒绝不留痕，注册 / 查询并发安全。指令栈效应、推导顺序、错误
  优先级与本地验证方法见 [`stackcheck/README.md`](stackcheck/README.md)。

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
