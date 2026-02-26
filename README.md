# gopi-pro

基于 `gopi` 的外层智能编排器（PoC），实现 `Read → Plan → Act` 循环与 `todos` 状态跟踪。

## 特性

- Read 阶段：先提炼需求
- Plan 阶段：生成结构化 JSON 计划（步骤、风险、审批需求）
- Act 阶段：逐步执行，每步更新 todo 状态，支持失败重试
- Final 阶段：汇总输出
- 通过调用 `gopi --print` 复用现有模型与工具能力

## 运行

先确保已构建 gopi：

```powershell
cd ../gopi
.\make.ps1 build
```

再运行 gopi-pro：

```powershell
cd ../gopi-pro
go run ./cmd/gopi-pro --gopi-bin ../gopi/build/gopi.exe --cwd ../testdemo --auto-approve --max-retries 2
```

## 参数

- `--gopi-bin`：gopi 可执行文件路径
- `--cwd`：任务工作目录
- `--timeout`：每次 LLM 调用超时秒数（默认 120）
- `--auto-approve`：自动批准高风险步骤
- `--max-retries`：每个 act 步骤最大重试次数

## 说明

这是第一版骨架，后续可继续加：

- 工具级审批（危险操作确认）
- 失败重试与回滚策略
- 结构化计划(JSON)与置信度
- 会话持久化与分支
- 并行子任务执行
