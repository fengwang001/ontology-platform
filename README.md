# 本体平台 — 服务端请求优先级与公平性准入控制器

基于流分类、份额席位、流间轮转和宽请求队首阻塞的准入控制实现，Go 包 `ontology/admission`。
详细设计与取舍见 [DESIGN.md](./DESIGN.md)。

## 环境要求

- Go 1.26+

## 运行

本仓库没有独立服务进程，控制器以库形式提供。

```bash
# 编译
go build ./...

# 全部测试（场景 + 并发 + 朴素模型随机差分）
go test ./...

# 竞态检测
go test -race ./admission

# 详细场景日志（每次操作的输入、实际输出、判定依据）
go test ./admission -v

# 性能基准（验证入队/完成不随流数与队列长度线性增长）
go test ./admission -bench Benchmark -benchmem
```

如本机 Go 缓存目录只读，可指定：

```bash
GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go test ./...
```

## 核心语义速览

- 请求按顺序匹配规则：优先级数值小者优先，同值按规则名称升序，命中第一条即决定级别与流。
- 受限级别名义席位 = `floor(total*share/sumShare)`，余量按份额降序、名称升序补一席。
- 豁免级别立即执行，不占席位、不排队。
- 请求席位 1..10；超过级别名义席位（或名义为 0）直接拒绝“席位不可满足”。
- 受限级别无等待者且席位足够时立即执行；有等待者必须排队，不得插队。
- 同流 FIFO；流间按“最近一次空→非空”顺序轮转，服务后流移到轮转尾部。
- 轮到的流队首放不下时整级停止出队，绝不越过它服务其他流。
- 排队超时左闭：达到 `enqueued + timeout` 即失效，在任何后续操作之前体现。
- 错误优先级：参数非法 > 时钟回退 > 无匹配 > 席位不可满足 > 队列满 > 超时。
- 配置整份热更新：非法整体不生效；在途不打断、排队不重分类；
  超过新名义席位的排队请求在更新时立即拒绝。
- 所有调用经互斥串行化，并发结果等价于某个合法串行顺序，占用永不超过名义席位。

## 代码结构

- `admission/config.go`：规则 / 级别配置、匹配、份额分配。
- `admission/flowqueue.go`：流 FIFO 与流间轮转链表。
- `admission/expiry.go`：超时最小堆。
- `admission/controller.go`：准入状态机、热更新、并发控制。
- `admission/*_test.go`：规则、份额、轮转、阻塞、超时、错误、热更新、并发测试。
- `admission/admissiontest/naive.go`：独立朴素参考模型。
- `admission/admissiontest/differential_test.go`：朴素模型 vs 实现的随机逐步差分。
