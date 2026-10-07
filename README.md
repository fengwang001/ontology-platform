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

## 子系统：变更流消费与恰好一次补偿

`compensate/` 消费本体动作变更流，并对每条事件恰好触发一次关联补偿动作：

- 以生产者分配的 `EventID` 唯一区分「网络重试重复投递」与「独立等价二次调用」；
- 补偿副作用逐项以独占标记在可串行化事务内提交，崩溃后续作只补齐未生效项；
- 撤销与补偿以「领取点」为唯一线性化分界，结果确定；
- 四类互斥错误（E3 身份 / E2 历史缺失 / E4 原子性 / E1 目标消失）按优先级报告并冻结边界；
- 去重为 O(1) 定点探测，审计 `Seq` 提供全局串行顺序证据。

```bash
go test ./compensate
go test -race -v ./compensate
go test ./compensate/ -run ExampleProcessor -v
```

设计取舍、被放弃方案与本地验证方法见 `compensate/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
