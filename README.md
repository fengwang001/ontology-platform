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

## 混合逻辑时钟（HLC）

`hlc/` 包实现了物理时钟回拨下仍严格单调、因果一致的混合逻辑时钟，
支持本地、发送、接收三类事件、在途消息登记、按时间戳有序的节点历史，
以及八类互不相同的拒绝原因（失败不留痕）。

```bash
go test -race -v ./hlc
```

推进规则、偏差/计数上限、边界行为、错误类别与验证方法见
[`hlc/README.md`](hlc/README.md)。
