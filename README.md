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

## 联合类型流敏感窄化分析器

根包 `ontology` 同时提供一个独立的流敏感类型窄化分析器（不依赖 server）：

- 五种原子类型（数字/字符串/布尔/空值/未定义）、数字与字符串字面量、
  布尔字面量、有限属性对象类型与联合类型；联合无序、嵌套展开、重复去重、
  字面量被同联合原子吸收、空联合为永不、单成员等同该成员。
- 支持赋值、`typeof`、严格/宽松相等、真值判断、判别属性，以及取反、
  短路逻辑与/或、条件分支与提前返回的流敏感窄化。
- 程序点可达性与窄化结果可区分；四类错误
  （参数非法 / 不可访问属性 / 缺少判别属性 / 不可赋值）按固定优先级与程序
  次序只报一处，任一错误都使整次分析失败。
- 分析结果不可变、可并发查询；查询代价只与被查询变量窄化类型规模相关，
  不随程序语句总数增长，也不重新分析。

设计与取舍、被放弃方案、复杂度证明与本地验证步骤见 `DESIGN.md`。

```bash
# 全量测试：手工场景 + 300 个随机程序对朴素枚举模型的差分对照（带日志）
go test -v ./...

# 竞态与静态检查
go vet ./...
go test -race ./...
```
