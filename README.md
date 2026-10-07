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

## 动作嵌套调用（前置继承边界 / 后置传播）

实现位于 `chain/`：嵌套动作调用的前置独立重评（可见外层未提交写入计划）、
关键调用失败整体放弃、非关键调用失败记录后继续、自我触发触发前检测
（开销仅与当前链条深度有关）、并发链条提交点可串行化，以及四类结论
（前置未通过 / 关键后置失败放弃 / 非关键失败继续 / 自我触发拒绝）的审计暴露。

详见 [`chain/DESIGN.md`](chain/DESIGN.md)（关键取舍、被放弃的方案、本地验证方法）。

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache GOPATH=/tmp/gopath
go test -race -v ./chain/
```
