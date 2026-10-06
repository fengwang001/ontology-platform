# 室内质控 API

## 登记项目

使用 `NewSystem()` 创建系统，再调用 `RegisterAssay(now, spec)`。`AssaySpec` 包含仪器 ID、项目 ID、低/高水平靶值、正整数标准差和正整数有效期。

## 提交运行

调用 `SubmitRun(now, RunInput{...})` 同时提交低、高两个水平测量值。返回：

- `Rules`：按规则一到五排序的全部触发规则；为空表示未失控。
- `Warning`：未触发规则但任一水平超过 2σ。
- `Outage`：判定后项目是否仍处于失控。
- `Recovered`：本次是否为第二次满足恢复条件的运行。

## 校准

`Calibrate(now, instrumentID, assayID)` 清空两个水平的连续序列、恢复在控，但不改变项目参数和已有报告状态。

## 报告

- `IssueReport(now, reportID, instrumentID, assayID)`：通过时报告为 `issued`，被拒绝不留记录。
- `ReviewReport(now, reportID)`：仅 `pending_review` 可复核，成功后为 `reviewed`。
- `ReportStatus(reportID)`：查询 `issued`、`pending_review` 或 `reviewed`。

## 错误

错误优先级为参数非法、时钟回退、对象不存在、项目失控、从未质控、质控过期、状态不符。比较错误使用 `errors.Is(err, qc.ErrQcExpired)` 等方式。
