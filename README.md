# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `exam/`：题库版本管理与试卷组卷约束引擎。题目可修订（版本单调递增、旧版不可变）、
  停用与下架；试卷发布时冻结题目版本快照，受总分、知识点覆盖、难度分布与互斥组
  （传递闭包）约束；已发布试卷仅支持单题原子替换。设计取舍见 [DESIGN.md](DESIGN.md)。

## 环境要求

- Go 1.26+（`go version` 确认）

## 构建

```bash
# 拉取依赖并编译全部包
go mod tidy
go build ./...
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
