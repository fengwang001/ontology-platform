# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `pretty`：行宽适配排版引擎。把调用方构造的文档树按给定行宽渲染成
  文本：能放进一行的结构放在一行，放不下的结构在其可断点处换行并
  缩进；支持组、缩进、对齐、条件文本、命名片段引用，返回超宽行清单，
  并对非法文档与超限输入给出可区分的错误。设计与验证方法见
  [pretty/DESIGN.md](pretty/DESIGN.md)。

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
