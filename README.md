# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 支付预授权额度账务（`preauth`）

`preauth` 包实现预授权持有、增量授权与分次/超额捕获的额度账务。核心恒等式：

```
可用额度 = 信用额度 - 已入账余额 - 全部有效持有
```

特性：授权过期自动释放（无需后台任务）、超额捕获占用可用额度、容差基点
上浮（向下取整）、终捕/撤销释放、退款不恢复持有、全局编号唯一、严格的
错误优先级、时钟回退保护、并发安全且可确定性重放。查询可用额度的开销
不随该账户历史授权总数或全部账户数增长（最小堆惰性折叠，见设计文档）。

```go
sys := preauth.NewSystem(preauth.Config{ValidityDays: 7, ToleranceBPS: 1000})
_ = sys.CreateAccount("card-1", 10000, 0)
_ = sys.Authorize("a1", "card-1", 3000, 1)
sys.Available("card-1", 1) // 7000
```

设计取舍、复杂度证明与被放弃方案见 `docs/design.md`。

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
