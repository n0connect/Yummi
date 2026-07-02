<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">一个隐私优先、故障关闭（fail-closed）的 AI 渗透测试执行器。</p>

<p align="center">
  <a href="#quick-start"><img alt="CI" src="https://img.shields.io/badge/CI-passing-brightgreen?style=flat-square" /></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-MIT-brightgreen?style=flat-square" /></a>
  <a href="https://github.com/n0connect/yummi/releases"><img alt="Version" src="https://img.shields.io/badge/version-v0.1.0-brightgreen?style=flat-square" /></a>
  <a href="orchestrator/go.mod"><img alt="Go" src="https://img.shields.io/badge/Go-1.22-00ADD8?style=flat-square" /></a>
  <a href="gateway/Cargo.toml"><img alt="Rust" src="https://img.shields.io/badge/Rust-stable-DEA584?style=flat-square" /></a>
  <a href="ui/package.json"><img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?style=flat-square" /></a>
</p>

<p align="center">
  <a href="README.md">English</a> |
  <a href="README.tr.md">Türkçe</a> |
  <a href="README.zh.md">简体中文</a> |
  <a href="README.de.md">Deutsch</a> |
  <a href="README.es.md">Español</a> |
  <a href="README.fr.md">Français</a> |
  <a href="README.ja.md">日本語</a> |
  <a href="README.ko.md">한국어</a> |
  <a href="README.ru.md">Русский</a> |
  <a href="README.pt-BR.md">Português (Brasil)</a>
</p>

<p align="center">
Yummi 将自然语言转化为实用的安全工作流，同时保持显式的流量控制和操作员权限。自治的红队（Red-Team）大脑驱动一个加固的、网络隔离的 Kali 沙箱 —— 且内存安全的 Rust 信任边界确保 AI 永远无法触碰未授权的目标。
</p>

---

## 目录

- [Yummi 是什么](#yummi-是什么)
- [系统架构](#系统架构)
- [信任边界（最核心的不变量）](#信任边界)
- [内容层安全](#内容层安全)
- [智能体（Agent）引擎](#智能体引擎)
  - [图状态机 —— 节点与边](#图状态机)
  - [标签协议](#标签协议)
  - [与大脑无关的核心](#与大脑无关的核心)
- [知识库（Knowledge Store） —— 双层内存 + 学习循环](#知识库)
- [技能（Skills） —— 声明式即时剧本](#技能)
- [子智能体（Sub-agents） —— 受限委派](#子智能体)
- [TRACE —— 智能体可观测性](#trace-可观测性)
- [指挥官、验证器与出口（Egress）模式](#指挥官验证器与出口模式)
- [网关（Rust 信任边界）](#网关)
- [沙箱](#沙箱)
- [接口 —— Web UI 与 TUI](#接口)
- [快速开始](#快速开始)
- [开发人员工作流](#开发人员工作流)
- [配置](#配置)
- [项目目录布局](#项目目录布局)
- [安全不变量](#安全不变量)

---

## Yummi 是什么 <a id="yummi-是什么"></a>

Yummi 包含 **四个组件和一个备用前端**，围绕一条硬性规则构建：*AI 提议，网关执行。* AI 运行的每个渗透测试命令都流经同一个地方，并受到崩溃后依然存活的故障关闭网络策略的控制。

```
用户 / 操作员  →  编排器 (Go)  →  网关 (Rust, IPC)  →  沙箱 (Kali Docker)
  Web UI / TUI     指挥官 + 智能体    故障关闭 nftables        仅工具执行
                   知识库 + API      策略引擎 + 审计
```

- **orchestrator** (Go) —— 拥有 AI 指挥官、智能体图引擎、知识库和 REST/WebSocket API。从不直接触碰 nftables。
- **gateway** (Rust，二进制文件 `yummi-gateway`) —— 唯一可以从沙箱打开外部流量的组件。启动时安装默认拒绝所有（`DROP`）的 nftables 规则；如果它崩溃，这些规则仍然保留（**故障关闭**）。
- **sandbox** (Kali Docker) —— 只读文件系统，非 root 用户，仅有 `NET_RAW`/`NET_ADMIN` 权限。被动式：仅在编排器对其运行 `docker exec` 时才执行操作。
- **ui** (React 18 + Vite) —— `:3000` 端口上的 Web 仪表板，将 `/api/` 和 `/ws/` 请求代理到 `:9080` 端口上的编排器。
- **TUI** (Charm Bubble Tea) —— 完全基于终端的操作员控制台（`yummi --tui`），与同一个 REST/WS API 通信。

---

## 系统架构 <a id="系统架构"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            Web 仪表板      ·      终端 TUI           │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ 编排器 (ORCHESTRATOR) · Go                           │
        │                                                      │
        │   API ─▶ 指挥官 ─▶ 智能体图 ─▶ 执行器                │
        │                          │                           │
        │                          ▼  (共享世界模型)           │
        │              知识库: 发现 · 内存 · 上下文            │
        │                          · TRACE                     │
        └───────────────────────────┬──────────────────────────┘
                                    │  Unix 套接字 · 带有长度前缀的 JSON IPC
        ┌───────────────────────────▼──────────────────────────┐
        │ 网关 (GATEWAY) · Rust 信任边界                       │
        │ 故障关闭 nftables · 出口 · 审计 · 看门狗              │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── 唯一的路径             │ 受控出口 (egress)
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ 沙箱 (SANDBOX) · Kali   │      │   外部目标           │
        │ 只读 · 非 root          │      │   (按网关策略)       │
        └─────────────────────────┘      └──────────────────────┘
```

信号沿一条主干直下 —— 操作员 → API → 指挥官 → 智能体 → 执行器 → 网关 → 沙箱。`docker exec` 是从信任平面进入沙箱的**唯一路径**，处于网关范围检查的下游；沙箱*仅*通过网关的 nftables 规则访问外部世界。知识库挂载在智能体上作为共享的世界模型。

### 网络拓扑

```
yummi_internal (172.20.0.0/24): 沙箱 .10 · 网关 .2 (同时也是 DNS) · 编排器 .3 · ui .4 · docker-proxy .5
yummi_external:                 网关 · 编排器 · 仅 ui —— 沙箱特意排除在外。
                                其唯一的出口路径是通过网关的 nftables 规则。
主机端口: 3000 → ui nginx · 9080 → 编排器 API (仅本地 localhost)
```

沙箱的 DNS 强制通过网关（`172.20.0.2`），因此每次域名查询都会被记录。

---

## 信任边界 <a id="信任边界"></a>

这是代码库中**最核心的不变量**：*除非明确允许，否则全部拒绝。*

- 网关在 IPC 套接字打开**之前**安装 `DROP` 所有规则。在显式的 `Allow` 规则下发之前，沙箱没有网络连接。
- 如果网关进程死亡，内核中的 `DROP` 规则依然存在 —— 系统**故障关闭**，永远不会保持打开状态。
- **阻止（Block）规则永远优先于允许（Allow）规则。**
- 渗透测试命令进入 shell **仅通过这一个地方**：`sandbox.Bridge.Exec`，它运行 `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`。智能体本身从不调用 `exec.Command` —— 它只将命令加入执行队列，由执行器消耗（网关范围检查 → 沙箱执行）。智能体/LLM 输出绝无法访问主机端命令路径。

> 网关模块在 Docker 容器内运行，而非宿主机上，并通过容器环境的内核支持来执行网络策略。如果直接在 macOS 宿主机上运行编译后的二进制文件（Docker 之外），则会因缺少 Linux 内核而无法执行安全策略。

---

## 内容层安全 <a id="内容层安全"></a>

网关是极佳的**网络层**边界。但渗透测试指挥官面临着第二个、更微妙的攻击面：流入 LLM 提示词的**内容**。目标的命令输出、抓取的网页、MCP 响应 —— 这些都不是操作员编写的，任何内容都可能带有提示词注入（`ignore previous instructions…`）、C2/promptware 指令、数据外泄载荷或不可见的双向（bidi）统一码。Yummi 将相同的故障关闭姿态扩展到内容层。

```text
    工具输出 · 内存写入 · 技能文件                 (非操作员编写)
                       │
                       ▼
             ┌───────────────────┐   threatscan: 注入 · 外泄 · C2
             │     threatscan    │               · 不可见统一码
             └─────────┬─────────┘
        工具输出       │   BELLEK 写入 / 技能加载
    (运行中无法干预)    │   (用户媒介)
                       ▼                ▼
    ⚠ 警示横幅            ⛔ 在严格模式下拦截
    "untrusted DATA"
                       │
                       ▼
                  LLM 提示词
```

- **`internal/security/threatscan`** —— 一个叶子包（仅依赖标准库），在三个范围（`all` / `context` / `strict`）内进行扫描。工具输出被**扫描并带有警示横幅**（检测而非拦截 —— 操作员无法在命令运行中进行干预）；`[REMEMBER]` 写入和技能加载在严格范围下会被**拦截**，因此被污染的输出无法植入持久的跨会话提示词注入。它还会在输出到达大脑之前剥离 **ANSI/C1 转义序列**。
- **内存来源归属** —— 每一行持久化内存都记录了写入者（`agent` / `subagent`）以供审计。
- **架构适配性测试** (`internal/security/invariants`) —— 遍历源码树的可执行守卫，如果在审计的 shell 边界文件之外出现了 `os/exec`、如果在生产代码中导入了 `internal/mcp`（休眠锁）或混入了遥测端点，构建将**宣告失败**。以下不变量由测试强制执行，而不仅仅是文档说明。

---

## 智能体引擎 <a id="智能体引擎"></a>

Yummi 的智能体**不是扁平的 `while` 循环**。它是一个显式的**图状态机**：LLM 每轮输出一个带标签的动作，确定性的路由引擎通过固定的边（edge）表遍历类型化节点，直到到达终端节点。这种设计驯服了上下文窗口耗尽和无限动作循环的问题 —— 每一步都会将完整任务状态重新渲染到提示词中，而不是依赖脆弱的对话历史记录。

### 图状态机 —— 节点与边 <a id="图状态机"></a>

引擎位于 `orchestrator/internal/agent`。`graph.go` 保存节点集和 `graphEdges` 路由表（`node → edge → nextNode`）；每个节点存放在各自的 `node_*.go` 文件中。

**十个节点：**

| 节点 | 文件 | 职责 |
|------|------|----------------|
| `step` | `node_step.go` | 向 LLM 请求下一个动作；将完整任务状态 + SKILLS + DELEGATION + MEMORY 渲染到提示词中；根据返回的控制标签进行路由。 |
| `tool` | `node_tool.go` | 验证、过滤并执行缓冲命令；检测卡住/重复的模式；监督长耗时命令。 |
| `consult` | `node_consult.go` | 执行中咨询（例如长耗时命令） —— 询问 LLM `[CONTINUE]`/`[ABORT]`。 |
| `retry` | `retry.go` | 具有指数回退的 LLM/解析错误恢复；路由回其调用者或放弃。 |
| `summarize` | `node_lifecycle.go` | 上下文压缩：当 token 预算消耗 ≥80% 时，将观察历史压缩为摘要。 |
| `approval` | `node_lifecycle.go` | 操作员审批门槛（阻塞通道上的循环 goroutine）。 |
| `finish` | `node_lifecycle.go` | 终端节点：已完成 / 失败 / 已停止；如果模型未生成摘要，则合成摘要。 |
| `ask` | `node_lifecycle.go` | 终端节点：暂停并向操作员提问。 |
| `handoff` | `node_handoff.go` | 将聚焦的子任务委托给专家子智能体，并反馈其摘要。 |
| `remember` | `node_remember.go` | 将结构化内存（例如代码图）持久化到知识库中。 |

**遍历路径**（`graph.go` 中的 `Execute`） —— 每个工作节点都路由回 `step`，因此引擎本身即是循环；它仅在终端节点退出：

```text
                     (每个工作节点都会循环回 STEP)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ 询问 LLM│ ◀───── PLAN_UPDATED ─────  │ exec │── 需要审批 ──────────▶ approval ─▶ STEP
    │ 下一步  │                            │  +   │── 长耗时 ────────────▶ consult  ─▶ STEP
    │ 动作    │                            │ 守卫 │── 卡住 / 循环循环 ─────▶ consult  ─▶ STEP
    └────┬────┘                            └──────┘
         │  finish · 预算用尽 ──▶ FINISH  (终端)
         │  ask_operator · 挂起 ──▶ ASK  (终端)
         │  llm_error ──▶ retry ──▶ (返回调用者)
         │  summarize · remember · handoff ──▶ (返回 STEP)
         ▼
    每个 STEP 都会重新将完整的任务状态 (plan · observations ·
    SKILLS · MEMORY) 渲染进提示词 —— 绝不是原始的聊天历史。
```

终端节点（`finish`, `ask`）没有出边 —— `Execute` 到达它们时停止。`retry` 动态路由回调用它的任何节点。每个转换都带有检查点，因此崩溃的任务会从最后一个节点恢复。

**循环阻断是分层的** (`tool` 节点)：精确重复指纹识别（`[DUPLICATE_BLOCKED]`）、针对 `A→B→A→B` 循环的滑动窗口卡住检测器（`[STUCK]`）、捕获哈希漏掉的语义近乎重复的 **工具抖动（tool-thrash）** 守卫 —— 例如交替执行 `ps -ef` / `ps -ejH` / `ps aux` (`[ABNORMAL_PATTERN]`)，以及在挂起并请求操作员介入之前强制改变方法的连续失败升级机制（`[REPEATED_FAILURE]`）。关键是，程序性内存从不召回*本任务*已经运行过的命令，因此学习循环不会强化重复循环。

### 标签协议 <a id="标签协议"></a>

智能体和 LLM 之间使用**闭合的、带方括号标签的词汇表**进行通信（在 `protocol.go` 的 `Registry` 中定义）。这*不是*自由文本，也*不是* XML —— 而是独占一行的 `[TAG]`，后跟可选的 JSON 体。注册表是单一事实来源，提示词的协议参考由此生成。

```text
   智能体 (step 节点)                                   LLM (大脑)
        │  渲染完整任务状态 + SKILLS · MEMORY                  │
        │  + 协议参考，然后询问 ─────────────────────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  验证 · 卡住/抖动检查 · 范围                         │
        │  ──▶ 网关范围检查 ──▶ docker exec (沙箱)             │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] 头+尾 ──────────────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] 完整输出 ─────────────────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  终端 —— 如果模型遗漏，则合成报告                     │
```

有两个特性使它在*小*模型上表现鲁棒：工具输出在到达大脑之前会经过**威胁扫描并剥离 ANSI 码**（见 [内容层安全](#内容层安全)），以及在缺少前导标签时通过**从 JSON 结构推断控制**来恢复响应（例如包含 `command` 字段 → `RUN_COMMAND`，`role`+`subgoal` → `HANDOFF` 等）。

**智能体 → LLM (观察 / 信封标签) —— 智能体如何返回结果：**

| 标签 | 含义 |
|-----|---------|
| `[TOOL_RESPONSE]` | 命令执行成功；输出在下方。 |
| `[TOOL_RESPONSE_TRUNCATED]` | 输出过长；显示头+尾。回复 `[SEND_FULL]` 获取全部内容。 |
| `[TOOL_RESPONSE_FULL]` | 您通过 `[SEND_FULL]` 请求的完整输出。 |
| `[TOOL_FAILED]` | 命令已运行但失败 —— 请进行调整，勿重复。 |
| `[COMMAND_BLOCKED]` | 格式错误，或网关判定目标超出范围而予以拒绝。 |
| `[DUPLICATE_BLOCKED]` | 您刚运行过这个完全相同的命令 —— 请选择其他命令。 |
| `[REPEATED_FAILURE]` | 连续多次失败 —— 切换工具/路由、重新规划或完成。 |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | 检测到重复模式。 |
| `[LONG_RUNNING]` | 命令超过软限制 —— 回复 `[CONTINUE]` 或 `[ABORT]`。 |
| `[OUTPUT_OVERFLOW]` | 终端输出正在压迫上下文窗口。 |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | 操作员拒绝 / 未响应受审批控制的命令。 |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | 规划已接受 / 剩余步骤较少。 |
| `[UNKNOWN]` | 智能体无法分类此状态 —— 由您决定。 |

**LLM → 智能体 (控制 / 动作标签) —— 模型回应的标签（一个标签后跟一个 JSON 体）：**

| 标签 | Body | 效果 |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | 运行沙箱命令。 |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | 修正规划。 |
| `[FINISH]` | `{summary, findings}` | 达成目标。 |
| `[HANDOFF]` | `{role, subgoal}` | 将子任务委派给专家子智能体。 |
| `[REMEMBER]` | `{key, value}` | 在键下持久化结构化内存（永远不会忘记）。 |
| `[ASK_OPERATOR]` | `{question}` | 阻塞，需要人工介入。 |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | 重新发送完整输出 / 继续等待 / 取消命令 / 停止任务。 |

弱模型经常会漏掉前导标签，只输出 JSON —— 解析器会根据负载格式推断出控制标签（`command` 字段 → `RUN_COMMAND`，`role`+`subgoal` → `HANDOFF` 等），因此无论模型多么不遵守格式，智能体都能保持鲁棒。

### 与大脑无关的核心 <a id="与大脑无关的核心"></a>

LLM 是隐藏在单方法接口 (`LLM { Chat(...) }`) 后面的**可注入、可替换依赖项**。核心部分专为*质量和覆盖范围*而设计，而不是针对当前连接的模型的特定功能。一个小型的本地模型（qwen3:8b）是我们的开发大脑和质量压力测试 —— 如果核心设计在小脑上能健康运行，在大脑上也同样适用。接入一个更强大的模型大脑，同一个核心只需*扩展*其表现，绝不需要重写。

---

## 知识库 —— 双层内存 + 学习循环 <a id="知识库"></a>

`internal/knowledge` 是一个叶子包（除了 `scope` 之外不依赖任何内部包），是**世界模型的单一拥有者**。智能体、执行器、API 和所有子智能体都会写入这同一个地方 —— 因此智能体的发现被转化为结构化、可查询的共享知识，而不是困在单一任务中的自由文本。

**双层内存：**

1. **事实性（单次任务）** —— `targets` 和 `findings` 表。发现结果带有出处信息：`source`（`manual` / `agent` / `subagent`）和产生它的 `task_id`。
2. **程序性（可移植，去目标化）** —— `procedural_memory` 表。每一个执行过的命令都会被记录为一种*战术*：`{tool, command_pattern, outcome}`。其中目标 token 会被替换为字面量 `<TARGET>` 占位符（通过 `scope.ExtractTarget`），从而确保**没有针对具体目标的敏感数据泄露**到可移植层。完全相同的战术使用 `hit_count` 去重。执行结果（`worked` / `failed`）是**机械地**根据退出状态码推导的 —— 因而此学习循环甚至不依赖模型大脑。

**学习循环：**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ 智能体运行命令     │──▶│ 执行器记录去目标的      │──▶│ procedural_memory │
    │                  │   │ 战术形式              │   │ 工具·模式·        │
    └──────────────────┘   └───────────────────────┘   │ 结果·碰撞次数      │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step 注入    │◀─│ RetrievalStrategy 读取    │◀────────┘
    │ MEMORY 块到提示词 │  │ 当前工具的关联战术        │
    └───────────────────┘  │ ‹排除本任务中已经运行过   │
                           │  的命令›                  │
                           └───────────────────────────┘
```

检索（Retrieval）隐藏在 `RetrievalStrategy` 接口后面。当前提供的实现 `StructuredRetrieval` 是确定性 SQL，基于当前会话和近期使用工具进行过滤 —— 零外部依赖，完全本地化。语义/嵌入（embedding）检索策略以后可以**在不触碰核心逻辑的情况下**直接替换进来。

排除（EXCLUDES）步骤是承载负荷的：程序性内存是用于**跨任务迁移**的，而不是把当前任务自己的工作集回显进提示词。将刚运行过的命令作为 *"先验知识 —— 直接用它，不用重复发现"* 重新呈现，正是使弱模型陷入命令循环的原因，因此当前任务的命令会被从召回中过滤掉。

**持久化上下文** —— `persistent_context` 表（`ON CONFLICT DO UPDATE`）保存生命周期长于单个任务滑动窗口的记忆：源代码连接图、模块图、文档。源码分析子智能体通过 `[REMEMBER]` 建立并更新这些数据 —— "永不遗忘的代码绘图仪"。

---

## 技能 —— 声明式即时剧本 <a id="技能"></a>

把所有的 playbook 塞进提示词会摧毁模型的推理能力。`internal/skill` 是一个**声明式注册表**：每个技能都是一个 YAML 文件（`persona/v0.1/skills/*.yaml`），包含 `{name, description, triggers, tools, knowledge}`。运行期间，注册表根据目标和近期使用工具中出现的 `triggers`（触发器）数量对每个技能评分，仅将**相关性前 N 名**的技能剧本注入规划和单步提示词中 —— 一种轻量、确定性的本地 RAG 机制。技能**不引入执行路径**，它们只是让模型在选择运行哪些*沙箱*命令时更聪明。系统内置 `recon`、`web` 和 `source-analysis` 技能。只需拖入 YAML 文件即可添加技能 —— 零代码改动。

---

## 子智能体 —— 受限委派 <a id="子智能体"></a>

Yummi 支持分层委派。当编排器需要深入、专业的工作时，模型会发出 `[HANDOFF]{role, subgoal}`，且 `handoff` 节点将运行一个**专家子智能体** —— 该子智能体是相同的图状态机，但配置了专属的角色画像（persona）和受限的技能子集。

- **共享世界模型。** 子智能体共享编排器的 Store + 知识库，因而它们的发现和学到的战术会自动加入到统一的世界模型中。这就是为什么知识库必须先于一切进行设计的原因。
- **Token 隔离保护。** 子智能体运行它自己的循环，父节点只拿回一个干净的 `Summary`（且相关发现已写入共享知识中），而不是子智能体内部的完整交互轮次。
- **深度限制为 1。** 创建子智能体时传入了空子智能体映射表，因此它们从不见也从不接受 `[HANDOFF]` —— 绝无递归，绝无分叉炸弹（fork bombs）。
- **完全对等的授权。** 每个子智能体命令仍然流经唯一的执行路径 → 网关范围检查 → 沙箱。子智能体的授权与编排器完全等同，不能超越。

系统内置 `recon`、`web`、`source-analysis`、`reverser` 和 `source-analyzer` 角色（`persona/v0.1/subagents/*.yaml`）。放入 YAML 文件即可添加新角色。

---

## TRACE —— 智能体可观测性 <a id="trace-可观测性"></a>

智能体的智慧是**清晰可见**的。图引擎抛出类型化的 `ActivityEvent` —— `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish`。它们被持久化到 `agent_activity` 表中，并通过 `/ws/events` WebSocket 广播为 `agent.activity`。子智能体的事件会带上 `parent_task_id`，使其嵌套在所属的分流（handoff）跨度下。

- **网页版 AGENT 栏** —— 一个内联活动时间线：🧠 内存召回、📘 技能应用、🔎 发现、🤝 委派分流、💾 记住 —— 因此您能看到 *智能体正在利用什么资源*，而不只是纯文本。
- **网页版 TRACE 栏** —— 完整的可观测性交互面板：实时活动轨迹（嵌套子智能体跨度）以及可浏览的发现、学习战术、持久化上下文（代码图）、技能和角色。
- **读取 API** (只读): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`。

TUI 在终端上同样实时显示这些活动气泡芯片。

---

## 指挥官、验证器与出口模式 <a id="指挥官验证器与出口模式"></a>

- **conductor** (`internal/conductor`) —— 通过角色驱动的系统提示词直接与 Ollama 通信。**不存在基于规则的备用机制（fallback）**：如果 LLM 未配置或不可达，它将返回一个带有双语（土耳其语/英语，自动检测）解释的 `intent: decline`。回应是结构化的 JSON 意图：`suggest_command`、`start_task`、`explain`、`ask_clarification`、`decline`。
- **validator** —— **仅做风险分级**，不做网络范围决策。风险级别源自模型自身的 `risk_level` 加上保守的结构化底限（`classifyRisk` 仅在检测到 shell 链式操作符时才会提升到 `critical` 级别）。**不存在硬编码的工具白名单** —— 要教给智能体新工具，请编辑 `persona/v0.1/` 知识，而不是修改注册表。
- **出口（Egress）模式**（操作员设定，网关强力推行） —— 位于单 IP 范围列表之上的运行期三路总开关：
  - `block` (默认，故障关闭) —— 禁止外部出口流量。
  - `allow` —— 沙箱出口完全放开（域名直接工作，无需单 IP 解析）。
  - `ask` —— 每次产生新的外部目的连接时均需要操作员审批。
  - **此模式在网关重启后不会保留 —— 它会重置为 `block` (故障关闭)。**

---

## 网关 (Rust 信任边界) <a id="网关"></a>

内存安全的安全实施层 (`gateway/`，二进制文件：`yummi-gateway`):

- **`policy/`** —— `PolicyEngine`（带有 TTL 过期特性的 `RwLock<HashMap>`）；阻止（Block）规则永远优先于允许（Allow）规则；出口模式决定了判定结果 `verdict`（`allow` / `block` / `needs_approval`）。
- **`network/`** —— 仅支持 Linux/nftables 的控制器，管理 FORWARD 链（需要 `NET_ADMIN`）。可选的指纹屏蔽：`ttl_normalize`（防范被动式操作系统指纹识别）和 `mac_randomization`。
- **`ipc/`** —— Unix 域套接字，长度前缀的 JSON 帧构建 (`[4-byte BE length][JSON]`)。请求包括：`check_scope`、`get_status`、`ping`、`add_temporary_rule`、`remove_rule`、`set_egress_mode`、`list_rules`、`get_audit_log`。
- **`audit/`** —— SQLite WAL 审计日志，每一行通过 **SHA-256 形成哈希链** 连接到前一行（防篡改验证）。规则增删和出口模式的改变都会被记录。
- **`watchdog.rs`** —— 驻留内存（RSS）限制 + 心跳新鲜度；一旦发生越界，触发紧急封锁和守护进程退出。

Web 网关（GATEWAY）栏展示了系统状态、活跃规则、审计日志以及出口模式开关。

---

## 沙箱 <a id="沙箱"></a>

加固的 Kali 容器：只读根文件系统，以 `1000:1000` 身份运行，仅限 `NET_RAW`/`NET_ADMIN`，设置 `no-new-privileges`。DNS 强制流经网关，从而确保所有查询都会被记录日志。容器入口（entrypoint）仅仅等待网关套接字就绪然后执行 `sleep infinity` —— 每一个命令都是一次全新的 `docker exec`，而非持续存活的交互式 shell。

---

## 接口 —— Web UI 与 TUI <a id="接口"></a>

两个前端，**同一个 REST/WS API** —— 均不开启新的命令执行路径：

- **Web UI** (`ui/`, React 18 + Vite, nginx 运行在 `:3000`) —— 栏目包括：AGENT（聊天 + 实时活动时间线）、BASH（基于 WS 的交互式沙箱终端）、TRACE（可观测性 + 世界模型）、LOGS（日志）、GATEWAY（网关）、HEALTH（健康状态）。
- **TUI** (`internal/tui`, Charm Bubble Tea) —— 简约干净的终端操作员控制台：提供带有“建议 → 批准”流转的聊天、智能体任务追踪、以及通过 WebSocket 获取的实时活动时间线（显示技能/内存/发现/委派的图形芯片）。使用 `yummi --tui` 或 `make start-tui` 启动。

---

## 快速开始 <a id="快速开始"></a>

```bash
make install   # 引导构建开发环境 (Go, Rust, Node, Docker, Ollama)
make build     # 编译构建 CLI + 编排器 + 网关二进制文件 (UI 需单独构建)
make start     # 确保 Ollama 运行，然后以 docker compose 启动技术栈
# Web 仪表板访问 → http://localhost:3000

make start-tui # 启动服务并进入终端控制台界面
./bin/yummi --tui   # (如果后台服务已在运行)
```

首次运行 `make start` 会自动拉取默认模型 (`qwen3:8b`，约 4 GB)。

---

## 开发人员工作流 <a id="开发人员工作流"></a>

```bash
make build      # 构建 CLI + 编排器 + 网关 (不包含 UI —— UI 需要单独构建)
make test       # 运行所有测试集: CLI + 编排器 + 网关 (cargo) + UI (vitest)
make lint       # 在项目根目录和编排器模块下运行 golangci-lint
```

```bash
# 细粒度构建
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# 细粒度测试
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # 真实 qwen3:8b + 真实沙箱 e2e 联调 (如果沙箱挂起则自动跳过)

# 生命周期控制
make dev · dev-ui · dev-orchestrator     # 热重载开发服务器
make force-restart · close               # 重启 / 关闭所有服务并释放显存 (VRAM)
```

包含两个 Go 模块：**根目录** (`./go.mod`) 对应 CLI + TUI；**编排器** (`orchestrator/go.mod`) 对应 AI/API/执行的所有核心逻辑。`orchestrator/internal/*` 下的测试必须进入 `orchestrator/` 目录运行（独立的 go module）。网关使用 Rust，前端 UI 使用 vitest。**对 Yummi 自身组件不做 Mocking** —— 测试均直接驱动真实存储和引擎，仅对 LLM 边界进行了占位桩开发。

---

## 配置 <a id="配置"></a>

编排器环境变量参数：

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # 若为空，则指挥官拒绝一切提议 (无 fallback)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

预设等级（Tier）对应相应的模型 + 上下文窗口 + 思考模式组合：`lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (开发默认) → qwen3:8b。

**网络控制范围并非编排器的设置项。** 它完全配置在网关专属的 YAML 规则文件中 (`gateway/config/default.yaml` 下的 `scope.allow` / `scope.block`) —— 网关是判定网络范围的可信单一源。

---

## 项目目录布局 <a id="项目目录布局"></a>

```text
cmd/yummi/              CLI 入口点 (生成的二进制: yummi)
internal/
  cli/                  终端启动、--tui 连线及彩色输出控制
  tui/                  Charm Bubble Tea 终端控制台 (api · ws · app · render · styles)
  config/ · version/    配置加载器 · 构建期元数据
orchestrator/           独立的 Go 模块 (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     服务器后台程序二进制 (汇聚连接各个底层包)
  internal/
    agent/              图状态机 —— graph.go, node_*.go, protocol.go, activity.go 等
    knowledge/          世界模型 —— targets · findings · procedural · context · retrieval
    skill/              声明式技能注册中心
    conductor/          LLM 驱动器 · 验证器 · 审批管理 · 性能等级 · 角色画像
    executor/ execution/ 单一执行路径 · 执行队列存储
    api/                REST 路由接口 + 处理器 + WS 消息桥接
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   整合集成叶子包
gateway/                Rust 信任边界 (policy · ipc · network · audit · watchdog)
sandbox/                安全加固 Kali 容器镜像结构与启动配置
ui/                     React 18 + Vite 仪表板前端 (由运行在 :3000 的 nginx 负责承载)
persona/v0.1/           由文档定义的系统画像、skills/ 目录、subagents/ 目录
```

---

## 安全不变量 <a id="安全不变量"></a>

当您调整 Yummi 时，请恪守以下安全特性：

1. **默认故障关闭。** 针对作用域和 nftables 的任何改动必须坚守“除非明确允许，否则全部阻断”规则。网关必须在开启套接字之前安装 DROP 规则。
2. **唯一执行通道。** 所有渗透测试命令**必须**仅通过 `sandbox.Bridge.Exec` 运行到终端中。绝对不能从 Agent/LLM 的生成内容引入任何可到达的主机命令路径。(`internal/mcp` 可拉起主机端进程，因此其被永久休眠锁定 —— 生产代码绝不能导入它。)
3. **网关是作用域唯一的判定主体。** 编排器仅发出询问，其自身不作任何可达性判定。
4. **出口模式在网关重启后自动恢复为 `block`。**
5. **指挥官与执行器的完全解耦。** 指挥官知道 LLM、系统画像和风险评级 —— 从不了解命令如何落实运行。执行器深谙网关、沙箱以及底层运行逻辑 —— 从不理会聊天或者建议内容。

---

<div align="center">

使用 Go · Rust · React · Charm · Ollama 匠心打造。遵循 Conventional Commits 规范。

</div>
