<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">Un director de pentesting con IA, fail-closed (seguro ante fallos) y con privacidad garantizada.</p>

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
Yummi convierte el lenguaje natural en flujos de trabajo de seguridad prácticos mientras mantiene explícitos el control del tráfico y la autoridad del operador. Un cerebro autónomo de Red-Team conduce un sandbox de Kali endurecido y aislado de la red, y un límite de confianza en Rust, seguro para la memoria, asegura que la IA nunca pueda tocar un objetivo para el que no esté autorizada.
</p>

---

## Tabla de contenidos

- [Qué es Yummi](#qué-es-yummi)
- [Arquitectura del sistema](#arquitectura-del-sistema)
- [El límite de confianza (el invariante más importante)](#el-límite-de-confianza)
- [Seguridad en la capa de contenido](#seguridad-en-la-capa-de-contenido)
- [El motor de agentes](#el-motor-de-agentes)
  - [Máquina de estados de grafo: nodos y aristas](#máquina-de-estados-de-grafo)
  - [El protocolo de etiquetas](#el-protocolo-de-etiquetas)
  - [Núcleo agnóstico del cerebro](#núcleo-agnóstico-del-cerebro)
- [El Almacén de Conocimiento (Knowledge Store): memoria de dos capas + bucle de aprendizaje](#el-almacén-de-conocimiento)
- [Habilidades (Skills): guías de ejecución declarativas just-in-time](#habilidades)
- [Subagentes: delegación acotada](#subagentes)
- [TRACE: observabilidad del agente](#trace-observabilidad)
- [El director, validador y modos de salida (egress)](#el-director-validador-y-modos-de-salida)
- [Pasarela (límite de confianza en Rust)](#pasarela)
- [Sandbox](#sandbox)
- [Interfaces: interfaz web y TUI](#interfaces)
- [Inicio rápido](#inicio-rápido)
- [Flujo de trabajo del desarrollador](#flujo-de-trabajo-del-desarrollador)
- [Configuración](#configuración)
- [Estructura del repositorio](#estructura-del-repositorio)
- [Invariantes de seguridad](#invariantes-de-seguridad)

---

## Qué es Yummi <a id="qué-es-yummi"></a>

Yummi consta de **cuatro componentes y una interfaz alternativa**, construidos en torno a una regla estricta: *la IA propone, la pasarela dispone.* Cada comando de pentesting que ejecuta la IA fluye a través de un único lugar y está regulado por una política de red de tipo fail-closed que sobrevive a las caídas del sistema.

```
Usuario / Operador  →  Orquestador (Go)  →  Pasarela (Rust, IPC)  →  Sandbox (Kali Docker)
  Web UI / TUI          director + agente      fail-closed nftables      ejecución de herramientas
                        conocimiento + API     motor de políticas + aud.
```

- **orchestrator** (Go): posee el director de IA, el motor de grafos de agentes, el almacén de conocimiento y la API REST/WebSocket. Nunca manipula nftables directamente.
- **gateway** (Rust, binario `yummi-gateway`): el único componente que puede abrir tráfico externo desde el sandbox. Installa reglas nftables de tipo `DROP`-all al iniciarse; si se cae, esas reglas persisten (**fail-closed**).
- **sandbox** (Kali Docker): sistema de archivos de solo lectura, usuario no root, solo permisos `NET_RAW`/`NET_ADMIN`. Pasivo: solo actúa cuando el orquestador ejecuta un comando `docker exec` en él.
- **ui** (React 18 + Vite): panel de control web en `:3000`, que redirige `/api/` y `/ws/` al orquestador en `:9080`.
- **TUI** (Charm Bubble Tea): consola de operador basada completamente en terminal (`yummi --tui`), que se comunica con la misma API REST/WS.

---

## Arquitectura del sistema <a id="arquitectura-del-sistema"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            Panel Web       ·       Terminal TUI      │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ ORQUESTADOR · Go                                     │
        │                                                      │
        │   API ─▶ director ─▶ grafo de agentes ─▶ ejecutor    │
        │                          │                           │
        │                          ▼  (modelo de mundo comp.)  │
        │        Almacén Conocimiento: hallazgos · memoria     │
        │                          · contexto · TRACE          │
        └───────────────────────────┬──────────────────────────┘
                                    │  Socket Unix · IPC JSON con prefijo de long.
        ┌───────────────────────────▼──────────────────────────┐
        │ PASARELA · Límite de confianza en Rust               │
        │ fail-closed nftables · salida · auditoría · watchdog │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── el ÚNICO camino        │ salida controlada
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ SANDBOX · Kali          │      │  Objetivos externos  │
        │ solo lectura · no-root  │      │  (según política de  │
        │                         │      │    la pasarela)      │
        └─────────────────────────┘      └──────────────────────┘
```

La señal fluye directamente hacia abajo por un único canal: operador → API → director → agente → ejecutor → pasarela → sandbox. `docker exec` es el **único camino** desde el plano confiable hacia el sandbox, después de la verificación de alcance de la pasarela; el sandbox se comunica con el mundo exterior *exclusivamente* a través de las reglas nftables de la pasarela. El Almacén de Conocimiento se conecta al agente como su modelo de mundo compartido.

### Topología de red

```
yummi_internal (172.20.0.0/24): sandbox .10 · pasarela .2 (también DNS) · orquestador .3 · ui .4 · docker-proxy .5
yummi_external:                 pasarela · orquestador · solo ui — el sandbox está ausente intencionadamente.
                                Su único camino de salida es a través de las reglas nftables de la pasarela.
Puertos del host: 3000 → ui nginx · 9080 → API del orquestador (solo localhost)
```

El DNS para el sandbox se fuerza a través de la pasarela (`172.20.0.2`), por lo que cada búsqueda se registra.

---

## El límite de confianza <a id="el-límite-de-confianza"></a>

Esta es la **invariante más importante** en el código base: *denegar hasta que se permita explícitamente.*

- La pasarela instala reglas nftables `DROP`-all **antes** de que se abra su socket IPC. El sandbox tiene conectividad cero hasta que se aplica una regla `Allow` explícita.
- Si el proceso de la pasarela muere, las reglas `DROP` permanecen en el kernel: el sistema falla en modo **cerrado (fail-closed)**, nunca abierto.
- **Las reglas de bloqueo siempre prevalecen sobre las reglas de permiso.**
- Los comandos de pentesting llegan a una shell a través de **exactamente un lugar**: `sandbox.Bridge.Exec`, que ejecuta `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`. El agente nunca llama directamente a `exec.Command` por sí mismo; solo los pone en la cola del almacén de ejecución, del cual el ejecutor los extrae (verificación de alcance de la pasarela → ejecución en el sandbox). Ninguna ruta de comandos del lado del host es accesible desde la salida del agente o LLM.

> El módulo de la pasarela se ejecuta dentro de Docker en lugar del sistema host, utilizando el soporte de núcleo del entorno de contenedores. La ejecución directa del binario compilado en el host macOS (fuera de Docker) carece del núcleo Linux necesario para aplicar las políticas.

---

## Seguridad en la capa de contenido <a id="seguridad-en-la-capa-de-contenido"></a>

La pasarela es una frontera de **capa de red** excelente. Sin embargo, un director de pentesting se enfrenta a una segunda superficie de ataque más sutil: el **contenido** que fluye de *regreso* al prompt de LLM. La salida de comando de un objetivo, una página web analizada, una respuesta de MCP; nada de esto es creado por el operador, y cualquiera de estos elementos puede contener una inyección de prompt (`ignore previous instructions…`), una directiva de C2/promptware, una carga útil de exfiltración o unicode invisible/bidi. Yummi extiende la misma postura fail-closed a esa capa de contenido.

```text
    salida de herramienta · escrituras de memoria · arch. de habilidades (NO creados por el op.)
                       │
                       ▼
             ┌───────────────────┐   threatscan: inyección · exfiltración · C2
             │     threatscan    │               · unicode invisible
             └─────────┬─────────┘
  salida de herr.      │   escritura de MEMORIA / carga de habilidades
  (no se puede inter-  │   (mediante el usuario)
  venir a mitad de ej) │
               ▼                ▼
    ⚠ banner de adv.      ⛔ bloqueo en alcance estricto
    "untrusted DATA"
               │
               ▼
          prompt de LLM
```

- **`internal/security/threatscan`**: un paquete hoja (solo biblioteca estándar) que escanea en tres alcances (`all` / `context` / `strict`). La salida de la herramienta se **escanea y se le añade un banner de advertencia** (detección, no bloqueo; el operador no puede intervenir en mitad del comando); las escrituras `[REMEMBER]` y las cargas de habilidades se **bloquean** en el alcance estricto, para que una salida maliciosa no pueda plantar una inyección de prompt persistente entre sesiones. También elimina las **secuencias de escape ANSI/C1** de la salida antes de que llegue al cerebro.
- **Procedencia de la memoria**: cada fila de memoria persistente registra quién la escribió (`agent` / `subagent`) para auditoría.
- **Pruebas de idoneidad arquitectónica** (`internal/security/invariants`): protectores ejecutables que recorren el árbol de fuentes y **hacen fallar la compilación** si aparece `os/exec` fuera de los archivos de límites de shell auditados, si `internal/mcp` es importado por código de producción (bloqueo de inactividad) o si se introduce un endpoint de telemetría. Las invariantes a continuación se aplican mediante pruebas, no solo se documentan.

---

## El motor de agentes <a id="el-motor-de-agentes"></a>

El agente de Yummi **no es un bucle `while` plano**. Es una **máquina de estados de grafo** explícita: el LLM emite una única acción etiquetada por turno, y un enrutador determinista recorre nodos tipados a través de una tabla de aristas fija hasta alcanzar un nodo terminal. Este diseño mitiga el agotamiento de la ventana de contexto y los bucles infinitos de acciones; cada paso vuelve a renderizar el estado completo de la tarea en el prompt en lugar de depender de un historial de conversación frágil.

### Máquina de estados de grafo: nodos y aristas <a id="máquina-de-estados-de-grafo"></a>

El motor reside en `orchestrator/internal/agent`. `graph.go` contiene el conjunto de nodos y la tabla de enrutamiento `graphEdges` (`node → edge → nextNode`); cada nodo vive en su propio archivo `node_*.go`.

**Los diez nodos:**

| Nodo | Archivo | Responsabilidad |
|------|------|----------------|
| `step` | `node_step.go` | Pide a la IA la siguiente acción; renderiza el estado completo de la tarea + HABILIDADES + DELEGACIÓN + MEMORIA en el prompt; enruta según la etiqueta de control devuelta. |
| `tool` | `node_tool.go` | Valida, filtra y ejecuta el comando almacenado en búfer; detecta patrones bloqueados/duplicados; supervisa comandos de larga duración. |
| `consult` | `node_consult.go` | Consulta a mitad de comando (p. ej., de larga duración): pregunta al LLM `[CONTINUE]`/`[ABORT]`. |
| `retry` | `retry.go` | Recuperación de errores de LLM/análisis con retroceso exponencial; vuelve a enrutar a su llamador o desiste. |
| `summarize` | `node_lifecycle.go` | Compactación del contexto: cuando se consume ≥80% del presupuesto de tokens, comprime el historial de observaciones en un resumen. |
| `approval` | `node_lifecycle.go` | Puerta de aprobación del operador (bloquea la goroutine del bucle en un canal). |
| `finish` | `node_lifecycle.go` | Terminal: completado / fallido / detenido; sintetiza un resumen si el modelo nunca produjo uno. |
| `ask` | `node_lifecycle.go` | Terminal: pausa y hace una pregunta al operador. |
| `handoff` | `node_handoff.go` | Delega una subtarea específica a un subagente especialista y devuelve su resumen. |
| `remember` | `node_remember.go` | Guarda una memoria estructurada (p. ej., un mapa de código) en el almacén de conocimiento. |

**El recorrido** (`Execute` en `graph.go`): cada nodo de trabajo vuelve a enrutar a `step`, por lo que el motor *es* el bucle; solo sale en un nodo terminal:

```text
                     (cada nodo de trabajo vuelve al paso STEP)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ pide a  │ ◀───── PLAN_UPDATED ─────  │ exec │── requiere aprobación ─▶ approval ─▶ STEP
    │ LLM la  │                            │  +   │── larga duración ──────▶ consult  ─▶ STEP
    │ sgte    │                            │guard.│── bloq. / bucle inf. ──▶ consult  ─▶ STEP
    │ acción  │                            └──────┘
    └────┬────┘
         │  finish · presupuesto agotado ──▶ FINISH  (terminal)
         │  ask_operator · detener ────────▶ ASK     (terminal)
         │  llm_error ──▶ retry ──▶ (vuelve al llamador)
         │  summarize · remember · handoff ──▶ (vuelve a STEP)
         ▼
    Cada STEP vuelve a renderizar el estado COMPLETO de la tarea (plan · obs ·
    HABILIDADES · MEMORIA) en el prompt, nunca el historial de chat sin procesar.
```

Los nodos terminales (`finish`, `ask`) no tienen aristas de salida; `Execute` se detiene cuando llega a ellos. `retry` enruta dinámicamente de regreso a cualquier nodo que lo haya llamado. Cada transición se registra en un punto de verificación (checkpoint), por lo que una tarea caída se reanuda desde su último nodo.

**La prevención de bucles es multicapa** (nodo `tool`): detección de huellas digitales idénticas (`[DUPLICATE_BLOCKED]`), un detector de bloqueos de ventana deslizante para ciclos `A→B→A→B` (`[STUCK]`), un protector de **tool-thrash** (fatiga de herramientas) que captura duplicados semánticos que la función hash no detecta (por ejemplo, alternar entre `ps -ef` / `ps -ejH` / `ps aux` (`[ABNORMAL_PATTERN]`)), y una escala de fallos consecutivos que fuerza un enfoque diferente (`[REPEATED_FAILURE]`) antes de detenerse y reportar al operador. Es fundamental que la memoria procedimental nunca recuerde un comando ya ejecutado en *esta tarea*, por lo que el bucle de aprendizaje no puede reforzar un bucle de repetición.

### El protocolo de etiquetas <a id="the-tag-protocol"></a>

El agente y el LLM se comunican con un **vocabulario cerrado y etiquetado con corchetes** (definido en el registro `Registry` de `protocol.go`). Esto *no* es texto libre ni XML: es un `[TAG]` en su propia línea, seguido opcionalmente de un cuerpo JSON. El registro es la única fuente de verdad; la referencia del protocolo del prompt se genera a partir de él.

```text
   AGENTE (nodo step)                                   LLM (cerebro)
        │  renderiza estado de tarea + HABILIDADES · MEMORIA   │
        │  + referencia de protocolo, luego pregunta ─────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  valida · verificación de bloqueo/fatiga · alc.    │
        │  ──▶ verificación alcance pasarela ──▶ docker exec │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] cabecera+pie ───────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] salida completa ─────────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  terminal — sintetiza informe si el modelo lo omitió│
```

Dos propiedades hacen que esto sea robusto en modelos *pequeños*: la salida de las herramientas se **escanea contra amenazas y se despoja de códigos ANSI** antes de llegar al cerebro (ver [Seguridad en la capa de contenido](#seguridad-en-la-capa-de-contenido)), y una respuesta a la que le falta su etiqueta inicial se recupera **infiriendo el control a partir de la estructura del JSON** (un campo `command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, …).

**Agente → LLM (etiquetas de observación/sobre): cómo el agente devuelve los resultados:**

| Etiqueta | Significado |
|-----|---------|
| `[TOOL_RESPONSE]` | El comando tuvo éxito; la salida sigue a continuación. |
| `[TOOL_RESPONSE_TRUNCATED]` | Salida demasiado larga; se muestra cabecera+pie. Responda `[SEND_FULL]` para ver todo. |
| `[TOOL_RESPONSE_FULL]` | La salida completa que solicitó mediante `[SEND_FULL]`. |
| `[TOOL_FAILED]` | El comando se ejecutó pero falló; adáptese, no repita. |
| `[COMMAND_BLOCKED]` | Formato incorrecto, o la pasarela rechazó el objetivo por estar fuera de alcance. |
| `[DUPLICATE_BLOCKED]` | Acaba de ejecutar exactamente este comando; elija uno diferente. |
| `[REPEATED_FAILURE]` | Varios fallos seguidos: cambie de herramienta/ruta, vuelva a planificar o finalice. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Repetición detectada. |
| `[LONG_RUNNING]` | Un comando excedió su límite blando: responda `[CONTINUE]` o `[ABORT]`. |
| `[OUTPUT_OVERFLOW]` | La salida del terminal está presionando la ventana de contexto. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | El operador denegó o no respondió al comando controlado. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plan aceptado / quedan pocos pasos. |
| `[UNKNOWN]` | El agente no pudo clasificar la situación: usted decide. |

**LLM → Agente (etiquetas de control/acción): con qué responde el modelo (una etiqueta y luego un cuerpo JSON):**

| Etiqueta | Cuerpo | Efecto |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Ejecuta un comando en el sandbox. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Revisa el plan. |
| `[FINISH]` | `{summary, findings}` | Objetivo alcanzado. |
| `[HANDOFF]` | `{role, subgoal}` | Delega una subtarea a un subagente especialista. |
| `[REMEMBER]` | `{key, value}` | Guarda una memoria estructurada bajo una clave (nunca se olvida). |
| `[ASK_OPERATOR]` | `{question}` | Bloqueado; necesita intervención humana. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Reenviar salida completa / seguir esperando / cancelar comando / detener tarea. |

Los modelos débiles a menudo omiten la etiqueta inicial y emiten solo el JSON: el analizador infiere la etiqueta de control a partir de la estructura de los datos (un campo `command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, etc.), por lo que el agente se mantiene robusto sin importar la precisión del formato del modelo.

### Núcleo agnóstico del cerebro <a id="núcleo-agnóstico-del-cerebro"></a>

El LLM es una **dependencia inyectada e intercambiable** detrás de una interfaz de un solo método (`LLM { Chat(...) }`). El núcleo está diseñado para ofrecer *calidad y cobertura*, no para las capacidades del modelo actualmente conectado. Un modelo local pequeño (qwen3:8b) es el cerebro de desarrollo y la prueba de esfuerzo de calidad; si el núcleo funciona bien con un cerebro pequeño, funcionará con uno grande. Conecte un cerebro más potente y el mismo núcleo simplemente *expandirá* su nivel; nunca tendrá que cambiar.

---

## El Almacén de Conocimiento: memoria de dos capas + bucle de aprendizaje <a id="el-almacén-de-conocimiento"></a>

`internal/knowledge` es un paquete hoja (no depende de nada interno excepto `scope`) que es el **propietario único del modelo del mundo**. El agente, el ejecutor, la API y todos los subagentes escriben en el mismo lugar, de modo que los descubrimientos del agente se convierten en conocimiento compartido, estructurado y consultable, en lugar de texto libre atrapado en una sola tarea.

**Dos capas de memoria:**

1. **Fáctica (por sesión)**: tablas `targets` y `findings`. Los hallazgos llevan procedencia: `source` (`manual` / `agent` / `subagent`) y el `task_id` que los produjo.
2. **Procedimental (portátil, depurada de objetivos)**: la tabla `procedural_memory`. Cada comando ejecutado se registra como una *táctica*: `{tool, command_pattern, outcome}`, donde el token del objetivo se reemplaza con el marcador de posición literal `<TARGET>` (a través de `scope.ExtractTarget`) para que **no se filtren datos específicos del objetivo** a la capa portátil. Las tácticas idénticas se unifican mediante un contador de aciertos (`hit_count`). El resultado (`worked` / `failed`) se obtiene **mecánicamente** a partir del código de salida, por lo que este bucle de aprendizaje ni siquiera depende del cerebro.

**El bucle de aprendizaje:**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ el agente ej. un │──▶│ el ejecutor registra  │──▶│ procedural_memory │
    │ comando          │   │ táctica sin objetivo  │   │ herram·patrón·    │
    └──────────────────┘   └───────────────────────┘   │ resultado·hits    │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step inyecta │◀─│ RetrievalStrategy lee las │◀────────┘
    │ un bloque MEMORY  │  │ tácticas para las herr.   │
    │ en el prompt      │  │ actuales                  │
    └───────────────────┘  │ ‹EXCLUYE comandos ya      │
                           │  ejecutados en ESTA tarea›│
                           └───────────────────────────┘
```

La recuperación se realiza a través de la interfaz `RetrievalStrategy`. La implementación provista, `StructuredRetrieval`, es SQL determinista indexado en la sesión actual y las herramientas de los comandos ejecutados recientemente: cero nuevas dependencias, completamente local. Una estrategia semántica o basada en embeddings puede añadirse más adelante **sin tocar el núcleo**.

El paso de exclusión (EXCLUDES) es fundamental: la memoria procedimental es para la **transferencia entre tareas**, nunca para reflejar el conjunto de trabajo de la tarea actual en el prompt. Presentar un comando recién ejecutado como *"conocimiento previo: úselo, no lo vuelva a descubrir"* es precisamente lo que empuja a un modelo débil a un bucle de comandos, por lo que los comandos de la tarea actual se filtran fuera del retorno.

**Contexto persistente**: la tabla `persistent_context` (`ON CONFLICT DO UPDATE`) almacena memoria de larga duración que sobrevive a la ventana deslizante de una sola tarea: mapas de conexión de código fuente, grafos de módulos, documentación. El subagente de análisis de código los construye y actualiza mediante `[REMEMBER]`: el "mapeador de código que nunca olvida".

---

## Habilidades: guías de ejecución declarativas just-in-time <a id="habilidades"></a>

Llenar el prompt con todas las guías de ejecución posibles destruye la capacidad de razonamiento. `internal/skill` es un **registro declarativo**: cada habilidad es un archivo YAML (`persona/v0.1/skills/*.yaml`) con `{name, description, triggers, tools, knowledge}`. En tiempo de ejecución, el registro califica cada habilidad según la cantidad de sus `triggers` (disparadores) que aparecen en el objetivo y en las herramientas utilizadas recientemente, e inyecta solo las **primeras N** guías de ejecución relevantes en los prompts de planificación y de pasos: un RAG local, determinista y ligero. Las habilidades **no introducen rutas de ejecución**; hacen que el cerebro sea más inteligente a la hora de decidir qué comandos del *sandbox* ejecutar. Viene con las habilidades `recon`, `web` y `source-analysis`. Para agregar una habilidad, basta con soltar un archivo YAML; no requiere cambios de código.

---

## Subagentes: delegación acotada <a id="subagentes"></a>

Yummi admite la delegación jerárquica. Cuando el orquestador necesita un trabajo profundo y especializado, el modelo emite `[HANDOFF]{role, subgoal}` y el nodo `handoff` ejecuta un **subagente especialista**: la misma máquina de estados de grafo, configurada con su propia identidad (persona) y un subconjunto delimitado de habilidades.

- **Modelo de mundo compartido**: los subagentes comparten el Almacén y el Almacén de Conocimiento del orquestador, por lo que sus hallazgos y tácticas aprendidas se unen automáticamente al modelo del mundo único. Esta es la razón exacta por la que el Almacén de Conocimiento se diseñó primero.
- **Protección de tokens**: un subagente ejecuta su propio bucle; el padre solo recibe de vuelta un `Summary` (resumen) limpio (y los hallazgos ya guardados en el conocimiento compartido), no los turnos internos del subagente.
- **Profundidad limitada a 1**: los subagentes se crean con un mapa de subagentes vacío, por lo que nunca ven ni aceptan `[HANDOFF]`: sin recursividad, sin bombas fork.
- **Misma autoridad**: cada comando de subagente fluye a través de la misma ruta de ejecución única → verificación de alcance de la pasarela → sandbox. Un subagente tiene exactamente la misma autoridad que el orquestador, ni más ni menos.

Viene con los roles `recon`, `web`, `source-analysis`, `reverser` y `source-analyzer` (`persona/v0.1/subagents/*.yaml`). Agregue un rol colocando un archivo YAML.

---

## TRACE: observabilidad del agente <a id="trace-observability"></a>

La inteligencia del agente se hace **legible**. El grafo emite eventos `ActivityEvent` tipados (`plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish`) guardados en una tabla `agent_activity` y transmitidos a través del WebSocket `/ws/events` como `agent.activity`. Los eventos de subagente llevan un `parent_task_id` para que se aniden bajo su bloque de transferencia.

- **Pestaña AGENTE en la web**: una línea de tiempo de actividad en línea: 🧠 memoria recordada, 📘 habilidad aplicada, 🔎 hallazgo, 🤝 transferencia, 💾 memorizado; para que vea *de qué se alimenta el agente*, no solo su texto.
- **Pestaña TRACE en la web**: interfaz completa de observabilidad: el seguimiento de actividad en vivo (bloques de subagentes anidados) más Hallazgos navegables, Tácticas aprendidas, Contexto persistente (mapas de código), Habilidades y Roles.
- **APIs de lectura** (solo lectura): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

La interfaz TUI presenta los mismos chips de actividad en vivo en la terminal.

---

## El director, validador y modos de salida <a id="el-director-validador-y-modos-de-salida"></a>

- **conductor** (`internal/conductor`): se comunica con Ollama directamente con un prompt de sistema orientado a la personalidad. **No hay una solución basada en reglas**: si el LLM no está configurado o no es accesible, devuelve un `intent: decline` con una explicación bilingüe (turco/inglés, detectada automáticamente). Las respuestas son intenciones JSON estructuradas: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator**: realiza **solo la clasificación de riesgos**, no decisiones de alcance de red. El riesgo proviene del propio `risk_level` del modelo más un suelo estructural conservador (`classifyRisk` solo escala a `critical` en operadores de encadenamiento de shell). **No hay una lista de herramientas permitidas rígida**: para enseñar al agente una nueva herramienta, edite el conocimiento en `persona/v0.1/`, no un registro de código.
- **Modo de salida (egress)** (establecido por el operador, impuesto por la pasarela): un interruptor maestro de 3 vías por encima de la lista de alcance de IP:
  - `block` (predeterminado, fail-closed): sin salida externa.
  - `allow`: la salida del sandbox se acepta completamente (los nombres de host funcionan sin resolución IP).
  - `ask`: cada nuevo destino externo necesita aprobación del operador.
  - **El modo no se mantiene tras reiniciar la pasarela: se restablece a `block` (fail-closed).**

---

## Pasarela <a id="pasarela"></a>

La capa de ejecución segura para la memoria (`gateway/`, binario `yummi-gateway`):

- **`policy/`**: el motor `PolicyEngine` (`RwLock<HashMap>` con expiración TTL); el bloqueo vence al permiso; el modo de salida resuelve un veredicto `verdict` (`allow` / `block` / `needs_approval`).
- **`network/`**: controlador exclusivo de Linux/nftables que gestiona la cadena FORWARD (necesita `NET_ADMIN`). Enmascaramiento opcional de huella digital: `ttl_normalize` (para evitar la detección pasiva de SO) y `mac_randomization`.
- **`ipc/`**: socket de dominio Unix, trama JSON con prefijo de longitud (`[4-byte BE length][JSON]`). Peticiones: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`**: registro SQLite WAL, cada fila **encadenada mediante hash SHA-256** a la anterior (evidencia manipulaciones). Se registran las adiciones/eliminaciones de reglas y los cambios en el modo de salida.
- **`watchdog.rs`**: límite RSS + frescura de latido; un incumplimiento activa el bloqueo de emergencia y la salida del demonio.

La pestaña GATEWAY en la web expone el estado, las reglas activas, el registro de auditoría y el interruptor de modo de salida.

---

## Sandbox <a id="sandbox"></a>

Un contenedor Kali endurecido: sistema de archivos raíz de solo lectura, se ejecuta como `1000:1000`, solo `NET_RAW`/`NET_ADMIN`, `no-new-privileges`. El DNS se fuerza a través de la pasarela para que se registren todas las búsquedas. El punto de entrada simplemente espera al socket de la pasarela y luego realiza un `sleep infinity`: cada comando es un `docker exec` nuevo, nunca una shell persistente.

---

## Interfaces: interfaz web y TUI <a id="interfaces"></a>

Dos interfaces, la **misma API REST/WS**: ninguna abre una nueva ruta de ejecución:

- **Web UI** (`ui/`, React 18 + Vite, nginx en `:3000`): pestañas: AGENT (chat + línea de tiempo de actividad en vivo), BASH (terminal interactiva del sandbox sobre WS), TRACE (observabilidad + modelo de mundo), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea): una consola de operador limpia y simple en terminal: chat con propuesta → aprobación, seguimiento de tareas del agente y la misma línea de tiempo de actividad en vivo (chips de habilidad/memoria/hallazgo/transferencia) sobre el WebSocket. Inicie con `yummi --tui` o `make start-tui`.

---

## Inicio rápido <a id="inicio-rápido"></a>

```bash
make install   # prepara el entorno de desarrollo (Go, Rust, Node, Docker, Ollama)
make build     # compila los binarios de la CLI + orquestador + pasarela (la UI se compila aparte)
make start     # asegura que Ollama esté corriendo y levanta el stack con docker compose
# Panel de control web → http://localhost:3000

make start-tui # inicia el stack y lanza la consola de terminal en su lugar
./bin/yummi --tui   # (si los servicios ya están activos)
```

El primer `make start` descarga el modelo predeterminado (`qwen3:8b`, ~4 GB).

---

## Flujo de trabajo del desarrollador <a id="flujo-de-trabajo-del-desarrollador"></a>

```bash
make build      # CLI + orquestador + pasarela (NO la UI, esa se construye por separado)
make test       # todas las pruebas: CLI + orquestador + pasarela (cargo) + UI (vitest)
make lint       # golangci-lint en la raíz del repositorio y en el módulo del orquestador
```

```bash
# Construcción granular
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Pruebas granulares
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # qwen3:8b real + sandbox real e2e (se omite si el sandbox está apagado)

# Ciclo de vida
make dev · dev-ui · dev-orchestrator     # servidores de desarrollo con recarga en caliente
make force-restart · close               # reinicia / detiene todo y libera memoria VRAM
```

Dos módulos de Go: la **raíz** (`./go.mod`) es la CLI + TUI; el **orquestador** (`orchestrator/go.mod`) es todo lo relacionado con IA/API/ejecución. Las pruebas bajo `orchestrator/internal/*` deben ejecutarse desde `orchestrator/` (un módulo separado). La pasarela es Rust; la interfaz de usuario utiliza vitest. **No hay simulaciones (mocks) de los componentes de Yummi**: las pruebas ejercitan almacenes/motores reales, simulando únicamente el límite con el LLM.

---

## Configuración <a id="configuración"></a>

Variables de entorno del orquestador:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # vacío = el conductor rechaza todo (sin fallback)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Los niveles preestablecidos (tiers) determinan el modelo, la ventana de contexto y el modo de pensamiento: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (predeterminado de dev) → qwen3:8b.

**El alcance de la red no es una configuración del orquestador.** Se configura exclusivamente en el propio YAML de la pasarela (`scope.allow` / `scope.block` en `gateway/config/default.yaml`); la pasarela es la única autoridad de alcance.

---

## Estructura del repositorio <a id="estructura-del-repositorio"></a>

```text
cmd/yummi/              Punto de entrada de la CLI (binario: yummi)
internal/
  cli/                  inicio de terminal, cableado de --tui, salida a color
  tui/                  consola Charm Bubble Tea (api · ws · app · render · estilos)
  config/ · version/    cargador de config. · metadatos de compilación
orchestrator/           módulo Go separado (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     binario del servidor (conecta todos los paquetes)
  internal/
    agent/              máquina de estados de grafo: graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          modelo del mundo: targets · findings · procedural · context · recuperación
    skill/              registro de habilidades declarativas
    conductor/          motor LLM · validador · aprobación · nivel · identidad (persona)
    executor/ execution/ ruta de ejecución única · almacén de ejecución
    api/                rutas REST + manejadores + puente WS
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   paquetes de integración hoja
gateway/                límite de confianza de Rust (policy · ipc · network · audit · watchdog)
sandbox/                imagen y punto de entrada del contenedor Kali endurecido
ui/                     React 18 + panel de control Vite (servido por nginx en :3000)
persona/v0.1/           identidad (persona) basada en documentos, skills/, subagents/
```

---

## Invariantes de seguridad <a id="invariantes-de-seguridad"></a>

Si modifica Yummi, conserve estas reglas:

1. **Fail-closed de forma predeterminada.** Los cambios en alcance/nftables deben mantener el principio de "denegar hasta que se permita explícitamente". La pasarela instala reglas DROP antes de abrir su socket.
2. **Una ruta de ejecución.** Los comandos de pentesting llegan a una shell únicamente a través de `sandbox.Bridge.Exec`. Nunca introduzca una ruta de comandos en el host accesible desde el agente o salida de LLM. (`internal/mcp` inicia un proceso del host y por lo tanto se mantiene inactivo; no importado en código de producción).
3. **La pasarela es la única autoridad de alcance.** El orquestador pregunta; nunca decide la accesibilidad por sí mismo.
4. **El modo de salida se restablece a `block` al reiniciar la pasarela.**
5. **Separación de director y ejecutor.** El director conoce el LLM, las identidades (personas), el riesgo; nunca cómo se ejecutan los comandos. El ejecutor conoce la pasarela, el sandbox, la ejecución; nunca el chat ni las propuestas.

---

<div align="center">

Construido con Go · Rust · React · Charm · Ollama. Conventional Commits.

</div>
