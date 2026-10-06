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

## 源码映射服务（srcmap）

`srcmap/` 包实现“最终产物→中间产物→原始源码”两张映射的合成与按位置反查：

- `srcmap.New` 校验并规范化映射；`(*Mapping).Lookup` 二分点查询；
- `srcmap.Compose(m2, m1)` 合成两张映射（在中间映射的段边界处切分）；
- `srcmap.Service` 以名字并发管理登记/合成/查询，四类错误可区分。

HTTP 接口（均为 POST JSON）：

```bash
go run ./cmd/server
curl -s -XPOST localhost:8080/register -d '{"name":"m1","sourceCount":1,
  "lines":[{"GeneratedLine":0,"Segments":[
    {"Start":0,"SourceIndex":0,"OrigLine":1,"OrigCol":0}]}]}'
curl -s -XPOST localhost:8080/query    -d '{"name":"m1","line":0,"column":0}'
```

详细的语义、规范形式规则、复杂度证明、被放弃方案与验证方法见
[`srcmap/DESIGN.md`](srcmap/DESIGN.md)。

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
