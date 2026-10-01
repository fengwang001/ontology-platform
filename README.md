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

## DNS 名字压缩

`dnsname` 包实现同一 DNS 报文内的名字后缀压缩：

- `NewBuilder()` 从偏移 12 开始，`NewBuilderAt(base)` 可指定起始偏移；每个标签必须为 1–63 字节，名字展开后的线格式（含结尾）不得超过 255 字节。
- 写入非根名字时，从完整名字开始依次去掉最前标签查找登记表；命中的第一个后缀就是最长后缀，写出仍缺的前缀标签后接两字节指针。整名命中时只写指针。
- 本次未命中的各后缀按其线格式起点登记；起点为 16384 或更大时不能作为 14 位指针目标，因此不登记。根名字只写一个 0，不压缩也不登记。
- 后缀匹配按 ASCII 大小写不敏感；解码指针目标时返回该后缀首次登记时的原始大小写。
- 指针的高两位为 `11`，低 14 位是从报文起始算起的绝对偏移。解码允许指针指向另一个指针，但每一跳的目标都必须不小于 `base` 且严格小于当前指针起点；自指、前向、越出报文和指向 `base` 之前都会被拒绝。
- `Decode` 返回标签序列和调用方名字字段消费的字节数；遇到指针时消费计数只包含到首个指针的两个字节，随后继续展开目标。
- 构造器用互斥保证并发调用等价于某种串行顺序；校验失败或会使报文超过 65535 字节的写入不会追加字节，也不会改变登记表。

本地验证：

```bash
go test -v ./dnsname
go test -race ./...
gofmt -l dnsname
go vet ./...
```
