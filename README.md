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

## Cookie 存储与容量淘汰内核

`ontology/cookie` 是浏览器 Cookie 的请求附带判定与按站点容量淘汰内核，
不解析原始响应头，条目以结构化字段写入。关键文件：

- `cookie/entry.go`、`cookie/errors.go`：条目/请求/配置类型与五类可区分错误。
- `cookie/heap.go`：LRU、过期、站点计数等带索引堆（淘汰 victim 选择 O(log n)）。
- `cookie/store.go`：写入、覆盖、过期处理、站点/全局容量淘汰、清除、时钟与并发。
- `cookie/request.go`：路径段 trie、路径边界、同站规则、请求附带与脚本读取。
- `cookie/naive.go`：独立编写的朴素参考模型，供随机操作序列对照。
- `cookie/kernel_test.go`：规则覆盖面、守恒、并发、复杂度断言与随机对照测试。
- `cookie/DESIGN.md`：关键取舍、被放弃方案与本地验证方法。

快速开始：

```go
k, _ := cookie.New(cookie.Config{PerSiteLimit: 50, GlobalLimit: 3000, LaxGrace: 120}, nil)
_, _, _ = k.Write(cookie.WriteInput{
	Name: "sid", Domain: "example.com", Path: "/", Secure: true,
	SourceDomain: "example.com", SourceSecure: true, Now: 1,
})
res, _ := k.Attach(cookie.RequestInput{
	TargetDomain: "example.com", TargetPath: "/", Secure: true,
	Initiator: "example.com", Now: 2,
})
_ = res.Sent // 附带的条目（已更新最近访问时刻）
```
