<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">Ein datenschutzfreundlicher, im Fehlerfall schließender (fail-closed) KI-Pentest-Dirigent.</p>

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
Yummi verwandelt natürliche Sprache in praktische Sicherheits-Workflows, während die Verkehrssteuerung und die Betreiberautorität explizit bleiben. Ein autonomes Red-Team-Gehirn steuert eine gehärtete, netzwerkisolierte Kali-Sandbox – und eine speichersichere Rust-Vertrauensgrenze stellt sicher, dass die KI niemals ein Ziel berühren kann, für das sie keine Berechtigung hat.
</p>

---

## Inhaltsverzeichnis

- [Was Yummi ist](#was-yummi-ist)
- [Systemarchitektur](#systemarchitektur)
- [Die Vertrauensgrenze (die wichtigste Invariante)](#die-vertrauensgrenze)
- [Sicherheit auf Inhaltsebene](#sicherheit-auf-inhaltsebene)
- [Die Agenten-Engine](#die-agenten-engine)
  - [Graph-Zustandsautomat – Knoten & Kanten](#graph-zustandsautomat)
  - [Das Tag-Protokoll](#das-tag-protokoll)
  - [Gehirn-agnostischer Kern](#gehirn-agnostischer-kern)
- [Der Wissensspeicher (Knowledge Store) – zweischichtiger Speicher + Lernschleife](#der-wissensspeicher)
- [Skills – deklarative Just-in-Time-Playbooks](#skills)
- [Sub-Agenten – begrenzte Delegation](#sub-agenten)
- [TRACE – Agenten-Beobachtbarkeit](#trace-beobachtbarkeit)
- [Dirigent, Validator & Egress-Modi](#dirigent-validator-und-egress-modi)
- [Gateway (Rust-Vertrauensgrenze)](#gateway)
- [Sandbox](#sandbox)
- [Schnittstellen – Web-UI & TUI](#schnittstellen)
- [Schnellstart](#schnellstart)
- [Entwickler-Workflow](#entwickler-workflow)
- [Konfiguration](#konfiguration)
- [Repository-Struktur](#repository-struktur)
- [Sicherheitsinvarianten](#sicherheitsinvarianten)

---

## Was Yummi is <a id="was-yummi-ist"></a>

Yummi besteht aus **vier Komponenten und einem alternativen Frontend**, die um eine feste Regel herum aufgebaut sind: *Die KI schlägt vor, das Gateway entscheidet.* Jedes Pentest-Kommando, das die KI ausführt, fließt durch genau eine Stelle und wird durch eine im Fehlerfall schließende (fail-closed) Netzwerkrichtlinie geschützt, die auch Abstürze übersteht.

```
Benutzer / Betreiber  →  Orchestrator (Go)  →  Gateway (Rust, IPC)  →  Sandbox (Kali Docker)
   Web-UI / TUI          Dirigent + Agent       fail-closed nftables     nur Tool-Ausführung
                         Wissen + API           Policy-Engine + Audit
```

- **orchestrator** (Go) – besitzt den KI-Dirigenten, die agentenbasierte Graph-Engine, den Wissensspeicher und die REST/WebSocket-API. Berührt niemals nftables direkt.
- **gateway** (Rust, Binärdatei `yummi-gateway`) – die einzige Komponente, die externen Datenverkehr aus der Sandbox zulassen kann. Installiert beim Start `DROP`-All-nftables-Regeln; bei einem Absturz bleiben diese Regeln bestehen (**fail-closed**).
- **sandbox** (Kali Docker) – schreibgeschütztes Dateisystem, Nicht-Root-Benutzer, nur `NET_RAW`/`NET_ADMIN`. Passiv: Sie agiert nur, wenn der Orchestrator ein `docker exec` in ihr ausführt.
- **ui** (React 18 + Vite) – Web-Dashboard auf Port `:3000`, das `/api/` und `/ws/` an den Orchestrator auf Port `:9080` weiterleitet.
- **TUI** (Charm Bubble Tea) – eine vollständig terminalbasierte Betreiberkonsole (`yummi --tui`), die mit derselben REST/WS-API kommuniziert.

---

## Systemarchitektur <a id="systemarchitektur"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            Web-Dashboard   ·   Terminal-TUI          │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ ORCHESTRATOR · Go                                    │
        │                                                      │
        │   API ─▶ Dirigent ─▶ Agentengraph ─▶ Executor        │
        │                          │                           │
        │                          ▼  (geteiltes Weltmodell)   │
        │            Knowledge Store: Befunde · Speicher       │
        │                          · Kontext · TRACE           │
        └───────────────────────────┬──────────────────────────┘
                                    │  Unix-Socket · JSON-IPC mit Längenpräfix
        ┌───────────────────────────▼──────────────────────────┐
        │ GATEWAY · Rust-Vertrauensgrenze                      │
        │ fail-closed nftables · Egress · Audit · Watchdog     │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── der EINZIGE Pfad       │ kontrollierter Egress
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ SANDBOX · Kali          │      │  Externe Ziele       │
        │ schreibgeschützt ·      │      │  (gemäß Gateway-     │
        │ non-root                │      │   Richtlinie)        │
        └─────────────────────────┘      └──────────────────────┘
```

Das Signal läuft direkt an einer Achse nach unten – Betreiber → API → Dirigent → Agent → Executor → Gateway → Sandbox. `docker exec` ist der **einzige Pfad** aus der vertrauenswürdigen Ebene in die Sandbox, nachgelagert zur Bereichsprüfung des Gateways; die Sandbox erreicht die Außenwelt *ausschließlich* über die nftables-Regeln des Gateways. Der Knowledge Store hängt als geteiltes Weltmodell am Agenten.

### Netzwerktopologie

```
yummi_internal (172.20.0.0/24): sandbox .10 · gateway .2 (auch DNS) · orchestrator .3 · ui .4 · docker-proxy .5
yummi_external:                 nur gateway · orchestrator · ui — die Sandbox fehlt absichtlich.
                                Ihr einziger Weg nach draußen führt über die nftables-Regeln des Gateways.
Host-Ports: 3000 → ui nginx · 9080 → Orchestrator-API (nur localhost)
```

DNS für die Sandbox wird über das Gateway (`172.20.0.2`) erzwungen, sodass jede Abfrage protokolliert wird.

---

## Die Vertrauensgrenze <a id="die-vertrauensgrenze"></a>

Dies ist die **wichtigste Invariante** im Codebase: *Blockieren, sofern nicht ausdrücklich erlaubt.*

- Das Gateway installiert `DROP`-All-nftables-Regeln, **bevor** sein IPC-Socket überhaupt geöffnet wird. Die Sandbox hat null Konnektivität, bis eine explizite `Allow`-Regel eintrifft.
- Wenn der Gateway-Prozess stirbt, verbleiben die `DROP`-Regeln im Kernel – das System schließt im Fehlerfall (**fail-closed**), es bleibt niemals offen.
- **Blockierregeln überschreiben immer Erlaubnisregeln.**
- Pentest-Kommandos erreichen eine Shell über **genau eine Stelle**: `sandbox.Bridge.Exec`, was `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>` ausführt. Der Agent ruft `exec.Command` niemals selbst auf – er reiht Befehle nur in den Ausführungsspeicher ein, den der Executor leert (Gateway-Bereichsprüfung → Sandbox-Ausführung). Über den Agenten-/LLM-Output ist niemals ein hostseitiger Befehlspfad erreichbar.

> Das Gateway-Modul wird in Docker und nicht auf dem Host-System ausgeführt, wobei es die Kernel-Unterstützung der Container-Umgebung nutzt. Die direkte Ausführung der kompilierten Binärdatei auf dem macOS-Host (außerhalb von Docker) bietet keinen Linux-Kernel zur Durchsetzung der Regeln.

---

## Sicherheit auf Inhaltsebene <a id="sicherheit-auf-inhaltsebene"></a>

Das Gateway ist eine erstklassige Schnittstelle auf **Netzwerkschicht**. Ein Pentest-Dirigent ist jedoch einer zweiten, subtileren Angriffsfläche ausgesetzt: dem **Inhalt**, der *zurück* in den LLM-Prompt fließt. Der Befehlsausgang eines Ziels, eine gescrapte Webseite, eine MCP-Antwort – nichts davon wurde vom Betreiber verfasst, und alles kann Prompt-Injektionen (`ignore previous instructions…`), C2/Promptware-Direktiven, Exfiltrations-Payloads oder unsichtbares/Bidi-Unicode enthalten. Yummi dehnt dieselbe Fail-closed-Haltung auf diese Inhaltsebene aus.

```text
    Tool-Output · Speicher-Writes · Skill-Dateien     (NICHT vom Betreiber verfasst)
                       │
                       ▼
             ┌───────────────────┐   threatscan: Injektion · Exfiltration · C2
             │     threatscan    │               · unsichtbares Unicode
             └─────────┬─────────┘
      Tool-Output      │   SPEICHER-Schreiben / Skill-Laden
    (Eingriff während  │   (Benutzer-vermittelt)
    Laufzeit unmöglich)│
               ▼                ▼
    ⚠ Warnbanner          ⛔ Blockieren bei strengem Gültigkeitsbereich
    "untrusted DATA"
               │
               ▼
          LLM-Prompt
```

- **`internal/security/threatscan`** – ein Leaf-Paket (nur Standardbibliothek), das in drei Bereichen scannt (`all` / `context` / `strict`). Tool-Output wird **gescannt und mit einem Warnbanner versehen** (Erkennung, kein Blockieren – der Betreiber kann während des Befehls nicht eingreifen); `[REMEMBER]`-Schreibvorgänge und Skill-Downloads werden im Modus `strict` **blockiert**, sodass ein kompromittierter Output keine persistente, sitzungsübergreifende Prompt-Injektion hinterlassen kann. Zudem werden **ANSI/C1-Escape-Sequenzen** aus dem Output entfernt, bevor er das Gehirn erreicht.
- **Speicherherkunft (Memory provenance)** – Jede persistente Speicherzeile zeichnet zur Überprüfung auf, wer sie geschrieben hat (`agent` / `subagent`).
- **Architektonische Eignungstests** (`internal/security/invariants`) – Ausführbare Wächter, die den Quellbaum durchsuchen und den **Build fehlschlagen lassen**, wenn `os/exec` außerhalb der geprüften Shell-Grenzdateien auftaucht, wenn `internal/mcp` im Produktionscode importiert wird (Dormancy Lock) oder wenn sich ein Telemetrie-Endpunkt einschleicht. Die folgenden Invarianten werden durch Tests erzwungen und sind nicht bloß dokumentiert.

---

## Die Agenten-Engine <a id="die-agenten-engine"></a>

Yummis Agent ist **keine flache `while`-Schleife**. Er ist ein expliziter **Graph-Zustandsautomat**: Das LLM gibt pro Runde eine einzige getaggte Aktion aus, und ein deterministischer Router durchläuft typisierte Knoten über eine feste Kanten-Tabelle, bis ein Endknoten erreicht wird. Dieses Design verhindert die Erschöpfung des Kontextfensters und endlose Aktionsschleifen – jeder Schritt rendert den vollständigen Aufgabenstatus neu in den Prompt, anstatt sich auf einen fragilen Konversationsverlauf zu verlassen.

### Graph-Zustandsautomat – Knoten & Kanten <a id="graph-zustandsautomat"></a>

Die Engine befindet sich in `orchestrator/internal/agent`. `graph.go` enthält die Knotengebilde und die `graphEdges`-Routingtabelle (`node → edge → nextNode`); jeder Knoten befindet sich in seiner eigenen `node_*.go`-Datei.

**Die zehn Knoten:**

| Knoten | Datei | Verantwortung |
|------|------|----------------|
| `step` | `node_step.go` | Fragt das LLM nach der nächsten Aktion; rendert den vollständigen Aufgabenstatus + SKILLS + DELEGATION + MEMORY in den Prompt; routet basierend auf dem zurückgegebenen Kontroll-Tag. |
| `tool` | `node_tool.go` | Validiert, filtert und führt den gepufferten Befehl aus; erkennt hängende/doppelte Muster; überwacht lang laufende Befehle. |
| `consult` | `node_consult.go` | Beratung während des Befehls (z. B. bei langen Laufzeiten) – fragt das LLM nach `[CONTINUE]`/`[ABORT]`. |
| `retry` | `retry.go` | LLM/Parser-Fehlerbehebung mit exponentiellem Backoff; routet zurück zum Aufrufer oder gibt auf. |
| `summarize` | `node_lifecycle.go` | Kontextkomprimierung: Wenn das Token-Budget zu ≥80 % verbraucht ist, wird der Beobachtungsverlauf in einer Zusammenfassung komprimiert. |
| `approval` | `node_lifecycle.go` | Freigabe-Gate für den Betreiber (blockiert die Schleifen-Goroutine auf einem Kanal). |
| `finish` | `node_lifecycle.go` | Endknoten: abgeschlossen / fehlgeschlagen / gestoppt; generiert eine Zusammenfassung, falls das Modell dies versäumt hat. |
| `ask` | `node_lifecycle.go` | Endknoten: Pausieren und dem Betreiber eine Frage stellen. |
| `handoff` | `node_handoff.go` | Delegiert eine fokussierte Teilaufgabe an einen spezialisierten Sub-Agenten und führt dessen Zusammenfassung zurück. |
| `remember` | `node_remember.go` | Speichert einen strukturierten Speicher (z. B. eine Code-Map) im Wissensspeicher. |

**Der Ablauf** (`Execute` in `graph.go`) – Jeder aktive Knoten leitet zurück zu `step`, sodass die Engine die Schleife *ist*; sie endet nur an einem Endknoten:

```text
                     (jeder aktive Knoten führt zurück zu STEP)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ fragt   │ ◀───── PLAN_UPDATED ─────  │ exec │── Freigabe erf. ──────▶ approval ─▶ STEP
    │ LLM nach│                            │  +   │── lang_laufend ───────▶ consult  ─▶ STEP
    │ nächster│                            │ Guard│── Schleife / Hänger ──▶ consult  ─▶ STEP
    │ Aktion  │                            └──────┘
    └────┬────┘
         │  finish · Budget aus ──▶ FINISH  (Endknoten)
         │  ask_operator · Halt ──▶ ASK     (Endknoten)
         │  llm_error ──▶ retry ──▶ (zurück zum Aufrufer)
         │  summarize · remember · handoff ──▶ (zurück zu STEP)
         ▼
    Jeder STEP rendert den VOLLSTÄNDIGEN Aufgabenstatus (Plan · Beobachtungen ·
    SKILLS · MEMORY) neu in den Prompt – niemals den rohen Chatverlauf.
```

Endknoten (`finish`, `ask`) haben keine ausgehenden Kanten – `Execute` stoppt, sobald sie erreicht werden. `retry` routet dynamisch zurück zu dem Knoten, der es aufgerufen hat. Jeder Übergang wird gesichert, sodass eine abgestürzte Aufgabe an ihrem letzten Knoten fortgesetzt werden kann.

**Die Schleifenvermeidung ist mehrschichtig** (`tool`-Knoten): Exakte Duplikat-Erkennung (`[DUPLICATE_BLOCKED]`), ein Hänger-Detektor mit gleitendem Fenster für `A→B→A→B`-Zyklen (`[STUCK]`), ein **Tool-Thrash**-Schutz, der semantische Fast-Duplikate abfängt, die der Hash übersieht – z. B. der Wechsel zwischen `ps -ef` / `ps -ejH` / `ps aux` (`[ABNORMAL_PATTERN]`), und eine Eskalation bei aufeinanderfolgenden Fehlern, die einen anderen Ansatz erzwingt (`[REPEATED_FAILURE]`), bevor der Prozess angehalten und an den Betreiber übergeben wird. Wichtig ist, dass sich das prozedurale Gedächtnis *innerhalb derselben Aufgabe* niemals an einen bereits ausgeführten Befehl erinnert, sodass die Lernschleife keine Wiederholungsschleifen verstärken kann.

### Das Tag-Protokoll <a id="the-tag-protocol"></a>

Der Agent und das LLM kommunizieren über ein **geschlossenes, in eckigen Klammern gesetztes Vokabular** (definiert in der `Registry` von `protocol.go`). Dies ist *kein* Freitext und *kein* XML – es ist ein `[TAG]` in einer eigenen Zeile, optional gefolgt von einem JSON-Body. Die Registry ist die einzige Quelle der Wahrheit; die Protokollreferenz des Prompts wird daraus generiert.

```text
   AGENT (Knoten: step)                                 LLM (Gehirn)
        │  rendert vollständigen Aufgabenstatus + SKILLS · MEMORY │
        │  + Protokollreferenz, dann fragen ─────────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  validieren · Hänger/Thrash-Check · Scope          │
        │  ──▶ Gateway-Scope-Check ──▶ docker exec (Sandbox) │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] Kopf+Fuss ──────────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] komplette Ausgabe ───────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  Endknoten — Bericht generieren, falls Modell      │
        │  dies versäumt hat                                 │
```

Zwei Eigenschaften machen dies bei *kleinen* Modellen robust: Tool-Ausgaben werden **auf Bedrohungen gescannt und von ANSI-Codes befreit**, bevor sie das Gehirn erreichen (siehe [Sicherheit auf Inhaltsebene](#sicherheit-auf-inhaltsebene)), und eine Antwort ohne führendes Tag wird wiederhergestellt, indem der **Kontrollbefehl aus der JSON-Struktur abgeleitet wird** (ein `command`-Feld → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, …).

**Agent → LLM (Beobachtungs- / Umschlag-Tags) – wie der Agent Ergebnisse zurückgibt:**

| Tag | Bedeutung |
|-----|---------|
| `[TOOL_RESPONSE]` | Befehl erfolgreich; Ausgabe folgt. |
| `[TOOL_RESPONSE_TRUNCATED]` | Ausgabe zu lang; Kopf und Fuß werden angezeigt. Antworten Sie mit `[SEND_FULL]` für die vollständige Ausgabe. |
| `[TOOL_RESPONSE_FULL]` | Die vollständige Ausgabe, die Sie per `[SEND_FULL]` angefordert haben. |
| `[TOOL_FAILED]` | Befehl wurde ausgeführt, schlug jedoch fehl – anpassen, nicht wiederholen. |
| `[COMMAND_BLOCKED]` | Fehlerhaft formatiert oder das Gateway hat das Ziel als außerhalb des Scopes abgelehnt. |
| `[DUPLICATE_BLOCKED]` | Sie haben diesen Befehl gerade ausgeführt – wählen Sie einen anderen. |
| `[REPEATED_FAILURE]` | Mehrere Fehler in Folge – Tool/Route wechseln, umplanen oder beenden. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Wiederholungen erkannt. |
| `[LONG_RUNNING]` | Ein Befehl hat sein weiches Limit überschritten – antworten Sie mit `[CONTINUE]` oder `[ABORT]`. |
| `[OUTPUT_OVERFLOW]` | Die Terminalausgabe belastet das Kontextfenster. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | Der Betreiber hat den Befehl abgelehnt oder nicht geantwortet. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plan akzeptiert / nur noch wenige Schritte verbleibend. |
| `[UNKNOWN]` | Der Agent konnte die Situation nicht klassifizieren – Sie entscheiden. |

**LLM → Agent (Kontroll- / Aktions-Tags) – womit das Modell antwortet (ein Tag, gefolgt von einem JSON-Body):**

| Tag | Body | Wirkung |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Führt einen Sandbox-Befehl aus. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Aktualisiert den Plan. |
| `[FINISH]` | `{summary, findings}` | Ziel erreicht. |
| `[HANDOFF]` | `{role, subgoal}` | Delegiert eine Teilaufgabe an einen spezialisierten Sub-Agenten. |
| `[REMEMBER]` | `{key, value}` | Speichert eine strukturierte Information unter einem Schlüssel (wird nie vergessen). |
| `[ASK_OPERATOR]` | `{question}` | Blockiert; benötigt menschliche Interaktion. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Sende vollständige Ausgabe / weiter warten / Befehl abbrechen / Aufgabe stoppen. |

Schwächere Modelle lassen oft das führende Tag weg und senden nur JSON – der Parser leitet das Kontroll-Tag aus der Struktur des Payloads ab (`command`-Feld → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF` usw.), sodass der Agent unabhängig von der Formatstreue des Modells stabil bleibt.

### Gehirn-agnostischer Kern <a id="gehirn-agnostischer-kern"></a>

Das LLM ist eine **injizierte, austauschbare Abhängigkeit** hinter einer Ein-Methoden-Schnittstelle (`LLM { Chat(...) }`). Der Kern ist auf *Qualität und Abdeckung* ausgelegt, nicht auf die Fähigkeiten des aktuell angebundenen Modells. Ein kleines lokales Modell (qwen3:8b) dient als Entwicklungsgehirn und Qualitäts-Stresstest – wenn der Kern auf einem kleinen Gehirn zuverlässig funktioniert, läuft er auch auf einem großen. Schließen Sie ein stärkeres Gehirn an, und derselbe Kern *erweitert* einfach seine Fähigkeiten; er muss sich nie ändern.

---

## Der Wissensspeicher – zweischichtiger Speicher + Lernschleife <a id="der-wissensspeicher"></a>

`internal/knowledge` ist ein Leaf-Paket (hängt intern von nichts außer `scope` ab) und der **alleinige Besitzer des Weltmodells**. Der Agent, der Executor, die API und alle Sub-Agenten schreiben an denselben Ort – so werden die Entdeckungen des Agenten zu strukturiertem, abfragbarem, geteiltem Wissen statt zu Freitext, der in einer einzigen Aufgabe gefangen bleibt.

**Zwei Speicherschichten:**

1. **Faktenbasiert (pro Sitzung)** – Tabellen `targets` und `findings`. Befunde tragen Herkunftsdaten: `source` (`manual` / `agent` / `subagent`) und die `task_id`, die sie erzeugt hat.
2. **Prozedural (portabel, zielbereinigt)** – die Tabelle `procedural_memory`. Jedes ausgeführte Kommando wird als *Taktik* erfasst: `{tool, command_pattern, outcome}`, wobei das Ziel-Token durch den Platzhalter `<TARGET>` ersetzt wird (über `scope.ExtractTarget`), damit **keine zielspezifischen Daten** in die portable Schicht gelangen. Identische Taktiken werden mit einem `hit_count` zusammengeführt. Das Ergebnis (`worked` / `failed`) wird **mechanisch** aus dem Exit-Code abgeleitet – diese Lernschleife ist also nicht einmal vom Gehirn abhängig.

**Die Lernschleife:**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ Agent führt      │──▶│ Executor zeichnet     │──▶│ procedural_memory │
    │ Befehl aus       │   │ zielbereinigte Taktik │   │ Tool·Muster·      │
    └──────────────────┘   │ auf                   │   │ Ergebnis·Treffer  │
             ▲             └───────────────────────┘   └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step inji-   │◀─│ RetrievalStrategy liest   │◀────────┘
    │ ziert MEMORY-Block│  │ Taktiken für aktuelle     │
    │ in den Prompt     │  │ Tools                     │
    └───────────────────┘  │ ‹SCHLIESST Befehle aus,   │
                           │  die in DIESER Aufgabe    │
                           │  bereits liefen›          │
                           └───────────────────────────┘
```

Der Abruf erfolgt über eine `RetrievalStrategy`-Schnittstelle. Die implementierte Standard-Strategie `StructuredRetrieval` basiert auf deterministischem SQL, das auf die aktuelle Sitzung und die Tools der kürzlich ausgeführten Befehle abgestimmt ist – null neue Abhängigkeiten, vollständig lokal. Eine semantische/vektorbasierte Strategie kann später hinzugefügt werden, **ohne den Kern zu verändern**.

Der Ausschluss-Schritt (EXCLUDES) ist von zentraler Bedeutung: Das prozedurale Gedächtnis dient dem **Transfer zwischen Aufgaben**, nicht dem Zurückspiegeln des aktuellen Arbeitsbereichs in den Prompt. Ein gerade ausgeführter Befehl als *"vorheriges Wissen – nutzen, nicht neu entdecken"* darzustellen, führt bei schwachen Modellen genau in die Befehlsschleife. Daher werden Befehle der aktuellen Aufgabe beim Abruf herausgefiltert.

**Persistenter Kontext** – die Tabelle `persistent_context` (`ON CONFLICT DO UPDATE`) speichert langfristige Erinnerungen, die das gleitende Fenster einer einzelnen Aufgabe überdauern: Quellcode-Verbindungspläne, Modulgraphen, Dokumentationen. Der Quellcodeanalyse-Subagent erstellt und aktualisiert diese über `[REMEMBER]` – der „Vergiss-nie-Codemapper“.

---

## Skills – deklarative Just-in-Time-Playbooks <a id="skills"></a>

Das Hinzufügen aller Playbooks in den Prompt blockiert das Denkvermögen des Modells. `internal/skill` ist eine **deklarative Registry**: Jeder Skill ist eine YAML-Datei (`persona/v0.1/skills/*.yaml`) mit `{name, description, triggers, tools, knowledge}`. Zur Laufzeit bewertet die Registry jeden Skill danach, wie viele seiner `triggers` im Ziel und in den kürzlich verwendeten Tools auftauchen, und injiziert nur die **Top-N** relevanten Playbooks in die Plan- und Schritt-Prompts – ein schlankes, deterministisches, lokales RAG. Skills führen **keinen eigenen Ausführungspfad** ein; sie machen das Gehirn lediglich klüger bei der Auswahl der auszuführenden *Sandbox*-Befehle. Standardmäßig sind `recon`, `web` und `source-analysis` enthalten. Neue Skills werden einfach als YAML-Datei hinzugefügt – keine Codeänderung nötig.

---

## Sub-Agenten – begrenzte Delegation <a id="sub-agenten"></a>

Yummi unterstützt hierarchische Delegation. Wenn der Orchestrator tiefgehende, spezialisierte Arbeit benötigt, gibt das Modell `[HANDOFF]{role, subgoal}` aus, und der `handoff`-Knoten startet einen **spezialisierten Sub-Agenten** – denselben Graph-Zustandsautomaten, jedoch konfiguriert mit einer eigenen Persona und einer begrenzten Auswahl an Skills.

- **Gemeinsames Weltmodell.** Sub-Agenten teilen sich den Store und den Wissensspeicher des Orchestrators, sodass ihre Befunde und gelernten Taktiken automatisch in das gemeinsame Weltmodell einfließen. Genau aus diesem Grund wurde der Wissensspeicher zuerst entwickelt.
- **Token-Schutz.** Ein Sub-Agent führt seine eigene Schleife aus; der Eltern-Agent erhält nur eine saubere Zusammenfassung (`Summary`) und die Befunde im gemeinsamen Wissen zurück, nicht jedoch die internen Schritte des Sub-Agenten.
- **Begrenzte Tiefe von 1.** Sub-Agenten werden mit einer leeren Sub-Agenten-Map instanziiert, sodass sie niemals `[HANDOFF]` sehen oder akzeptieren – keine Rekursion, keine Fork-Bomben.
- **Gleiche Autorität.** Jeder Befehl eines Sub-Agenten läuft weiterhin über den einzigen Ausführungspfad → Gateway-Scope-Check → Sandbox. Ein Sub-Agent hat exakt dieselbe Berechtigung wie der Orchestrator, nicht mehr.

Ausgeliefert mit den Rollen `recon`, `web`, `source-analysis`, `reverser` und `source-analyzer` (`persona/v0.1/subagents/*.yaml`). Rollen können durch Hinzufügen einer YAML-Datei ergänzt werden.

---

## TRACE – Agenten-Beobachtbarkeit <a id="trace-beobachtbarkeit"></a>

Die Logik des Agenten wird **lesbar** gemacht. Der Graph gibt typisierte `ActivityEvent`s aus – `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish` –, die in einer `agent_activity`-Tabelle gespeichert und über den `/ws/events`-WebSocket als `agent.activity` gestreamt werden. Ereignisse von Sub-Agenten tragen eine `parent_task_id`, damit sie sich unter ihren Handoff-Bereich einordnen lassen.

- **Web AGENT-Reiter** – eine Aktivitäts-Zweitleiste: 🧠 Speicher abgerufen, 📘 Skill angewendet, 🔎 Befund, 🤝 Übergabe, 💾 gemerkt – damit Sie sehen, *worauf der Agent zurückgreift*, und nicht nur seinen Text.
- **Web TRACE-Reiter** – eine vollständige Visualisierungsoberfläche: der Live-Aktivitätsverlauf (verschachtelte Sub-Agenten-Bereiche) sowie durchsuchbare Befunde, gelernte Taktiken, persistenter Kontext (Code-Maps), Skills und Rollen.
- **Lese-APIs** (schreibgeschützt): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

Die TUI zeigt dieselben Aktivitäts-Chips live im Terminal an.

---

## Dirigent, Validator & Egress-Modi <a id="dirigent-validator-und-egress-modi"></a>

- **conductor** (`internal/conductor`) – spricht direkt mit Ollama über einen Persona-gesteuerten System-Prompt. **Es gibt kein regelbasiertes Fallback**: Wenn das LLM nicht konfiguriert oder nicht erreichbar ist, wird ein `intent: decline` mit einer zweisprachigen (Türkisch/Englisch, automatisch erkannt) Erklärung zurückgegeben. Antworten sind strukturierte JSON-Intents: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** – führt **ausschließlich eine Risikoklassifizierung** durch, keine Netzwerkentscheidungen. Das Risiko ergibt sich aus dem vom Modell angegebenen `risk_level` plus einer konservativen strukturellen Untergrenze (`classifyRisk` stuft das Risiko nur bei Shell-Chaining-Operatoren auf `critical` hoch). **Es gibt keine fest codierte Tool-Allowlist** – um dem Agenten ein neues Tool beizubringen, bearbeiten Sie die Informationen unter `persona/v0.1/` und nicht eine Registry.
- **Egress-Modus** (vom Betreiber gesetzt, vom Gateway erzwungen) – ein globaler 3-Wege-Schalter über der IP-Bereichsliste:
  - `block` (Standard, fail-closed) – kein externer Egress.
  - `allow` – Sandbox-Egress wird vollständig erlaubt (Hostnamen funktionieren ohne IP-Auflösung).
  - `ask` – jedes neue externe Ziel erfordert die Freigabe durch den Betreiber.
  - **Der Modus bleibt bei einem Gateway-Neustart nicht erhalten – er setzt sich auf `block` (fail-closed) zurück.**

---

## Gateway <a id="gateway"></a>

Die speichersichere Kontrollschicht (`gateway/`, Binärdatei `yummi-gateway`):

- **`policy/`** – die `PolicyEngine` (eine `RwLock<HashMap>` mit TTL-Ablauf); Block-Regeln überschreiben Allow-Regeln; der Egress-Modus entscheidet über das `verdict` (`allow` / `block` / `needs_approval`).
- **`network/`** – Linux/nftables-Controller zur Verwaltung der FORWARD-Kette (erfordert `NET_ADMIN`). Optionale Verschleierung von Fingerabdrücken: `ttl_normalize` (verhindert passive OS-Fingerabdrücke) und `mac_randomization`.
- **`ipc/`** – Unix-Domain-Socket, JSON-IPC mit Längenpräfix (`[4-Byte-BE-Länge][JSON]`). Anfragen: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** – SQLite-WAL-Protokoll, jede Zeile ist über **SHA-256 mit der vorherigen verkettet** (manipulationssicher). Änderungen an Regeln und Egress-Modi werden aufgezeichnet.
- **`watchdog.rs`** – RSS-Limit + Heartbeat-Überwachung; ein Verstoß löst eine Notfall-Sperrung und das Beenden des Daemons aus.

Ein Web-Reiter GATEWAY zeigt Status, aktive Regeln, das Audit-Protokoll und den Egress-Modus-Schalter.

---

## Sandbox <a id="sandbox"></a>

Ein gehärteter Kali-Container: schreibgeschütztes Root-Dateisystem, läuft als Benutzer `1000:1000`, nur `NET_RAW`/`NET_ADMIN`, `no-new-privileges`. DNS wird über das Gateway erzwungen, sodass alle Anfragen protokolliert werden. Der Entrypoint wartet lediglich auf den Gateway-Socket und führt dann ein `sleep infinity` aus – jeder Befehl ist ein frisches `docker exec`, keine dauerhafte Shell.

---

## Schnittstellen – Web-UI & TUI <a id="schnittstellen"></a>

Zwei Frontends, die **dieselbe REST/WS-API** verwenden – keines öffnet einen neuen Ausführungspfad:

- **Web-UI** (`ui/`, React 18 + Vite, nginx auf Port `:3000`) – Reiter: AGENT (Chat + Live-Aktivitätsverlauf), BASH (interaktives Sandbox-Terminal über WS), TRACE (Beobachtbarkeit + Weltmodell), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) – eine einfache und übersichtliche Terminal-Konsole: Chat mit Vorschlag → Freigabe, Aufgabenverfolgung des Agenten und derselbe Live-Aktivitätsverlauf (Skills/Speicher/Befund/Übergabe-Chips) über das WebSocket. Starten mit `yummi --tui` oder `make start-tui`.

---

## Schnellstart <a id="schnellstart"></a>

```bash
make install   # Gelingt der Bootstrap der Entwicklungsumgebung (Go, Rust, Node, Docker, Ollama)
make build     # Baut CLI + Orchestrator + Gateway-Binärdateien (UI wird separat gebaut)
make start     # Stellt sicher, dass Ollama läuft, und startet den Stack mit docker compose
# Web-Dashboard → http://localhost:3000

make start-tui # Startet den Stack und öffnet stattdessen die Terminal-Konsole
./bin/yummi --tui   # (falls die Dienste bereits laufen)
```

Der erste Aufruf von `make start` lädt das Standardmodell herunter (`qwen3:8b`, ca. 4 GB).

---

## Entwickler-Workflow <a id="entwickler-workflow"></a>

```bash
make build      # CLI + Orchestrator + Gateway (OHNE die UI – diese separat bauen)
make test       # Alle Testsuiten: CLI + Orchestrator + Gateway (cargo) + UI (vitest)
make lint       # golangci-lint im Root-Verzeichnis und im Orchestrator-Modul
```

```bash
# Granularer Build
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Granularer Test
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # Echtes qwen3:8b + Live-Sandbox-e2e (überspringt sich, wenn Sandbox aus ist)

# Lebenszyklus
make dev · dev-ui · dev-orchestrator     # Hot-Reload-Entwicklungsserver
make force-restart · close               # Alles neu starten / stoppen + VRAM freigeben
```

Zwei Go-Module: Das **Root-Modul** (`./go.mod`) enthält CLI + TUI; das **Orchestrator-Modul** (`orchestrator/go.mod`) umfasst die gesamte KI-, API- und Ausführungslogik. Tests unter `orchestrator/internal/*` müssen aus dem Verzeichnis `orchestrator/` ausgeführt werden (eigenes Modul). Das Gateway ist in Rust geschrieben; die UI nutzt vitest. **Keine Mocks der Yummi-Komponenten** – Tests führen echte Speicher/Engines aus, wobei nur die LLM-Grenze durch Stubs ersetzt wird.

---

## Konfiguration <a id="konfiguration"></a>

Umgebungsvariablen des Orchestrators:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # leer = Dirigent lehnt alles ab (kein Fallback)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Die Tier-Voreinstellungen bestimmen das Modell, das Kontextfenster und den Denkmodus: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (Entwicklungsstandard) → qwen3:8b.

**Der Netzwerkbereich ist keine Orchestrator-Einstellung.** Er wird ausschließlich in der YAML des Gateways konfiguriert (`gateway/config/default.yaml` unter `scope.allow` / `scope.block`) – das Gateway ist die einzige Scope-Autorität.

---

## Repository-Struktur <a id="repository-struktur"></a>

```text
cmd/yummi/              CLI-Einstiegspunkt (Binärdatei: yummi)
internal/
  cli/                  Terminal-Start, --tui-Verkabelung, Farbausgabe
  tui/                  Charm Bubble Tea Terminal-Konsole (api · ws · app · render · styles)
  config/ · version/    Konfigurationslader · Build-Metadaten
orchestrator/           Separates Go-Modul (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     Server-Binärdatei (verbindet alle Pakete)
  internal/
    agent/              Graph-Zustandsautomat – graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          Weltmodell – targets · findings · procedural · context · retrieval
    skill/              Deklarative Skill-Registry
    conductor/          LLM-Engine · Validator · Freigabe · Tier · Persona
    executor/ execution/ Einziger Ausführungspfad · Ausführungsspeicher
    api/                REST-Routen + Handler + WS-Brücke
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   Integrationspakete
gateway/                Rust-Vertrauensgrenze (policy · ipc · network · audit · watchdog)
sandbox/                Gehärtetes Kali-Container-Image + Entrypoint
ui/                     React 18 + Vite Dashboard (ausgeliefert von nginx auf Port :3000)
persona/v0.1/           Dokumentenbasierte Persona, skills/, subagents/
```

---

## Sicherheitsinvarianten <a id="sicherheitsinvarianten"></a>

Wenn Sie Yummi ändern, bewahren Sie diese auf:

1. **Standardmäßig fail-closed.** Scope-/nftables-Änderungen müssen das Prinzip „Blockieren, sofern nicht ausdrücklich erlaubt“ beibehalten. Das Gateway installiert DROP-Regeln vor dem Öffnen seines Sockets.
2. **Ein einziger Ausführungspfad.** Pentest-Befehle erreichen eine Shell nur über `sandbox.Bridge.Exec`. Führen Sie niemals einen hostseitigen Befehlspfad ein, der über Agenten-/LLM-Ausgaben erreichbar ist. (`internal/mcp` startet einen Host-Prozess und wird daher inaktiv gehalten – unimportiert im Produktionscode.)
3. **Das Gateway ist die alleinige Scope-Autorität.** Der Orchestrator fragt an; er entscheidet die Erreichbarkeit niemals selbst.
4. **Der Egress-Modus setzt sich bei einem Gateway-Neustart auf `block` zurück.**
5. **Trennung von Dirigent und Executor.** Der Dirigent kennt das LLM, die Personas, das Risiko – niemals jedoch, wie Befehle ausgeführt werden. Der Executor kennt das Gateway, die Sandbox, die Ausführung – niemals jedoch Chats oder Vorschläge.

---

<div align="center">

Gebaut mit Go · Rust · React · Charm · Ollama. Konventionelle Commits.

</div>
