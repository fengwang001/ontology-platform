# 使用指南：接触者追踪系统

包导入路径：`ontology/contacttracing`，参考实现：`ontology/naivemodel`。

## 快速开始

```go
import ct "ontology/contacttracing"

s := ct.New()

// 1) 住宿（区间左闭右开，单位分钟）
s.RecordStay("p1", "R301", 5000, 1000, 1120) // 一次性追补已结束住宿
s.RecordAdmission("p2", "R301", 6000, 5900)  // 登记入住，出时刻未定
s.RecordDischarge("p2", "R301", 7000, 6500)  // 后续补登出住

// 2) 病例
cid, _ := s.RegisterCase("p1", 5000, 3000)   // now=5000，发病=3000
s.RecordIsolation(cid, 5000, 4000)           // 可选：隔离时刻截断传染期
s.CorrectOnset(cid, 8000, 2800)              // 改正发病时刻，立即重新推导
s.RevokeCase(cid, 9000)                      // 撤销，不再产生接触者

// 3) 查询（now 必须不小于上次接受操作的 now）
entries, _ := s.ListContacts(cid, 8000)      // 密接 + 次密接清单
st, _ := s.PatientStatusAt("p2", 8000)       // 个体状态
// st.Status: UNRELATED / CLOSE_QUARANTINE / SECONDARY_OBSERVATION / RELEASED
// st.ReleaseAt: 非无关时的解除时刻
```

## 错误处理

错误为 `*contacttracing.Error`，用 `Kind` 区分：

```go
if e, ok := err.(*ct.Error); ok {
    switch e.Kind {
    case ct.ErrInvalidParameter: // 参数非法（最高优先级）
    case ct.ErrClockRollback:    // 时钟回退
    case ct.ErrNotFound:         // 对象不存在
    case ct.ErrInvalidState:     // 状态不符
    case ct.ErrStayConflict:     // 住宿冲突
    }
}
```

## 判定规则速查

- 密接阈值：传染期内同病房正时长累计 `>= 120` 分钟（跨病房/住宿段相加，含等于）。
- 次密接：与密接在其暴露期 `[首接触, 确诊登记时刻)` 内同病房累计 `>= 120`，不再外扩。
- 隔离：密接触发最后接触 `+7 天`；次密接触发最后接触 `+3 天`；恰到达边界即解除。
- 多来源：在期取最严等级，解除时刻取最晚；全部过期但曾被认定为 `RELEASED`。

## 测试与日志

- 全量：`go test ./...`；竞态：`go test -race ./contacttracing/`。
- 随机对照：`go test ./contacttracing/difftest/ -run TestRandomDifferential -v`，
  逐步日志输出到 `logs/random_differential.log`（输入/输出/判定依据）。
