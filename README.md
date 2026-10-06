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

## 字体匹配内核（`fontcore` 包）

`fontcore/` 是一个自包含的网页字体协调内核：字体族登记、宽度→倾斜→字重
三维人脸匹配、字符范围子集加载、阻塞/交换/回退/可选四种显示策略的时期
推演，以及带回退族的文本整形（逐字符结果 + 连续段落合并）。

- 关键取舍、被放弃方案与验证方式见 `fontcore/DESIGN.md`（≤40 行）。
- 复杂度：字符覆盖判定为“公共段二分 + 中点区间树”，
  `O(log S + log R + k)`，单字符匹配只遍历覆盖候选，不随族内人脸/区间总数线性增长。
- 并发：单互斥量串行化，等价于某个合法串行顺序；每张人脸至多触发一次加载。
- 参考模型：`naive.go` 是独立编写的暴力实现，`TestRandomDifferential`
  用 80 个随机种子 × 300 步操作序列做逐字符/触发/错误类别差分对照。
- 日志：`eng.SetLogger(log.New(w, "", 0))` 可打印每次登记、推进、触发、
  加载报告与整形的输入、输出和判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
