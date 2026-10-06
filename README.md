# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `sigchain`：提交签名验证链服务。依据可信锚点、密钥有效区间与多级背书关系，
  对分支从锚点到顶端的第一父提交链给出「全链可信」或带定位与原因的「不可信」裁决。
  支持密钥轮换背书、吊销（可选追溯）、合并提交豁免与并发访问。设计取舍见
  [DESIGN.md](DESIGN.md)。

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
