<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">Um maestro de pentest com IA focado em privacidade e fail-closed (seguro contra falhas).</p>

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
Yummi transforma linguagem natural em fluxos de trabalho práticos de segurança, mantendo explícitos o controle de tráfego e a autoridade do operador. Um cérebro autônomo de Red-Team comanda um sandbox do Kali endurecido e isolado da rede — e um limite de confiança em Rust, seguro para a memória, garante que a IA nunca possa tocar em um alvo para o qual não tenha permissão.
</p>

---

## Tabela de Conteúdos

- [O que é o Yummi](#o-que-é-o-yummi)
- [Arquitetura do sistema](#arquitetura-do-sistema)
- [O limite de confiança (o invariante mais importante)](#o-limite-de-confiança)
- [Segurança na camada de conteúdo](#segurança-na-camada-de-conteúdo)
- [O motor do agente](#o-motor-do-agente)
  - [Máquina de estados de grafo — nós & arestas](#máquina-de-estados-de-grafo)
  - [O protocolo de tags](#o-protocolo-de-tags)
  - [Core agnóstico ao cérebro](#core-agnóstico-ao-cérebro)
- [O Armazenamento de Conhecimento (Knowledge Store) — memória de duas camadas + loop de aprendizado](#o-armazenamento-de-conhecimento)
- [Skills — playbooks declarativos just-in-time](#skills)
- [Subagentes — delegação limitada](#subagentes)
- [TRACE — observabilidade do agente](#trace-observabilidade)
- [O condutor, validador & modos de egress (saída)](#o-condutor-validador-e-modos-de-egress)
- [Gateway (limite de confiança em Rust)](#gateway)
- [Sandbox](#sandbox)
- [Interfaces — Web UI & TUI](#interfaces)
- [Início rápido](#início-rápido)
- [Fluxo de trabalho do desenvolvedor](#fluxo-de-trabalho-do-desenvolvedor)
- [Configuração](#configuração)
- [Estrutura do repositório](#estrutura-do-repositório)
- [Invariantes de segurança](#invariantes-de-segurança)

---

## O que é o Yummi <a id="o-que-é-o-yummi"></a>

O Yummi é composto por **quatro componentes e um frontend alternativo**, construídos em torno de uma regra rígida: *a IA propõe, o gateway executa.* Cada comando de pentest executado pela IA flui através de exatamente um lugar e é regulado por uma política de rede fail-closed que sobrevive a falhas e crashes.

```
Usuário / Operador  →  Orquestrador (Go)  →  Gateway (Rust, IPC)  →  Sandbox (Kali Docker)
  Web UI / TUI          condutor + agente       fail-closed nftables      apenas execução de ferramentas
                        conhecimento + API      motor de políticas + aud.
```

- **orchestrator** (Go) — gerencia o condutor de IA, o motor de grafo de agentes, o armazenamento de conhecimento e a API REST/WebSocket. Nunca manipula o nftables diretamente.
- **gateway** (Rust, binário `yummi-gateway`) — o único componente capaz de abrir tráfego externo a partir do sandbox. Instala regras do nftables do tipo `DROP`-all durante a inicialização; se ele falhar, essas regras persistem no kernel (**fail-closed**).
- **sandbox** (Kali Docker) — sistema de arquivos somente leitura, usuário não-root, apenas permissões `NET_RAW`/`NET_ADMIN`. Passivo: só age quando o orquestador executa um comando `docker exec` dentro dele.
- **ui** (React 18 + Vite) — painel de controle web em `:3000`, que faz o proxy das rotas `/api/` e `/ws/` para o orquestrador em `:9080`.
- **TUI** (Charm Bubble Tea) — uma console de operador totalmente baseada em terminal (`yummi --tui`), comunicando-se com a mesma API REST/WS.

---

## Arquitetura do sistema <a id="arquitetura-do-sistema"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            Painel Web      ·       Terminal TUI      │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ ORQUESTRADOR · Go                                    │
        │                                                      │
        │   API ─▶ condutor ─▶ grafo do agente ─▶ executor     │
        │                          │                           │
        │                          ▼  (modelo de mundo comp.)  │
        │      Knowledge Store: descobertas · memória          │
        │                          · contexto · TRACE          │
        └───────────────────────────┬──────────────────────────┘
                                    │  Socket Unix · IPC JSON com prefixo de tamanho
        ┌───────────────────────────▼──────────────────────────┐
        │ GATEWAY · Limite de confiança em Rust                │
        │ fail-closed nftables · saída · auditoria · watchdog  │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── o ÚNICO caminho        │ saída controlada (egress)
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ SANDBOX · Kali          │      │  Alvos externos      │
        │ somente leitura ·       │      │  (conforme política  │
        │ não-root                │      │      do gateway)     │
        └─────────────────────────┘      └──────────────────────┘
```

O sinal corre diretamente por uma única espinha dorsal — operador → API → condutor → agente → executor → gateway → sandbox. `docker exec` é o **único caminho** do plano confiável para o sandbox, a jusante da verificação de escopo do gateway; o sandbox se comunica com o mundo exterior *exclusivamente* através das regras nftables do gateway. O Knowledge Store é associado ao agente como seu modelo de mundo compartilhado.

### Topologia de rede

```
yummi_internal (172.20.0.0/24): sandbox .10 · gateway .2 (também DNS) · orquestrador .3 · ui .4 · docker-proxy .5
yummi_external:                 gateway · orquestrador · apenas ui — o sandbox está intencionalmente ausente.
                                Seu único caminho para fora é por meio das regras nftables do gateway.
Portas do host: 3000 → ui nginx · 9080 → API do orquestrador (apenas localhost)
```

O DNS para o sandbox é forçado através do gateway (`172.20.0.2`), fazendo com que cada consulta seja registrada.

---

## O limite de confiança <a id="o-limite-de-confiança"></a>

Este é o **invariante individual mais importante** no codebase: *negar por padrão até que seja explicitamente permitido.*

- O gateway instala regras do nftables do tipo `DROP`-all **antes** que seu socket IPC seja aberto. O sandbox tem conectividade zero até que uma regra `Allow` explícita seja aplicada.
- Se o processo do gateway morrer, as regras `DROP` permanecem no kernel — o sistema opera em modo **fail-closed (seguro contra falhas)**, nunca aberto.
- **Regras de bloqueio (Block) sempre prevalecem sobre regras de permissão (Allow).**
- Os comandos de pentest chegam a um shell através de **exatamente um lugar**: `sandbox.Bridge.Exec`, que executa `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`. O agente nunca chama `exec.Command` diretamente por conta própria — ele apenas enfileira os comandos no armazenamento de execução, do qual o executor drena (verificação de escopo do gateway → execução no sandbox). Nenhum caminho de comando no lado do host é acessível a partir da saída do agente ou do LLM.

> O módulo gateway é executado dentro do Docker em vez do sistema host, utilizando o suporte de kernel do ambiente de contêineres. A execução direta do binario compilado no host macOS (fora do Docker) carece do kernel Linux necessário para aplicar as regras.

---

## Segurança na camada de conteúdo <a id="segurança-na-camada-de-conteúdo"></a>

O gateway é uma excelente fronteira na **camada de rede**. No entanto, um condutor de pentest enfrenta uma segunda superfície de ataque mais sutil: o **conteúdo** que flui de *volta* para o prompt do LLM. O retorno de comando de um alvo, uma página web raspada, uma resposta de MCP — nada disso é escrito pelo operador, e qualquer parte desses dados pode conter uma injeção de prompt (`ignore previous instructions…`), uma diretiva de C2/promptware, uma carga de exfiltração ou caracteres unicode invisíveis/bidi. O Yummi estende a mesma postura fail-closed a essa camada de conteúdo.

```text
    saída de ferramentas · escrituras de memória · arq. de skills (NÃO escritos pelo op.)
                       │
                       ▼
             ┌───────────────────┐   threatscan: injeção · exfiltração · C2
             │     threatscan    │               · unicode invisível
             └─────────┬─────────┘
      retorno de ferr. │   escrita de MEMÓRIA / carga de skills
     (sem intervenção  │   (intermediado pelo usuário)
       em execução)    │
               ▼                ▼
    ⚠ banner de aviso     ⛔ bloquear no escopo estrito
    "untrusted DATA"
               │
               ▼
         prompt do LLM
```

- **`internal/security/threatscan`** — um pacote folha (apenas biblioteca padrão) que escaneia dados em três escopos (`all` / `context` / `strict`). O retorno das ferramentas é **escaneado e sinalizado com um banner de aviso** (detecção, sem bloqueio — o operador não pode intervir no meio do comando); escrituras do tipo `[REMEMBER]` e carregamentos de skills são **bloqueados** no escopo estrito, para que uma saída maliciosa não possa plantar uma injeção de prompt persistente e entre sessões. Ele também remove **sequências de escape ANSI/C1** do retorno antes de chegar ao cérebro.
- **Proveniência da memória** — cada linha de memória persistente registra quem a escreveu (`agent` / `subagent`) para fins de auditoria.
- **Testes de adequação arquitetônica** (`internal/security/invariants`) — verificadores executáveis que varrem a árvore de arquivos fonte e **falham o build** se `os/exec` aparecer fora dos arquivos de fronteira de shell auditados, se `internal/mcp` for importado pelo código de produção (bloqueio de inatividade) ou se algum endpoint de telemetria for inserido. Os invariantes abaixo são aplicados por testes, não apenas documentados.

---

## O motor do agente <a id="o-motor-do-agente"></a>

O agente do Yummi **não é um loop `while` simples**. É uma **máquina de estados de grafo** explícita: o LLM emite uma única ação rotulada por turno, e um enrouteador determinista percorre nós tipados por meio de uma tabela de arestas fixa até que um nó terminal seja alcançado. Este design evita o esgotamento da janela de contexto e os loops infinitos de ações — cada passo renderiza novamente o estado completo da tarefa no prompt, em vez de depender de um histórico de conversação frágil.

### Máquina de estados de grafo — nós & arestas <a id="máquina-de-estados-de-grafo"></a>

O motor reside em `orchestrator/internal/agent`. O arquivo `graph.go` mantém o conjunto de nós e a tabela de roteamento `graphEdges` (`node → edge → nextNode`); cada nó vive em seu próprio arquivo `node_*.go`.

**Os dez nós:**

| Nó | Arquivo | Responsabilidade |
|------|------|----------------|
| `step` | `node_step.go` | Solicita ao LLM a próxima ação; renderiza o estado completo da tarefa + SKILLS + DELEGATION + MEMORY no prompt; roteia com base na tag de controle retornada. |
| `tool` | `node_tool.go` | Valida, filtra e executa o comando armazenado no buffer; detecta padrões bloqueados/duplicados; supervisiona comandos de longa execução. |
| `consult` | `node_consult.go` | Consulta no meio do comando (ex: longa execução) — pergunta ao LLM `[CONTINUE]`/`[ABORT]`. |
| `retry` | `retry.go` | Recuperação de erros de LLM/parsing com recuo exponencial (backoff); roteia de volta ao chamador original ou desiste. |
| `summarize` | `node_lifecycle.go` | Compactação de contexto: quando o orçamento de tokens é consumido em ≥80%, comprime o histórico de observações em um resumo. |
| `approval` | `node_lifecycle.go` | Portão de aprovação do operador (bloqueia a goroutine do loop em um canal). |
| `finish` | `node_lifecycle.go` | Terminal: concluído / falhou / parado; sintetiza um resumo caso o modelo não tenha gerado um. |
| `ask` | `node_lifecycle.go` | Terminal: pausa e faz uma pergunta ao operador. |
| `handoff` | `node_handoff.go` | Delega uma subtarefa focada a um subagente especialista e devolve seu resumo. |
| `remember` | `node_remember.go` | Persiste uma memória estruturada (ex: um mapa de código) no armazenamento de conhecimento. |

**O percurso** (`Execute` em `graph.go`) — cada nó ativo redireciona para `step`, de modo que o motor *é* o loop; ele só para ao atingir um nó terminal:

```text
                     (cada nó de trabalho volta para o STEP)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ solicita│ ◀───── PLAN_UPDATED ─────  │ exec │── requer aprovação ────▶ approval ─▶ STEP
    │ ao LLM  │                            │  +   │── longa execução ──────▶ consult  ─▶ STEP
    │ a próx. │                            │ guard│── travado / loop inf. ──▶ consult  ─▶ STEP
    │ ação    │                            └──────┘
    └────┬────┘
         │  finish · orçamento esgotado ──▶ FINISH  (terminal)
         │  ask_operator · parar ─────────▶ ASK     (terminal)
         │  llm_error ──▶ retry ──▶ (retorna ao chamador)
         │  summarize · remember · handoff ──▶ (retorna ao STEP)
         ▼
    Cada STEP renderiza novamente o estado COMPLETO da tarefa (plano · obs ·
    SKILLS · MEMÓRIA) no prompt — nunca o histórico de chat bruto.
```

Os nós terminais (`finish`, `ask`) não possuem arestas de saída — o `Execute` para quando os alcança. O `retry` roteia dinamicamente de volta para qualquer nó que o chamou. Cada transição é salva em um ponto de verificação (checkpoint), permitindo que uma tarefa interrompida continue do seu último nó.

**A prevenção de loops é feita em camadas** (nó `tool`): detecção de impressões digitais idênticas (`[DUPLICATE_BLOCKED]`), um detector de travamento por janela deslizante para ciclos `A→B→A→B` (`[STUCK]`), um protetor de **tool-thrash** (fadiga de ferramentas) que captura duplicados semânticos ignorados pela função hash (por exemplo, alternar entre `ps -ef` / `ps -ejH` / `ps aux` (`[ABNORMAL_PATTERN]`)), e uma escala de falhas consecutivas que força uma abordagem diferente (`[REPEATED_FAILURE]`) antes de parar e relatar ao operador. Crucialmente, a memória procedimental nunca lembra um comando já executado *nesta tarefa*, para que o loop de aprendizado não reforce um loop de repetição.

### O protocolo de tags <a id="the-tag-protocol"></a>

O agente e o LLM se comunicam usando um **vocabulário fechado e delimitado por colchetes** (definido na `Registry` de `protocol.go`). Isso *não* é texto livre e *não* é XML — é uma tag `[TAG]` isolada em sua linha, opcionalmente seguida por um corpo JSON. O registro é a única fonte da verdade; o manual do protocolo do prompt é gerado a partir dele.

```text
   AGENTE (nó step)                                     LLM (cérebro)
        │  renderiza estado da tarefa + SKILLS · MEMORY         │
        │  + manual do protocolo e depois pergunta ────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  valida · verificação de travamento/fadiga · escopo │
        │  ──▶ verif. escopo do gateway ──▶ docker exec      │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] início+fim exibidos ────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] saída completa ──────────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  terminal — sintetiza relatório se o modelo omitiu │
```

Duas propriedades tornam esse sistema robusto em modelos *pequenos*: o retorno das ferramentas é **escaneado contra ameaças e limpo de códigos ANSI** antes de chegar ao cérebro (veja [Segurança na camada de conteúdo](#segurança-na-camada-de-conteúdo)), e uma resposta sem a tag inicial é recuperada **deduzindo a intenção a partir do formato do JSON** (presença do campo `command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, …).

**Agente → LLM (tags de observação / envelope) — como o agente devolve os resultados:**

| Tag | Significado |
|-----|---------|
| `[TOOL_RESPONSE]` | O comando foi executado com sucesso; a saída segue abaixo. |
| `[TOOL_RESPONSE_TRUNCATED]` | Saída muito longa; início+fim exibidos. Responda `[SEND_FULL]` para obter tudo. |
| `[TOOL_RESPONSE_FULL]` | A saída completa que você solicitou via `[SEND_FULL]`. |
| `[TOOL_FAILED]` | O comando foi executado mas falhou — adapte os parâmetros, não repita. |
| `[COMMAND_BLOCKED]` | Formato incorreto ou o gateway rejeitou o alvo por estar fora do escopo. |
| `[DUPLICATE_BLOCKED]` | Você acabou de executar este comando idêntico — escolha um diferente. |
| `[REPEATED_FAILURE]` | Várias falhas consecutivas — alterne a ferramenta/rota, replaneje ou finalize. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Repetição de padrão detectada. |
| `[LONG_RUNNING]` | O comando excedeu seu limite flexível de tempo — responda `[CONTINUE]` ou `[ABORT]`. |
| `[OUTPUT_OVERFLOW]` | A saída do terminal está pressionando a janela de contexto. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | O operador recusou ou não respondeu ao comando controlado. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plano aceito / poucas etapas restantes. |
| `[UNKNOWN]` | O agente não conseguiu classificar a situação — você decide. |

**LLM → Agent (tags de controle / ação) — o que o modelo envia como resposta (uma tag, depois um corpo JSON):**

| Tag | Corpo | Efeito |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Executa um comando no sandbox. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Revisa o plano. |
| `[FINISH]` | `{summary, findings}` | Objetivo alcançado. |
| `[HANDOFF]` | `{role, subgoal}` | Delega uma subtarefa a um subagente especialista. |
| `[REMEMBER]` | `{key, value}` | Persiste uma informação estruturada sob uma chave (nunca é esquecida). |
| `[ASK_OPERATOR]` | `{question}` | Travado; precisa de intervenção humana. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Reenviar saída completa / continuar esperando / cancelar comando / parar tarefa. |

Modelos fracos geralmente omitem a tag inicial e emitem apenas o JSON — o analisador deduz a tag de controle a partir do formato da carga de dados (`command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, etc.), mantendo o agente robusto independentemente do quão mal o modelo siga a formatação.

### Core agnóstico ao cérebro <a id="core-agnóstico-ao-cérebro"></a>

O LLM é uma **dependência injetada e intercambiável** por trás de uma interface de método único (`LLM { Chat(...) }`). O núcleo é projetado para *qualidade e cobertura*, nunca para os recursos do modelo conectado atualmente. Um modelo local pequeno (qwen3:8b) é usado como cérebro de desenvolvimento e teste de carga — se o núcleo funciona de forma saudável em um cérebro pequeno, ele funcionará em um grande. Conecte um cérebro mais potente e o mesmo núcleo apenas *expandirá* seu nível de desempenho; ele nunca precisará ser alterado.

---

## O Armazenamento de Conhecimento — memória de duas camadas + loop de aprendizado <a id="o-armazenamento-de-conhecimento"></a>

O pacote `internal/knowledge` é um pacote folha (depende apenas do `scope` na camada interna) e é o **único proprietário do modelo do mundo**. O agente, executor, API e todos os subagentes escrevem no mesmo local — para que as descobertas do agente se tornem dados de conhecimento compartilhados, estruturados e consultáveis, em vez de texto livre preso em uma única tarefa.

**Duas camadas de memória:**

1. **Fática (por sessão)** — tabelas `targets` e `findings`. As descobertas carregam a proveniência: `source` (`manual` / `agent` / `subagent`) e o `task_id` que as produziu.
2. **Procedimental (portátil, limpa de alvos)** — a tabela `procedural_memory`. Cada comando executado é registrado como uma *tática*: `{tool, command_pattern, outcome}`, onde o endereço do alvo é substituído pelo marcador de posição `<TARGET>` (via `scope.ExtractTarget`) para que **nenhum dado específico do alvo vaze** para a camada portátil. Táticas idênticas se agrupam com um `hit_count`. O resultado (`worked` / `failed`) é derivado **mecanicamente** do código de saída — este loop de aprendizado nem sequer depende do cérebro.

**O loop de aprendizado:**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ agente executa   │──▶│ executor registra a   │──▶│ procedural_memory │
    │ um comando       │   │ tática sem alvo       │   │ ferr·padrão·      │
    └──────────────────┘   └───────────────────────┘   │ resultado·hits    │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step injeta  │◀─│ RetrievalStrategy lê as   │◀────────┘
    │ um bloco MEMORY   │  │ táticas para as           │
    │ no prompt         │  │ ferramentas atuais        │
    └───────────────────┘  │ ‹EXCLUI comandos já       │
                           │  executados NESTA tarefa› │
                           └───────────────────────────┘
```

A busca é gerenciada por meio de uma interface `RetrievalStrategy`. A implementação fornecida, `StructuredRetrieval`, é SQL determinista indexado na sessão atual e nas ferramentas de comandos executados recentemente — zero novas dependências, totalmente local. Uma estratégia semântica ou baseada em embeddings pode ser adicionada posteriormente **sem tocar no núcleo**.

A etapa de exclusão (EXCLUDES) é essencial: a memória procedimental é para a **transferência entre tarefas**, nunca para refletir os passos atuais no prompt. Apresentar um comando executado recentemente como *"conhecimento prévio — use-o, não o redescubra"* é exatamente o que empurra um modelo fraco para um loop de repetição, de modo que os comandos da tarefa atual são filtrados.

**Contexto persistente** — a tabela `persistent_context` (`ON CONFLICT DO UPDATE`) armazena memórias de longa duração que sobrevivem à janela deslizante de uma única tarefa: mapas de conexão de código-fonte, grafos de módulos, documentação. O subagente de análise de código os constrói e atualiza por meio de `[REMEMBER]` — o "mapeador de código que nunca esquece".

---

## Skills — playbooks declarativos just-in-time <a id="skills"></a>

Encher o prompt com todas as guias de execução destrói a capacidade de raciocínio da IA. O pacote `internal/skill` implementa um **registro declarativo**: cada habilidade é um arquivo YAML (`persona/v0.1/skills/*.yaml`) contendo `{name, description, triggers, tools, knowledge}`. Em tempo de execução, o registro avalia cada habilidade de acordo com a quantidade de gatilhos (`triggers`) que aparecem no objetivo e nas ferramentas usadas recentemente, inserindo apenas as **N primeiras** guias adequadas nos prompts de planejamento e etapas — um RAG local, previsível e leve. As habilidades **não introduzem rotas de execução**; elas apenas tornam o cérebro mais inteligente na escolha dos comandos a serem executados no *sandbox*. Vem com as habilidades `recon`, `web` e `source-analysis`. Adicione uma habilidade criando um arquivo YAML — nenhuma alteração de código é necessária.

---

## Subagentes — delegação limitada <a id="subagentes"></a>

O Yummi suporta delegação hierárquica. Quando o orquestrador precisa de um trabalho profundo e especializado, o modelo emite `[HANDOFF]{role, subgoal}` e o nó `handoff` executa um **subagente especialista** — a mesma máquina de estados de grafo, configurada com seu próprio perfil (persona) e um subconjunto restrito de habilidades.

- **Modelo de mundo compartilhado.** Os subagentes compartilham a base de dados e o armazenamento de conhecimento do orquestador, de modo que suas descobertas e táticas aprendidas são automaticamente integradas ao modelo de mundo único. Esta é a razão exata pela que o armazenamento de conhecimento foi projetado primeiro.
- **Isolamento de tokens.** Um subagente executa seu próprio loop; o pai recebe apenas uma versão limpa da `Summary` (resumo) (e as descobertas gravadas na base comum), ignorando todos os passos intermediários do subagente.
- **Profundidade estritamente limitada a 1.** Os subagentes são instanciados com um mapa de subagentes vazio, por isso eles nunca verão ou aceitarão comandos do tipo `[HANDOFF]` — recursão e bombas fork estão excluídas.
- **Mesmo nível de autoridade.** Cada comando de um subagente ainda passa pelo mesmo caminho de execução único → verificação de escopo do gateway → sandbox. Um subagente possui exatamente os direitos do orquestrador, nem mais, nem menos.

Fornecido com as funções `recon`, `web`, `source-analysis`, `reverser` e `source-analyzer` (`persona/v0.1/subagents/*.yaml`). Adicione um papel dropando um arquivo YAML.

---

## TRACE — observabilidade do agente <a id="trace-observability"></a>

Tornamos o processo de raciocínio do agente **legível e transparente**. O grafo gera eventos do tipo `ActivityEvent` — `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish` — salvos na tabela `agent_activity` e transmitidos via WebSocket `/ws/events` como `agent.activity`. Os eventos dos subagentes contêm `parent_task_id`, permitindo o aninhamento correto sob a tarefa pai.

- **Aba AGENTE na Web** — uma linha do tempo integrada de atividades: 🧠 memória recuperada, 📘 habilidade aplicada, 🔎 descoberta, 🤝 delegação de tarefa, 💾 salvo na memória — você vê *em quais fontes o agente se baseou*, e não apenas o texto de resposta.
- **Aba TRACE na Web** — painel completo de observabilidade: o fluxo de etapas ao vivo (com subagentes aninhados) e busca em descobertas, táticas aprendidas, contexto persistente (mapas de código), habilidades e papéis.
- **APIs de leitura** (read-only): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

A TUI exibe esses mesmos blocos de atividade em tempo real diretamente na console.

---

## O condutor, validador e modos de saída <a id="o-condutor-validador-e-modos-de-egress"></a>

- **conductor** (`internal/conductor`) — comunica-se diretamente com o Ollama, usando um prompt de sistema orientado ao perfil da persona. **Não há tratamento baseado em regras para falhas (fallback)**: se o LLM não estiver configurado ou estiver inacessível, ele retornará `intent: decline` com uma explicação bilíngue (turco/inglês, autodetectada). As respostas são intenções estruturadas em JSON: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** — realiza **apenas a classificação de riscos**, não decisões de escopo de rede. O risco provém do próprio `risk_level` do modelo mais um limite estrutural conservador (`classifyRisk` só escala para `critical` no uso de operadores de encadeamento de shell). **Não há uma lista de ferramentas permitidas rígida** — para ensinar uma nova ferramenta ao agente, edite os arquivos de conhecimento sob `persona/v0.1/`, não um registro de código.
- **Modo de saída (egress)** (definido pelo operador, imposto pelo gateway) — um interruptor mestre de 3 vias acima dos limites de IP permitidos:
  - `block` (padrão, fail-closed) — saída de rede externa totalmente bloqueada.
  - `allow` — saída do sandbox é irrestrita (nomes de host funcionam sem resolução de IP individual).
  - `ask` — cada nova tentativa de acesso externo precisa de confirmação humana.
  - **O modo não persiste ao reiniciar o gateway — ele é resetado para `block` (fail-closed).**

---

## Gateway <a id="gateway"></a>

Camada de execução segura para a memória desenvolvida em Rust (`gateway/`, binário `yummi-gateway`):

- **`policy/`** — `PolicyEngine` (estrutura `RwLock<HashMap>` com tempo de expiração TTL); regras de bloqueio têm prioridade sobre permissões; modo egress decide a decisão final `verdict` (`allow` / `block` / `needs_approval`).
- **`network/`** — controlador Linux/nftables exclusivo que gerencia a cadeia FORWARD (requer `NET_ADMIN`). Ocultação opcional de impressões digitais: normalização de TTL (`ttl_normalize` para impedir a detecção passiva do SO) e randomização de MAC.
- **`ipc/`** — Unix domain socket, JSON-framing com prefixo de comprimento (`[4-byte BE length][JSON]`). Comandos suportados: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** — registro SQLite WAL de auditoria, cada linha **encadeada via hash SHA-256 à anterior** (proteção contra adulteração). Grava qualquer alteração de regras e troca de modo egress.
- **`watchdog.rs`** — limite de consumo de memória (RSS) + monitoramento de atividade do processo; qualquer violação dispara bloqueio emergencial total e o encerramento do daemon.

A aba GATEWAY na Web expõe o status, regras ativas, histórico de auditoria e a chave de modo de saída.

---

## Sandbox <a id="sandbox"></a>

Contêiner Kali endurecido: sistema de arquivos raiz montado somente leitura, executado como usuário `1000:1000`, somente privilégios `NET_RAW`/`NET_ADMIN` e flag `no-new-privileges` ativa. As requisições DNS são forçadas através do gateway para registro. O ponto de entrada (entrypoint) simplesmente aguarda o socket do gateway e entra em `sleep infinity` — cada comando é executado como um novo `docker exec`, e não em uma shell persistente.

---

## Interfaces — Web UI e TUI <a id="interfaces"></a>

Duas interfaces visuais funcionam **pelo mesmo API REST/WS** — nenhuma possui caminhos alternativos de execução de comandos:

- **Web UI** (`ui/`, React 18 + Vite, nginx na porta `:3000`) — abas: AGENT (chat + linha do tempo de atividades), BASH (terminal interativo por WebSocket), TRACE (observabilidade e modelo do mundo), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) — console de operador em terminal: chat com proposta e aprovação de comandos, monitoramento de progresso de tarefas e linha do tempo de atividades (skills, memórias, descobertas, subagentes) por WebSocket. Execute via `yummi --tui` ou `make start-tui`.

---

## Início rápido <a id="início-rápido"></a>

```bash
make install   # bootstrapping do ambiente de desenvolvimento (Go, Rust, Node, Docker, Ollama)
make build     # compilação do CLI + orquestrador + gateway (a UI é compilada separadamente)
make start     # garante o funcionamento do Ollama e inicia o stack via docker compose
# Painel web → http://localhost:3000

make start-tui # inicia os serviços e abre o console TUI no terminal
./bin/yummi --tui   # (caso os serviços já estejam rodando em segundo plano)
```

A primeira execução de `make start` baixa o modelo padrão (`qwen3:8b`, ~4 GB).

---

## Fluxo de trabalho do desenvolvedor <a id="fluxo-de-trabalho-do-desenvolvedor"></a>

```bash
make build      # CLI + orquestrador + gateway (SEM construir a UI)
make test       # todos os testes: Go (CLI + orquestrador) + Rust (gateway) + UI (vitest)
make lint       # executa golangci-lint na raiz do repositório e no módulo do orquestrador
```

```bash
# Construção dividida
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Execução de testes dividida
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # testes e2e com o modelo qwen3:8b real + sandbox (ignorado se o sandbox estiver desligado)

# Gerenciamento de processos
make dev · dev-ui · dev-orchestrator     # executa servidores dev com hot-reload
make force-restart · close               # reinicia / desliga todo o stack e libera a VRAM
```

Dois módulos Go: o **raiz** (`./go.mod`) contém o CLI + TUI; o módulo do **orquestrador** (`orchestrator/go.mod`) contém toda a lógica de IA, API e execução. Testes no pacote `orchestrator/internal/*` devem ser executados a partir de `orchestrator/` (um módulo Go separado). O gateway é Rust; a interface está coberta por testes no vitest. **Mocks de componentes do próprio Yummi são excluídos** — os testes exercitam bases de dados e motores reais, substituindo apenas a comunicação com o LLM por stubs.

---

## Configuração <a id="configuração"></a>

Variáveis de ambiente do orquestador:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # vazio = condutor rejeita tudo (sem fallback)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Presets do tier associam um modelo + janela de contexto + modo de raciocínio: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (padrão de desenvolvimento) → qwen3:8b.

**Acesso de rede não é configurado no orquestador.** É configurado exclusivamente no YAML do próprio gateway (`scope.allow` / `scope.block` em `gateway/config/default.yaml`) — o gateway é a única fonte da verdade para o acesso de rede.

---

## Estrutura do repositório <a id="estrutura-do-repositório"></a>

```text
cmd/yummi/              Ponto de entrada do CLI (binário compilado: yummi)
internal/
  cli/                  inicialização no terminal, associação do --tui, controle de cores
  tui/                  console Charm Bubble Tea (api · ws · app · render · estilos)
  config/ · version/    carregador de configuração · metadados de compilação
orchestrator/           módulo Go separado (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     binário do servidor (inicializa e conecta todos os pacotes)
  internal/
    agent/              máquina de estados de grafo — graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          modelo do mundo — targets · findings · procedural · context · retrieval
    skill/              registro declarativo de habilidades
    conductor/          motor LLM · validador de riscos · aprovação · tiers · personas
    executor/ execution/ caminho de execução único · fila de execução
    api/                rotas REST + manipuladores + ponte WebSocket
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   pacotes de integração
gateway/                limite de segurança no Rust (policy · ipc · network · audit · watchdog)
sandbox/                imagem e ponto de entrada do contêiner Kali endurecido
ui/                     React 18 + Vite painel de controle (nginx na porta :3000)
persona/v0.1/           perfis baseados em documentos, habilidades skills/, subagentes subagents/
```

---

## Invariantes de segurança <a id="invariantes-de-segurança"></a>

Se você fizer alterações no Yummi, mantenha estas regras:

1. **Fail-closed por padrão.** Quaisquer alterações de escopo e nftables devem manter a regra "negar por padrão até que seja explicitamente permitido". O gateway instala regras DROP antes de abrir seu socket.
2. **Único canal de execução.** Comandos de pentest chegam ao contêiner somente através de `sandbox.Bridge.Exec`. Nunca introduza caminhos de execução do lado do host acessíveis a partir do agente ou saída de LLM. (O `internal/mcp` inicia processos do host e por isso é mantido inativo — é proibido importá-lo no código de produção.)
3. **O gateway é a única autoridade de acesso.** O orquestrador pergunta; ele nunca toma essa decisão sozinho.
4. **O modo egress é resetado para `block` ao reiniciar o gateway.**
5. **Separação de condutor e executor.** O condutor sabe tudo sobre LLM, personas e riscos, mas nada sobre como os comandos são executados. O executor sabe tudo sobre o gateway, sandbox e execução, mas ignora conversas ou propostas do modelo.

---

<div align="center">

Desenvolvido com Go · Rust · React · Charm · Ollama. Padrão Conventional Commits.

</div>
