# 物业维修派单服务

该包提供纯内存、可重放的维修工单优先级管理和承包商派单服务。

## 模块划分

- `model.go`：工单、承包商、时限和事件领域模型。
- `service.go`：公开操作、互斥串行化、时钟推进和生命周期编排。
- `priority.go`：工单优先级堆与队列次序。
- `selector.go`：承包商候选排序、紧急抢占资格和受害者选择。
- `timers.go`：响应与完成时限的小根堆。
- `snapshots.go`：内部对象到只读 DTO 的转换。
- `errors.go`：固定语义的哨兵错误。

## 验证

```bash
go test ./...
go test -race ./...
go vet ./...
REPAIR_TEST_LOG=1 go test -run TestNaive -v ./repair
```

随机对照测试会打印每步输入、返回结果和朴素模型判定依据，便于重放分叉。
