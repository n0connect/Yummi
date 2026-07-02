<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">개인정보 보호 우선, 장애 시 차단(fail-closed) 방식의 AI 모의 해킹(Pentest) 실행 엔진.</p>

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
Yummi는 트래픽 제어 및 운영자 권한을 명시적으로 유지하면서 자연어를 실질적인 보안 워크플로우로 변환합니다. 자율적인 레드팀(Red-Team) 브레인이 강화되고 네트워크가 격리된 Kali 샌드박스를 구동하며, 메모리 안전한 Rust 신뢰 경계(trust boundary)를 통해 AI가 허용되지 않은 대상에 절대 접근할 수 없도록 보장합니다.
</p>

---

## 목차

- [Yummi 소개](#yummi-소개)
- [시스템 아키텍처](#시스템-아키텍처)
- [신뢰 경계 (가장 중요한 단일 불변 조건)](#신뢰-경계)
- [콘텐츠 레이어 보안](#콘텐츠-레이어-보안)
- [에이전트 엔진](#에이전트-엔진)
  - [그래프 상태 머신 — 노드와 엣지](#그래프-상태-머신)
  - [태그 프로토콜](#태그-프로토콜)
  - [브레인 독립적 코어](#브레인-독립적-코어)
- [지식 저장소 (Knowledge Store) — 2개 레이어 메모리 + 학습 루프](#지식-저장소)
- [스킬 (Skills) — 선언적 실시간 플레이북](#스킬)
- [서브 에이전트 — 제한된 위임](#서브-에이전트)
- [TRACE — 에이전트 관찰 가능성](#trace-관찰-가능성)
- [컨덕터, 검증기 및 이그레스 (Egress) 모드](#컨덕터-검증기-및-이그레스-모드)
- [게이트웨이 (Rust 신뢰 경계)](#게이트웨이)
- [샌드박스](#샌드박스)
- [인터페이스 — Web UI 및 TUI](#인터페이스)
- [빠른 시작](#빠른-시작)
- [개발자 워크플로우](#개발자-워크플로우)
- [설정](#설정)
- [저장소 구조](#저장소-구조)
- [보안 불변 조건](#보안-불변-조건)

---

## Yummi 소개 <a id="yummi-소개"></a>

Yummi는 *“AI가 제안하고, 게이트웨이가 처리한다”*라는 엄격한 규칙 아래 설계된 **4개의 컴포넌트와 1개의 대체 프론트엔드**로 구성됩니다. AI가 실행하는 모든 모의 해킹 명령은 반드시 한 곳을 통과하며, 크래시 발생 시에도 유지되는 장애 시 차단(fail-closed) 네트워크 정책에 의해 통제됩니다.

```
사용자 / 운영자  →  오케스트레이터 (Go)  →  게이트웨이 (Rust, IPC)  →  샌드박스 (Kali Docker)
  Web UI / TUI         컨덕터 + 에이전트       fail-closed nftables      도구 실행 전용
                       지식 저장소 + API      정책 엔진 + 감사
```

- **orchestrator** (Go) — AI 컨덕터, 에이전트 그래프 엔진, 지식 저장소 및 REST/WebSocket API를 소유합니다. nftables를 직접 제어하지 않습니다.
- **gateway** (Rust, 바이너리명 `yummi-gateway`) — 샌드박스로부터 외부 트래픽을 열 수 있는 유일한 컴포넌트입니다. 시작 시 기본 차단(`DROP`) nftables 규칙을 설치하며, 크래시가 발생하더라도 해당 규칙이 커널 내에 유지됩니다 (**fail-closed**).
- **sandbox** (Kali Docker) — 읽기 전용 파일 시스템, non-root 사용자, `NET_RAW`/`NET_ADMIN` 권한만 가집니다. 수동적 동작: 오케스트레이터가 샌드박스 내부로 `docker exec`를 실행할 때만 동작합니다.
- **ui** (React 18 + Vite) — `:3000` 포트에서 작동하는 웹 대시보드입니다. `/api/` 및 `/ws/`를 `:9080` 포트의 오케스트레이터로 프록시합니다.
- **TUI** (Charm Bubble Tea) — 동일한 REST/WS API와 통신하는 100% 터미널 기반의 운영자 콘솔(`yummi --tui`)입니다.

---

## 시스템 아키텍처 <a id="시스템-아키텍처"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            웹 대시보드      ·       터미널 TUI       │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ 오케스트레이터 · Go                                  │
        │                                                      │
        │   API ─▶ 컨덕터 ─▶ 에이전트 그래프 ─▶ 실행기         │
        │                          │                           │
        │                          ▼ (공유 세계 모델)          │
        │             지식 저장소: 발견 내용 · 메모리          │
        │                          · 컨텍스트 · TRACE          │
        └───────────────────────────┬──────────────────────────┘
                                    │  Unix 소켓 · 길이 접두사가 붙은 JSON IPC
        ┌───────────────────────────▼──────────────────────────┐
        │ 게이트웨이 · Rust 신뢰 경계                          │
        │ fail-closed nftables · 이그레스 · 감사 · 워치독      │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── 유일한 경로            │ 통제된 이그레스 (egress)
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ 샌드박스 · Kali         │      │  외부 대상           │
        │ 읽기 전용 · non-root    │      │  (게이트웨이 정책에  │
        │                         │      │   의해 결정됨)       │
        └─────────────────────────┘      └──────────────────────┘
```

신호는 운영자 → API → 컨덕터 → 에이전트 → 실행기 → 게이트웨이 → 샌드박스로 구성된 단일 뼈대를 따라 일직선으로 흐릅니다. `docker exec`는 게이트웨이 스코프 검증의 하류에 위치하여, 신뢰할 수 있는 영역에서 샌드박스로 들어갈 수 있는 **유일한 경로**입니다. 샌드박스는 *오직* 게이트웨이의 nftables 규칙을 통해서만 외부 세계와 통신합니다. 지식 저장소는 에이전트의 공유 세계 모델로서 연결되어 있습니다.

### 네트워크 토폴로지

```
yummi_internal (172.20.0.0/24): 샌드박스 .10 · 게이트웨이 .2 (DNS 겸용) · 오케스트레이터 .3 · ui .4 · docker-proxy .5
yummi_external:                 게이트웨이 · 오케스트레이터 · ui 전용 — 샌드박스는 의도적으로 제외되어 있습니다.
                                외부로 나가는 유일한 경로는 게이트웨이의 nftables 규칙뿐입니다.
호스트 포트: 3000 → ui nginx · 9080 → 오케스트레이터 API (localhost 전용)
```

샌드박스의 DNS는 게이트웨이(`172.20.0.2`)를 거치도록 강제되므로 모든 도메인 조회가 로그에 기록됩니다.

---

## 신뢰 경계 <a id="신뢰-경계"></a>

이것은 코드베이스에서 **가장 중요한 단일 불변 조건**입니다: *명시적으로 허용될 때까지 기본적으로 차단한다.*

- 게이트웨이는 IPC 소켓이 열리기 **전에** 모든 패킷을 폐기(`DROP`)하는 nftables 규칙을 설치합니다. 명시적인 `Allow` 규칙이 적용되기 전까지 샌드박스는 통신이 불가능합니다.
- 게이트웨이 프로세스가 종료되더라도 커널 내부의 `DROP` 규칙은 유지됩니다. 시스템은 **장애 시 차단(fail-closed)** 모드로 작동하며 절대 열리지 않습니다.
- **차단(Block) 규칙은 항상 허용(Allow) 규칙보다 우선합니다.**
- 모의 해킹 명령어가 셸에 도달할 수 있는 경로는 **오직 한 곳**입니다: `sandbox.Bridge.Exec` (`docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>` 실행). 에이전트가 직접 `exec.Command`를 호출하는 일은 절대 없습니다. 명령은 실행 저장소에 큐잉되며, 실행기가 이를 순차적으로 소비합니다 (게이트웨이 스코프 검증 → 샌드박스 실행). 에이전트/LLM의 출력 결과로부터 호스트 측 명령 실행 경로에 접근할 수 없습니다.

> 게이트웨이 모듈은 호스트 시스템이 아닌 Docker 내부에서 실행되며, 컨테이너 환경의 커널 지원을 활용하여 정책을 강제합니다. Docker 외부의 macOS 호스트에서 컴파일된 바이너리를 직접 실행할 경우 정책 적용에 필요한 Linux 커널이 존재하지 않게 됩니다.

---

## 콘텐츠 레이어 보안 <a id="콘텐츠-레이어-보안"></a>

게이트웨이는 훌륭한 **네트워크 레이어** 경계입니다. 하지만 모의 해킹 실행 엔진은 또 다른 미묘한 공격 표면을 마주하게 됩니다. 바로 LLM 프롬프트로 *다시 유입되는* **콘텐츠**입니다. 대상 컴퓨터의 명령 실행 결과, 스크래핑한 웹 페이지, MCP의 응답 등은 운영자가 작성한 것이 아닙니다. 이 콘텐츠에는 프롬프트 주입(prompt-injection, `ignore previous instructions…`), C2/promptware 명령, 데이터 유출 페이로드, 혹은 보이지 않는 유니코드 등이 포함되어 있을 수 있습니다. Yummi는 이 콘텐츠 레이어에 대해서도 동일한 fail-closed 방침을 확장 적용합니다.

```text
  도구 실행 결과 · 메모리 기록 · 스킬 파일                      (운영자 이외가 작성)
                       │
                       ▼
             ┌───────────────────┐   threatscan: 주입 · 유출 · C2
             │     threatscan    │               · 보이지 않는 유니코드 검출
             └─────────┬─────────┘
        도구 결과      │   메모리 기록 / 스킬 로드
    (실행 중 개입 불가)│   (사용자의 중재 필요)
               ▼                ▼
    ⚠ 경고 배너            ⛔ 엄격 스코프(strict)에서 차단
    "untrusted DATA"
               │
               ▼
          LLM 프롬프트
```

- **`internal/security/threatscan`** — 세 가지 스코프(`all` / `context` / `strict`)에서 스캔을 수행하는 리프(leaf) 패키지입니다 (표준 라이브러리만 의존). 도구 실행 결과는 **스캔을 거쳐 경고 배너가 부착됩니다** (탐지만 수행하며 차단하지는 않습니다. 운영자가 실행 도중에 개입하기 어렵기 때문입니다). `[REMEMBER]` 명령에 의한 기록이나 스킬 로드는 엄격 스코프에서 **차단**되어, 오염된 결과물이 세션을 가로지르는 영구적인 프롬프트 주입 공격을 심는 것을 방지합니다. 또한 결과물이 뇌에 도달하기 전에 **ANSI/C1 이스케이프 시퀀스**를 제거합니다.
- **메모리 출처 (Memory provenance)** — 감사 용도로 모든 영구 메모리 레코드에는 이를 작성한 주체(`agent` / `subagent`)가 함께 저장됩니다.
- **아키텍처 적합성 테스트** (`internal/security/invariants`) — 소스 트리를 탐색하며 감사된 셸 경계 파일 이외의 위치에서 `os/exec`가 사용되었거나, 프로덕션 코드에서 `internal/mcp`가 임포트되었거나 (휴면 잠금), 텔레메트리 엔드포인트가 혼입된 경우 **빌드를 실패시키는** 실행 가능한 가드입니다. 아래에 기술된 불변 조건들은 단순 도큐먼트가 아니라 실제 테스트 코드에 의해 강제됩니다.

---

## 에이전트 엔진 <a id="에이전트-엔진"></a>

Yummi의 에이전트는 **단순한 `while` 루프가 아닙니다**. 명시적인 **그래프 상태 머신**으로 구현되어 있습니다. LLM은 한 턴에 오직 하나의 태그 지정된 액션을 출력하고, 결정론적인 라우터가 고정된 엣지 테이블을 기반으로 타입 지정된 노드들을 탐색하며 최종 노드에 도달할 때까지 루프를 돕니다. 이 설계는 컨텍스트 윈도우 고갈이나 무한 루프 문제를 해결합니다. 매 단계마다 대화 기록의 흐름에 의존하기보다는, 전체 태스크 상태를 프롬프트 내에 재렌더링합니다.

### 그래프 상태 머신 — 노드와 엣지 <a id="그래프-상태-머신"></a>

엔진은 `orchestrator/internal/agent`에 살고 있습니다. `graph.go` 파일이 노드 세트와 라우팅 테이블인 `graphEdges` (`node → edge → nextNode`)를 관리하며, 각 노드는 개별 `node_*.go` 파일에 정의되어 있습니다.

**10가지 노드:**

| 노드 | 파일 | 역할 |
|------|------|----------------|
| `step` | `node_step.go` | LLM에 다음 액션을 요청합니다. 전체 태스크 상태 + 스킬 + 위임 정보 + 메모리를 프롬프트에 렌더링하고, 반환된 제어 태그에 따라 라우팅합니다. |
| `tool` | `node_tool.go` | 버퍼링된 명령어를 검증, 제한 및 실행합니다. 멈추거나 중복되는 패턴을 감지하고長時間 실행 명령어를 감시합니다. |
| `consult` | `node_consult.go` | 명령어 실행 도중 확인 (예: 장시간 실행 시) — LLM에 `[CONTINUE]`/`[ABORT]`를 물어봅니다. |
| `retry` | `retry.go` | 지수 백오프로 LLM/파싱 에러를 복구합니다. 호출한 노드로 되돌아가거나 포기합니다. |
| `summarize` | `node_lifecycle.go` | 컨텍스트 압축: 토큰 예산이 80% 이상 소비된 경우 관찰 기록을 요약으로 압축합니다. |
| `approval` | `node_lifecycle.go` | 운영자 승인 게이트 (채널 상에서 루프 goroutine을 블로킹합니다). |
| `finish` | `node_lifecycle.go` | 최종 노드: 완료 / 실패 / 중지. 모델이 요약을 생성하지 않은 경우 요약을 자체 합성합니다. |
| `ask` | `node_lifecycle.go` | 최종 노드: 일시 정지하고 운영자에게 질문합니다. |
| `handoff` | `node_handoff.go` | 특정 서브 태스크를 전문 서브 에이전트에게 위임하고 요약을 피드백 받습니다. |
| `remember` | `node_remember.go` | 구조화된 메모리(예: 코드 맵)를 지식 저장소에 영구 저장합니다. |

**이동 흐름** (`graph.go` 내의 `Execute`) — 작동하는 모든 노드는 `step`으로 복귀하므로 엔진 자체가 루프를 구성합니다. 최종 노드에 진입해야만 루프가 끝납니다:

```text
                     (모든 작동 노드는 STEP으로 루프백합니다)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ LLM에게 │ ◀───── PLAN_UPDATED ─────  │ exec │── 승인 필요 ──────────▶ approval ─▶ STEP
    │ 다음    │                            │  +   │── 장시간 실행 ────────▶ consult  ─▶ STEP
    │ 액션    │                            │ 가드 │── 고정 / 무한 루프 ────▶ consult  ─▶ STEP
    │ 요청    │                            └──────┘
    └────┬────┘
         │  finish · 예산 초과 ──▶ FINISH (최종)
         │  ask_operator · 중지 ──▶ ASK   (최종)
         │  llm_error ──▶ retry ──▶ (호출 노드로 복귀)
         │  summarize · remember · handoff ──▶ (STEP으로 복귀)
         ▼
    각 STEP은 생 대화 기록 대신 전체 태스크 상태 (계획 · 관찰 내역 ·
    스킬 · 메모리)를 프롬프트에 다시 렌더링합니다.
```

최종 노드(`finish`, `ask`)는 나가는 엣지가 없습니다. `Execute`는 이 노드들에 들어가면 종료됩니다. `retry`는 자신을 실행시켰던 노드로 동적 복귀합니다. 모든 전이는 체크포인트에 기록되므로 중단된 태스크는 마지막 상태의 노드부터 다시 시작됩니다.

**루프 탈출 제어는 다층적입니다** (`tool` 노드): 완전 동일 명령어 중복 감지 (`[DUPLICATE_BLOCKED]`), `A→B→A→B` 루프 패턴을 타겟하는 슬라이딩 윈도우식 스턱 감지기 (`[STUCK]`), 해시값 검증을 비껴가는 의미적 유사 중복을 잡는 **tool-thrash** 가드 — 예: `ps -ef` / `ps -ejH` / `ps aux` 패턴 감지 (`[ABNORMAL_PATTERN]`), 그리고 연속 실패 시 다른 방식을 강제하는 실패 에스컬레이션 (`[REPEATED_FAILURE]`) 후 운영자 질문 상태 진입. 무엇보다 절차적 메모리는 *이번 태스크*에서 이미 구동된 명령어를 과거 정보로 재호출하지 않으므로 학습 루프가 동일 명령 반복 상태를 학습하지 않습니다.

### 태그 프로토콜 <a id="the-tag-protocol"></a>

에이전트와 LLM은 **대괄호로 묶인 닫힌 어휘 집합** (`protocol.go` 내 `Registry`에 정의)으로 대화합니다. 이는 자유 텍스트나 XML이 아니며, 오직 한 행에 정의된 `[TAG]` 및 그에 따른 옵션 JSON 페이로드입니다. 레지스트리가 유일한 정보원이며, 프롬프트의 프로토콜 매뉴얼도 레지스트리 기반으로 자동 빌드됩니다.

```text
   에이전트 (step 노드)                                     LLM (뇌)
        │  전체 태스크 상태 + 스킬 · 메모리 렌더링                │
        │  및 프로토콜 매뉴얼 문서화 후 질의 ──────────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  검증 · 스턱/루프 체크 · 스코프 판단                │
        │  ──▶ 게이트웨이 범위 체크 ──▶ docker exec (샌드박스)  │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] 앞+뒤 잘라서 표시 ────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] 전체 출력 결과 제공 ──────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  최종 노드 — 모델이 요약을 누락한 경우 보고서 합성     │
```

소형 모델에서도 안정적인 작동을 위한 두 가지 메커니즘이 있습니다: 도구 출력은 뇌에 가 닿기 전에 **보안 스캔을 거치고 ANSI 문자가 필터링됩니다** ([콘텐츠 레이어 보안](#콘텐츠-레이어-보안) 참고). 또한 선두의 태그가 유실된 응답이 들어오면 **JSON 데이터 포맷 구조로부터 제어 태그를 역유추**하여 복구합니다 (`command` 필드 발견 → `RUN_COMMAND`, `role`+`subgoal` 발견 → `HANDOFF` 등).

**에이전트 → LLM (관찰 / 봉투 태그) — 결과를 돌려보내는 형식:**

| 태그 | 의미 |
|-----|---------|
| `[TOOL_RESPONSE]` | 명령 성공. 출력 결과는 다음과 같습니다. |
| `[TOOL_RESPONSE_TRUNCATED]` | 결과가 너무 깁니다. 앞/뒤 일부만 표시합니다. 전체가 필요하면 `[SEND_FULL]`로 답변하십시오. |
| `[TOOL_RESPONSE_FULL]` | `[SEND_FULL]` 요청에 따라 제공하는 전체 출력 결과입니다. |
| `[TOOL_FAILED]` | 명령은 돌았으나 에러가 났습니다. 반복하지 말고 다른 대안을 적용하십시오. |
| `[COMMAND_BLOCKED]` | 잘못된 포맷이거나 게이트웨이가 범위를 벗어난 타겟으로 판별하여 반려했습니다. |
| `[DUPLICATE_BLOCKED]` | 방금 실행한 명령어와 완전히 동일합니다. 다른 명령을 고르십시오. |
| `[REPEATED_FAILURE]` | 연속적인 명령 실패. 도구/경로 교체, 재계획 수립 또는 태스크 완료를 결정하십시오. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | 루프 혹은 고정된 패턴이 감지되었습니다. |
| `[LONG_RUNNING]` | 명령어가 기본 제한시간을 초과했습니다. `[CONTINUE]` 또는 `[ABORT]`로 답변하십시오. |
| `[OUTPUT_OVERFLOW]` | 터미널 아웃풋의 크기가 컨텍스트 윈도우를 압박합니다. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | 운영자가 승인이 필요한 명령어를 반려했거나 응답하지 않았습니다. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | 계획이 반영되었습니다 / 남은 예산(단계)이 부족합니다. |
| `[UNKNOWN]` | 에이전트가 상태를 분류하지 못했습니다. 직접 판단하십시오. |

**LLM → 에이전트 (제어 / 액션 태그) — 모델이 응답 시 붙이는 태그 (태그 뒤에 JSON 페이로드가 위치):**

| 태그 | Body | 효과 |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | 샌드박스 명령을 실행합니다. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | 계획을 개정합니다. |
| `[FINISH]` | `{summary, findings}` | 목표 도달. |
| `[HANDOFF]` | `{role, subgoal}` | 전문 서브 에이전트에게 하위 과제를 위임합니다. |
| `[REMEMBER]` | `{key, value}` | 키별 구조화 메모리를 영구 보관합니다 (망각되지 않음). |
| `[ASK_OPERATOR]` | `{question}` | 진행 불가 상태. 사람에게 직접 묻습니다. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | 전체 결과 재요구 / 대기 지속 / 명령 취소 / 태스크 중단. |

비교적 약한 로컬 모델은 선두 태그를 자주 생략하고 JSON만 보내곤 하는데, 파서가 페이로드 모양새를 보고 제어 태그(`command` 필드 → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF` 등)를 식별해주기 때문에 포맷 누락에도 문제없이 돌아갑니다.

### 브레인 독립적 코어 <a id="브레인-독립적-코어"></a>

LLM은 단일 메서드 인터페이스(`LLM { Chat(...) }`) 뒤에 위치한 **주입 및 교체 가능한 의존성**입니다. 코어는 오직 *품질과 동작성*에 맞추어 연계 설계되었으며, 특정 모델의 전유 기능에 기대지 않습니다. 로컬에서 가볍게 도는 `qwen3:8b` 모델을 기본 개발용 브레인 및 스트레스 테스트 장치로 쓰고 있습니다. 이 작고 투박한 브레인에서 안정적으로 돌면 더 큰 대형 브레인을 얹었을 때 훨씬 우수하게 작동합니다. 성능이 뛰어난 모델을 붙여도 코어 구조를 수정할 필요 없이 표현되는 깊이와 수준만 자동으로 *확장*됩니다.

---

## 지식 저장소 — 2개 레이어 메모리 + 학습 루프 <a id="지식-저장소"></a>

`internal/knowledge` 패키지는 최하단 리프(leaf)에 해당하며 (내부에서는 오직 `scope`에만 의존), **세계 모델의 단독 소유자**입니다. 에이전트, 실행기, API, 하위 에이전트 모두가 한 데이터 스토어에 기록하므로 결과물들이 파편화된 자유 텍스트가 아니라 구조화되고 질의 가능한 공유 데이터베이스로 통합됩니다.

**2개 메모리 레이어:**

1. **사실성 (Engagement별)** — `targets` 및 `findings` 테이블. 발견(findings) 데이터는 그 출처인 `source` (`manual` / `agent` / `subagent`) 및 태스크를 식별하는 `task_id`를 함께 저장합니다.
2. **절차적 (이식 가능, 대상 비식별화)** — `procedural_memory` 테이블. 실행된 모든 명령어는 *전술(tactic)*의 형태로 가공 및 기록됩니다: `{tool, command_pattern, outcome}`. 이때 대상 식별자(IP, 도메인 등)는 `scope.ExtractTarget`에 의해 리터럴 `<TARGET>` 플레이스홀더로 교체되어 **어떠한 대상 고유 식별 정보도 이식 레이어에 누출되지 않도록** 보호합니다. 완벽히 중복되는 전술은 `hit_count`로 합산 정렬됩니다. 실행 성공/실패(`worked` / `failed`)는 종료 코드를 기반으로 **기계적으로 분류**하므로 뇌의 의미 판단을 거치지 않아도 자체 학습을 돕습니다.

**학습 루프 흐름:**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ 에이전트가       │──▶│ 실행기가 비식별화된   │──▶│ procedural_memory │
    │ 명령을 실행      │   │ 전술을 저장           │   │ 도구·패턴·결과    │
    └──────────────────┘   └───────────────────────┘   │ ·누적 횟수        │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step 노드가  │◀─│ RetrievalStrategy 구현체가│◀────────┘
    │ 프롬프트에 MEMORY │  │ 최근 도구에 해당하는      │
    │ 블록을 주입       │  │ 전술을 조회               │
    └───────────────────┘  │ ‹이번 태스크에서 이미     │
                           │  수행된 명령어는 제외›    │
                           └───────────────────────────┘
```

지식 조회는 `RetrievalStrategy` 인터페이스 뒤에 격리되어 있습니다. 기본 공급되는 `StructuredRetrieval`은 현재 세션 및 최근 사용된 도구들을 조회 키로 삼는 결정론적 로컬 SQL 질의 구조를 가집니다. 추후 시맨틱/임베딩 검색기로 교체하더라도 **코어 패키지를 전혀 손대지 않고** 꽂아 넣을 수 있습니다.

조회 제외(EXCLUDES) 조건이 중요한데, 절차적 메모리는 **태스크 간 정보 공유**를 위한 것이지, 이번 태스크에서 방금 수행한 명령을 재호출하기 위한 것이 아닙니다. 방금 실행한 명령을 *"사전 지식 — 똑같이 써라"* 하고 올려버리면 능력이 부족한 모델은 반드시 명령 고착 루프에 걸리므로, 이번 태스크 소속 명령어들은 조회 필터에서 전부 제외합니다.

**지속성 컨텍스트** — `persistent_context` 테이블 (`ON CONFLICT DO UPDATE`)은 단일 태스크의 슬라이딩 윈도우를 관통해 오래 살아남는 데이터를 저장합니다: 소스코드 연결 맵, 모듈 의존성 그래프, 문서 등. 소스 분석 서브 에이전트가 `[REMEMBER]` (망각 방지 코드 매퍼) 지시를 내려 이 저장소를 갱신합니다.

---

## 스킬 — 선언적 실시간 플레이북 <a id="스킬"></a>

모든 시나리오 가이드를 프롬프트에 한 번에 쏟아부으면 모델의 추리 능력이 마비됩니다. `internal/skill`은 **선언적 레지스트리** 구조입니다: 각 스킬은 `{name, description, triggers, tools, knowledge}`가 적힌 YAML 파일들로 구성됩니다 (`persona/v0.1/skills/*.yaml`). 런타임에 레지스트리는 현재 과제 및 사용 도구에 해당 스킬의 `triggers`가 몇 개 부합하는지 실시간 연산하여, **가장 연관도 높은 상위 N개** 플레이북 정보만 꼽아 프롬프트에 병합하는 일종의 로컬 경량 RAG 장치입니다. 스킬은 **자체 실행 로직을 내장하지 않고**, 샌드박스에서 돌릴 도구를 뇌가 더 영리하게 결정할 수 있도록 지식만 보강해 줍니다. 기본으로 `recon`, `web`, `source-analysis` 스킬이 들어있으며 YAML만 넣으면 코드 수정 없이 스킬 확장이 가능합니다.

---

## 서브 에이전트 — 제한된 위임 <a id="서브-에이전트"></a>

Yummi는 계층식 태스크 위임을 지원합니다. 오케스트레이터가 심도 깊은 부분 조사를 필요로 할 때, 모델은 `[HANDOFF]{role, subgoal}` 지시를 내리고 `handoff` 노드가 **하위 전문 에이전트**를 실행합니다. 이 서브 에이전트는 독립된 페르소나 및 제한된 지식 세트만을 공급받는 동일한 상태 머신 엔진입니다.

- **공유 지식 데이터베이스.** 하위 에이전트도 오케스트레이터가 보유한 데이터 스토어 + 지식 저장소를 그대로 공유해 쓰므로 이들의 발견 정보 및 학습된 전술은 고스란히 단일 세계 모델에 축적됩니다. 지식 저장소를 가장 먼저 기본 설계한 이유가 바로 이 지점에 있습니다.
- **토큰 차폐.** 하위 에이전트는 자신만의 고유 루프를 돌며, 상위 부모 노드에는 지저분한 상세 대화 내역이 아닌 깔끔히 정리된 `Summary` (요약본) 정보와 데이터베이스에 축적된 발견 결과만 전달됩니다.
- **깊이 제한 1.** 하위 에이전트는 하위 위임 맵이 비어 있는 채로 초기화되어 추가적인 `[HANDOFF]` 지시를 이해하지도, 받지도 않습니다. 재귀적인 호출이나 포크 폭탄(fork bombs)이 원천 봉쇄됩니다.
- **동등 권한.** 하위 에이전트 명령어 역시 유일 실행 경로 → 게이트웨이 범위 확인 → 샌드박스로 구성되는 단일 라인을 통과하므로 오케스트레이터가 가진 권한을 초과할 수 없습니다.

`recon`, `web`, `source-analysis`, `reverser`, `source-analyzer` 역할 모델이 기본 탑재되어 있습니다 (`persona/v0.1/subagents/*.yaml`). YAML 파일을 더해 역할을 손쉽게 추가할 수 있습니다.

---

## TRACE — 에이전트 관찰 가능성 <a id="trace-관찰-가능성"></a>

에이전트의 사고 프로세스는 **일목요연하게 시각화**됩니다. 그래프 엔진은 `ActivityEvent` 타입 이벤트들(`plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish`)을 방출하고, `agent_activity` 테이블에 기록한 뒤 WebSocket `/ws/events` 채널을 통해 실시간 송출합니다. 서브 에이전트 이벤트에는 `parent_task_id`를 함께 붙여 위임 영역 아래 계층 구조로 표시됩니다.

- **웹 AGENT 탭** — 인라인 활동 타임라인: 🧠 떠올린 메모리, 📘 사용한 스킬, 🔎 찾은 정보, 🤝 위임 동작, 💾 기억한 정보. 텍스트 기록뿐만 아니라 에이전트가 *어떤 배경 지식을 꺼내 썼는지* 한눈에 봅니다.
- **웹 TRACE 탭** — 입체적 관찰 화면: 실시간 활동 흐름(서브 에이전트 영역 포함) 및 발견 정보, 학습된 전술, 지속성 컨텍스트(코드 맵), 스킬, 역할 모델 브라우징 기능을 제공합니다.
- **조회 전용 API**: `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`

TUI 콘솔도 동일한 활동 칩(chip)들을 터미널 화면에 실시간으로 깜빡여 보여줍니다.

---

## 컨덕터, 검증기 및 이그레스 모드 <a id="컨덕터-검증기-및-이그레스-모드"></a>

- **conductor** (`internal/conductor`) — 페르소나 지시가 담긴 시스템 프롬프트를 들고 Ollama와 직접 API 통신을 수행합니다. **규칙 기반 예외 대안(fallback) 처리는 배제**되어 있습니다: LLM이 설정되지 않았거나 통신 불가면 즉시 자동 감지된 언어팩(터키어/영어 중 택일)으로 해명과 함께 거절 의사(`intent: decline`)를 반환합니다. 응답 형식은 구조화된 JSON 기반 의도 규격입니다: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** — **오직 위험도 등급(risk)만 판단**하며 네트워크 범위 가부 결정은 맡지 않습니다. 등급 판정은 모델 자체 제안 값과 더불어 보수적인 셸 제어 연산자 감지식 기준치(`classifyRisk`는 파이프라인식 셸 체이닝 사용 시에만 `critical`로 상향시킴)에 따릅니다. **하드코딩된 특정 도구 허용 목록은 없습니다** — 신규 도구를 가르치려면 컴파일 코드 대신 `persona/v0.1/` 지식 파일을 가꾸면 됩니다.
- **이그레스 (Egress) 모드** (운영자 제어, 게이트웨이가 강제 집행) — IP별 범위 지정 리스트 상위에서 일괄 스위칭을 제공하는 3계통 마스터 스위치입니다:
  - `block` (기본값, fail-closed) — 외부 네트워크 완전 차단.
  - `allow` — 샌드박스 네트워크 무제한 허용 (도메인 lookup 시 개별 IP 해석 우회).
  - `ask` — 새로운 외부 접속 건마다 사람의 컨펌을 구함.
  - **이 상태는 게이트웨이 서비스 재시작 시 보존되지 않고 무조건 `block` (장애 시 차단) 상태로 초기화 복구됩니다.**

---

## 게이트웨이 <a id="게이트웨이"></a>

메모리 안전성 확보를 위한 Rust 구현 보안 게이트웨이 (`gateway/`, 바이너리명 `yummi-gateway`):

- **`policy/`** — `PolicyEngine` (TTL 자동 소멸형 `RwLock<HashMap>`). 차단 규칙이 언제나 허용 규칙보다 세며, 이그레스 모드 기반 판결 `verdict` (`allow` / `block` / `needs_approval`)을 최종 결정합니다.
- **`network/`** — FORWARD 필터를 전담 제어하는 Linux/nftables 컨트롤러 (`NET_ADMIN` 필요). 패시브 OS 지문 감지를 교란하는 `ttl_normalize` 설정 및 `mac_randomization` 기능을 지원합니다.
- **`ipc/`** — Unix 도메인 소켓 기반의 길이 접두사 규격 JSON 통신 구조 (`[4바이트 Big-Endian 길이][JSON]`). 지원 명령어: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** — SQLite WAL 규격 감사 로그. 위변조 검증을 위해 각 데이터 행은 직전 행 데이터와 **SHA-256 해시 체인으로 연쇄 묶음** 처리되어 있습니다. 규칙 증감 내역 및 모드 수정 일체가 남습니다.
- **`watchdog.rs`** — 프로세스 메모리(RSS) 크기 및 프로세스 생존 주기(heartbeat) 상태 감시. 한계 도달 시 즉각 비상 완전 차단을 단행하고 프로세스를 강제 중지합니다.

웹 UI의 GATEWAY 탭에서 현황, 적용 룰 목록, 감사 이력, 이그레스 모드 변경 스위치를 열어줍니다.

---

## 샌드박스 <a id="샌드박스"></a>

강화된 격리 Kali 컨테이너: 루트 파일 시스템은 읽기 전용으로 마운트되고, `1000:1000` 비루트 사용자로 작동하며, `NET_RAW`/`NET_ADMIN` 권한만 가지며, `no-new-privileges`가 기본 활성화됩니다. DNS 요청은 강제로 게이트웨이를 거치게 하여 기록이 투명하게 남습니다. 시작점(entrypoint) 스크립트는 단순히 게이트웨이 소켓만 보고 난 뒤 `sleep infinity`를 돕니다. 모든 명령어는 샌드박스 내부 상주 셸을 쓰지 않고 매번 일회성 `docker exec`로 신규 구동됩니다.

---

## 인터페이스 — Web UI 및 TUI <a id="인터페이스"></a>

두 인터페이스 모두 **내부적으로는 완전 동일한 REST/WS API**를 쓰며 독자적인 실행 우회로를 만들지 않습니다:

- **Web UI** (`ui/`, React 18 + Vite, nginx로 `:3000`에 호스팅) — 화면: AGENT (대화 기록 및 활동 타임라인), BASH (WebSocket 매개 터미널 접속), TRACE (관찰 도구 및 지식 베이스), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) — 미니멀하고 단정한 CLI 운영 도구: 대화/명령 제안 및 승인 흐름 처리, 과제 진척 모니터링, 실시간 활동 칩 피드백 수신 지원. `yummi --tui` 또는 `make start-tui` 명령어로 실행합니다.

---

## 빠른 시작 <a id="빠른-시작"></a>

```bash
make install   # 개발 장비 부트스트랩 (Go, Rust, Node, Docker, Ollama 설치 및 보장)
make build     # CLI + 오케스트레이터 + 게이트웨이 컴파일 빌드 (UI는 별도 빌드 필요)
make start     # Ollama 백엔드 핑을 확인한 뒤 docker compose 업
# 웹 브라우저 대시보드 → http://localhost:3000

make start-tui # 서비스를 띄운 뒤 터미널 TUI 콘솔 화면으로 즉시 연결
./bin/yummi --tui   # (이미 서비스가 구동 중인 경우 터미널만 붙을 때)
```

최초로 `make start`를 하면 동작 표준인 `qwen3:8b` 모델(약 4GB)을 자동으로 다운로드합니다.

---

## 개발자 워크플로우 <a id="개발자-워크플로우"></a>

```bash
make build      # CLI + 오케스트레이터 + 게이트웨이 빌드 (웹 UI 부분은 별도 빌드 필요)
make test       # 테스트 올인원: Go 테스트 + Rust 카고 테스트 + UI 비트테스트(vitest)
make lint       # 루트 디렉토리 및 orchestrator 패키지 대상 golangci-lint 수행
```

```bash
# 세부 분할 빌드
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# 세부 분할 테스트
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # qwen3:8b 및 실제 샌드박스를 연동한 E2E 검증 (샌드박스 비구동 시 패스)

# 관리용 유틸리티
make dev · dev-ui · dev-orchestrator     # 코드 수정 핫리로딩 개발 모드 구동
make force-restart · close               # 전원 내리기 / 정리하기 + 그래픽 메모리(VRAM) 회수
```

두 종류의 Go 모듈: **루트** (`./go.mod`) 디렉토리는 CLI + TUI 영역이며, **오케스트레이터** (`orchestrator/go.mod`) 영역은 모든 핵심 지식/API/실행엔진 패키지입니다. `orchestrator/internal/*` 이하 테스트를 수동으로 돌릴 때는 반드시 `orchestrator/` 패키지 안으로 이동해 실행해야 합니다. 게이트웨이는 Rust 구조이며 웹 UI는 vitest로 구축되어 있습니다. **시스템 주요 부품의 Mocking 처리는 배제**되어 있어, 테스트는 언제나 실제 로컬 엔진과 스토어를 가동하며 오직 LLM 연계 부분만 껍데기 스텁(stub)을 씁니다.

---

## 설정 <a id="설정"></a>

오케스트레이터 구동용 환경 변수 규격:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # 값 비어 있으면 오케스트레이터는 무조건 거절 (대안 없음)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Tier 세팅은 브레인 모델 + 컨텍스트 창 크기 + 추론 연산 수준을 일괄 매핑합니다: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (기본 모드) → qwen3:8b.

**네트워크 허용 범위 지정은 오케스트레이터의 소관이 아닙니다.** 오직 게이트웨이 독립 설정 파일인 `gateway/config/default.yaml` 내 `scope.allow` / `scope.block` 지정을 따르며 게이트웨이가 단일 신뢰 소스입니다.

---

## 저장소 구조 <a id="저장소-구조"></a>

```text
cmd/yummi/              CLI 메인 진입점 (빌드 결과물: yummi)
internal/
  cli/                  콘솔 기동 처리, --tui 플래그 바인딩, 콘솔 색상 제어
  tui/                  Bubble Tea 콘솔 패키지 (api · ws · app · render · styles)
  config/ · version/    시스템 설정 로더 · 빌드 메타데이터
orchestrator/           Go 서브 모듈 (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     서버 메인 바이너리 (패키지 총배선 영역)
  internal/
    agent/              그래프 상태 머신 — graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          세계 지식베이스 — targets · findings · procedural · context · retrieval
    skill/              선언적 스킬 레지스트리 패키지
    conductor/          LLM 질의 빌더 · 유효성 검증 · 승인 처리 · Tier · 페르소나
    executor/ execution/ 단일 실행 경로 · 태스크 큐 저장소
    api/                REST 라우팅 + WS 메신저 브릿지
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   기타 하위 통합 모듈
gateway/                Rust 게이트웨이 (policy · ipc · network · audit · watchdog)
sandbox/                하드닝 Kali 도커 이미지 및 초기 구동 구성
ui/                     React 18 + Vite 웹 앱 (:3000 포트 nginx 호스팅)
persona/v0.1/           문서 기반 페르소나, skills/, subagents/
```

---

## 보안 불변 조건 <a id="보안-불변-조건"></a>

Yummi 코드를 갱신할 때 이 핵심 가치들을 훼손하지 마십시오:

1. **장애 시 자동 잠금 (fail-closed).** 정책/필터 수정은 언제나 "명시적으로 열어주지 않은 대상은 차단" 규칙을 엄수합니다. 게이트웨이는 IPC 소켓을 물기 전에 nftables에 DROP 규칙을 박아 넣어야 합니다.
2. **단일 실행 보장.** 모의 해킹 명령어는 언제나 오직 `sandbox.Bridge.Exec`만을 타야 합니다. LLM 출력 내용이 호스트 측 명령 실행 통로로 곧장 뻗어나가는 우회로를 만들지 마십시오. (`internal/mcp`는 호스트 자식 프로세스를 기동하므로 휴면 차단되어 있으며 프로덕션에 절대 임포트해서는 안 됩니다.)
3. **게이트웨이가 단일 판단원.** 오케스트레이터는 게이트웨이에 질문을 보낼 뿐이며 스스로 네트워크 소통 가부를 판단하지 않습니다.
4. **이그레스 모드는 서비스 재부팅 시 `block`으로 복구됨.**
5. **질의 관리자(Conductor)와 실행 엔진의 분리.** Conductor는 LLM, 페르소나, 위험 점수만 알며 실행 상세를 모릅니다. 실행 엔진은 게이트웨이, 격리 환경, 큐 데이터만 보며 제안 채널의 실시간 대화 흐름을 모릅니다.

---

<div align="center">

Go · Rust · React · Charm · Ollama 기술로 가꾸었습니다. Conventional Commits 룰을 따릅니다.

</div>
