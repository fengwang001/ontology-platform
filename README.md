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

## 重复投保分摊赔付引擎（`apportion` 包）

同一损失下多张保单（限额比例型 / 独立责任型 / 超额型混合）的唯一、可复现分摊裁定。

```go
e := apportion.NewEngine(os.Stdout) // 传入 nil 关闭判定依据日志
e.Register(apportion.Policy{Insured: "I1", PolicyNo: "P1", /* 免赔/限额/区间/条款 */})
out, err := e.Accept(apportion.Loss{LossNo: "L1", Insured: "I1", Day: 5, Amount: 1000})
e.Undo("L1", "I1") // 仅允许撤销该被保人末笔受理
```

- 规则、关键取舍、被放弃方案：`apportion/DESIGN.md`
- 独立朴素模型（big.Rat）随机差分：`go test ./apportion/ -run TestDifferentialRandom -v`
- 并发等价串行（竞态检测）：`go test -race ./apportion/`
- 规模无关性基准（历史损失数 × 其他被保人保单数）：
  `go test ./apportion/ -run '^$' -bench BenchmarkAcceptScaling`

## 代码检查

```bash
gofmt -l .
go vet ./...
```
