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

## 组件

- [`consistentread`](consistentread/README.md)：带滞后上限的一致性读取器，
  维护单调推进的提交/已应用位点，支持降级（立即返回并报告降级）与阻塞
  （冻结调用时刻目标再等待）两种模式；语义、失败原因与本地验证方法见
  `consistentread/README.md`。
