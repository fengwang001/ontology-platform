# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `rollout/`：带指标闸门与自动回滚的灰度放量流量切分器。
  分阶段比例放量、同标识归属恒定、会话粘性、驻留/样本/错误率闸门、
  连续失败自动回滚、人工开始/重置/降级；时钟由调用方注入，
  全部行为可确定复现。设计取舍见 `rollout/DESIGN.md`。

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

# rollout 包：随机差分测试（-v 打印逐步输入/输出/判定日志）
go test -race -run TestDifferentialRandomSequences -v ./rollout

# rollout 包：路由/观测复杂度基准（开销不随历史请求总数增长）
go test -bench . -benchmem -run '^$' ./rollout

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
