# gopi-pro

基于 `gopi` 的外层智能编排器（PoC），实现 `Read → Plan → Act` 循环与 `todos` 状态跟踪。

## 特性

- Read 阶段：先提炼需求
- Plan 阶段：生成结构化 JSON 计划（步骤、风险、审批需求）
- Act 阶段：逐步执行，每步更新 todo 状态，支持失败重试
- Final 阶段：汇总输出
- Plan 失败修复：当计划 JSON 不合规时，自动触发一次修复重试
- 审计落盘：每次运行保存完整 JSON 审计日志
- 优先通过 `go-pi` SDK 直接调用能力（初始化失败时回退到 `gopi --print`）

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

# 查看最近一次审计摘要（不执行新任务）
go run ./cmd/gopi-pro --audit-dir .gopi-pro/runs --show-audit

# 查看第 2 新的审计摘要
go run ./cmd/gopi-pro --audit-dir .gopi-pro/runs --show-audit --show-audit-index 2

# 查看最新审计完整 JSON
go run ./cmd/gopi-pro --audit-dir .gopi-pro/runs --show-audit-full
```

也可以使用构建脚本：

```powershell
cd ../gopi-pro
.\make.ps1 build
.\make.ps1 test
```

```bash
make build
make test
```

## 参数

- `--gopi-bin`：gopi 可执行文件路径
- `--cwd`：任务工作目录
- `--timeout`：每次 LLM 调用超时秒数（默认 300，超时会自动重试 1 次）
- `--auto-approve`：自动批准高风险步骤
- `--max-retries`：每个 act 步骤最大重试次数
- `--audit-dir`：审计日志目录（默认 `.gopi-pro/runs`）
- `--show-audit`：显示最新审计摘要并退出
- `--show-audit-full`：显示指定审计完整 JSON 并退出
- `--show-audit-index`：指定查看第 N 新审计（默认 `1`）
- `--no-spinner`：禁用“思考中”加载动画

## 常见提示

- 若提示 `(no audit directory)` 或 `(no audit files)`，先执行一次正常任务生成审计文件。
- 若出现 `context deadline exceeded`，可先用 `--no-spinner` 排除 UI 干扰，再适当调大 `--timeout` 观察是否为后端响应慢。

## 说明

这是第一版骨架，后续可继续加：

- 工具级审批（危险操作确认）
- 失败重试与回滚策略
- 结构化计划(JSON)与置信度
- 会话持久化与分支
- 并行子任务执行
