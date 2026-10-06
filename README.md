# ontology-platform

Go 领域服务工程，当前实现快递中转场的集袋、封袋、出场、拆袋核对与快照查询，核心包为 `expresshub`。

## 环境要求

- Go 1.26+

若 `go` 不在默认 `PATH`，本机可使用 `/usr/local/go/bin/go`。沙箱中建议设置：

```bash
export GOCACHE=/tmp/go-build
export PATH=/usr/local/go/bin:$PATH
```

## 快速运行示例

```bash
GOCACHE=/tmp/go-build /usr/local/go/bin/go run ./cmd/demo
```

示例覆盖自动封袋、手动封袋、出场、拆袋差异和待查件不可重加。

## 测试

```bash
# 全量测试
go test ./...

# 详细日志：边界、随机输入、输出和判定依据
go test -v ./expresshub

# 竞态检测
go test -race ./...

# 常数时间索引基准
go test ./expresshub -run '^$' -bench BenchmarkDuplicateAddDecision -benchtime=1000x
```

## 文档

- 设计说明、关键取舍、放弃方案与复杂度论证：`docs/expresshub.md`
- API 和状态常量：`docs/api.md`

## 检查

```bash
gofmt -l .
go vet ./...
```
