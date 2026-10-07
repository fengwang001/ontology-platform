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

## 链接类型实例层仲裁

`link/` 包实现两个对象类型之间链接的创建、去重与撤销仲裁：双方向独立
基数（0 / 正整数 / 不限）、区分属性组合去重、撤销即释放、并发下等价于
某一全序串行执行，以及四类可精确区分的失败。设计、取舍与验证方法见
[`docs/link-arbitration-design.md`](docs/link-arbitration-design.md)，
可运行用法见 `link` 包的 `Example`。
