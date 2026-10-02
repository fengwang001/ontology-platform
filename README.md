# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`kerberos`](kerberos/DESIGN.md)：Kerberos 风格的票据发放、续期与重放缓存簿。
  支持可后置 TGT（`IssueTGT`）、凭 TGT 签发服务票据（`TGS`）、续期
  （`Renew`）、后置票据验证（`Validate`）、服务端偏差与重放校验
  （`Authenticate`）以及改密失效（`ChangeKey`）。票据时间公式、各操作固定
  判定次序、重放缓存保留范围与测试方法见 `kerberos/DESIGN.md`。

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
