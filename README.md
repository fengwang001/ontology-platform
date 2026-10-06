# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 共享单车电子围栏服务

`bikefence/` 包提供运营区/禁停区/奖励区三类围栏的还车判定、容量与计费、
奖励每日限次、跨围栏调度任务的生成/认领/超时/完成，以及 R 树空间索引与
朴素逐围栏模型的随机对照。设计取舍见 `bikefence/DESIGN.md`：

```bash
go test ./bikefence/ -count=1      # 全量场景 + 4000 条随机对照
go test -race ./bikefence/         # 并发线性化验证
```

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
