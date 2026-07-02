<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">Un chef d'orchestre de pentest IA axé sur la confidentialité et fail-closed (sécurisé après défaillance).</p>

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
Yummi transforme le langage naturel en flux de travail de sécurité pratiques tout en gardant explicites le contrôle du trafic et l'autorité de l'opérateur. Un cerveau Red-Team autonome pilote un bac à sable (sandbox) Kali durci et isolé du réseau — et une frontière de confiance Rust sécurisée pour la mémoire garantit que l'IA ne peut jamais toucher une cible non autorisée.
</p>

---

## Table des matières

- [Ce qu'est Yummi](#ce-quest-yummi)
- [Architecture du système](#architecture-du-système)
- [La frontière de confiance (l'invariant le plus important)](#la-frontière-de-confiance)
- [Sécurité de la couche de contenu](#sécurité-de-la-couche-de-contenu)
- [Le moteur d'agent](#le-moteur-dagent)
  - [Machine d'état de graphe — nœuds et arêtes](#machine-détat-de-graphe)
  - [Le protocole de balises](#le-protocole-de-balises)
  - [Noyau indépendant du cerveau](#noyau-indépendant-du-cerveau)
- [Le Knowledge Store — mémoire à deux couches + boucle d'apprentissage](#le-knowledge-store)
- [Skills — playbooks déclaratifs just-in-time](#skills)
- [Sous-agents — délégation limitée](#sous-agents)
- [TRACE — observabilité de l'agent](#trace-observabilité)
- [Le chef d'orchestre, le validateur et les modes de sortie (egress)](#le-chef-dorchestre-le-validateur-et-les-modes-de-sortie)
- [Passerelle (frontière de confiance Rust)](#passerelle)
- [Bac à sable (Sandbox)](#bac-à-sable-sandbox)
- [Interfaces — Interface Web et TUI](#interfaces)
- [Démarrage rapide](#démarrage-rapide)
- [Flux de travail du développeur](#flux-de-travail-du-développeur)
- [Configuration](#configuration)
- [Structure du dépôt](#structure-du-dépôt)
- [Invariants de sécurité](#invariants-de-sécurité)

---

## Ce qu'est Yummi <a id="ce-quest-yummi"></a>

Yummi est composé de **quatre composants et une interface alternative**, construits autour d'une règle stricte : *l'IA propose, la passerelle dispose.* Chaque commande de pentest exécutée par l'IA passe par un seul endroit et est régie par une politique réseau fail-closed qui survit aux plantages.

```
Utilisateur / Opérateur  →  Orchestrateur (Go)  →  Passerelle (Rust, IPC)  →  Sandbox (Kali Docker)
  UI Web / TUI               chef d'orch. + agent    fail-closed nftables       exéc. outils uniq.
                             connaissances + API     moteur de règles + audit
```

- **orchestrator** (Go) — possède le chef d'orchestre IA, le moteur de graphe d'agent, le magasin de connaissances (knowledge store) et l'API REST/WebSocket. Ne touche jamais directement nftables.
- **gateway** (Rust, binaire `yummi-gateway`) — le seul composant capable d'ouvrir le trafic externe depuis le sandbox. Installe des règles nftables `DROP`-all au démarrage ; en cas de crash, ces règles persistent (**fail-closed**).
- **sandbox** (Kali Docker) — système de fichiers en lecture seule, non-root, uniquement `NET_RAW`/`NET_ADMIN`. Passif : il n'agit que lorsque l'orchestrateur y exécute un `docker exec`.
- **ui** (React 18 + Vite) — tableau de bord web sur `:3000`, redirigeant `/api/` et `/ws/` vers l'orchestrateur sur `:9080`.
- **TUI** (Charm Bubble Tea) — une console opérateur entièrement basée sur le terminal (`yummi --tui`), communiquant avec la même API REST/WS.

---

## Architecture du système <a id="architecture-du-système"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │        Tableau de bord Web   ·   Terminal TUI        │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ ORCHESTRATEUR · Go                                   │
        │                                                      │
        │   API ─▶ chef d'orch. ─▶ graphe d'agent ─▶ exécuteur │
        │                          │                           │
        │                          ▼  (modèle de monde part.)  │
        │         Knowledge Store : découvertes · mémoire      │
        │                          · contexte · TRACE          │
        └───────────────────────────┬──────────────────────────┘
                                    │  Socket Unix · IPC JSON avec préfixe de long.
        ┌───────────────────────────▼──────────────────────────┐
        │ PASSERELLE · Frontière de confiance Rust             │
        │ fail-closed nftables · sortie · audit · watchdog     │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── l'UNIQUE chemin        │ sortie contrôlée (egress)
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ SANDBOX · Kali          │      │  Cibles externes     │
        │ lecture seule · non-root│      │  (selon la politique │
        │                         │      │    de la passerelle) │
        └─────────────────────────┘      └──────────────────────┘
```

Le signal descend directement le long d'un seul axe — opérateur → API → chef d'orchestre → agent → exécuteur → passerelle → sandbox. `docker exec` est le **seul chemin** menant de l'espace de confiance vers le sandbox, en aval de la vérification de portée de la passerelle ; le sandbox accède au monde extérieur *exclusivement* via les règles nftables de la passerelle. Le Knowledge Store est rattaché à l'agent comme son modèle de monde partagé.

### Topologie du réseau

```
yummi_internal (172.20.0.0/24): sandbox .10 · passerelle .2 (aussi DNS) · orchestrateur .3 · ui .4 · docker-proxy .5
yummi_external:                 passerelle · orchestrateur · ui uniquement — le sandbox est intentionnellement absent.
                                Son unique chemin de sortie passe par les règles nftables de la passerelle.
Ports de l'hôte : 3000 → ui nginx · 9080 → API de l'orchestrateur (localhost uniquement)
```

Le DNS pour le sandbox est forcé via la passerelle (`172.20.0.2`), de sorte que chaque résolution de nom est enregistrée.

---

## La frontière de confiance <a id="la-frontière-de-confiance"></a>

C'est l'**invariant le plus important** du code de base : *interdire tout ce qui n'est pas explicitement autorisé.*

- La passerelle installe des règles nftables `DROP`-all **avant** même l'ouverture de son socket IPC. Le sandbox a une connectivité nulle tant qu'une règle `Allow` explicite n'est pas appliquée.
- Si le processus de la passerelle s'arrête, les règles `DROP` restent dans le noyau — le système se bloque en mode **fermé (fail-closed)**, jamais ouvert.
- **Les règles de blocage l'emportent toujours sur les règles d'autorisation.**
- Les commandes de pentest atteignent un shell par **un seul endroit** : `sandbox.Bridge.Exec`, qui lance `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>`. L'agent n'appelle jamais `exec.Command` lui-même — il s'ajoute simplement à la file d'attente du magasin d'exécution, que l'exécuteur vide (vérification de portée de la passerelle → exécution dans le sandbox). Aucun chemin de commande côté hôte n'est accessible depuis la sortie de l'agent ou du LLM.

> Le module de passerelle s'exécute dans Docker et non sur le système hôte, s'appuyant sur le support noyau de l'environnement de conteneurs. L'exécution directe du binaire compilé sur l'hôte macOS (hors Docker) ne dispose pas du noyau Linux requis pour appliquer les règles.

---

## Sécurité de la couche de contenu <a id="sécurité-de-la-couche-de-contenu"></a>

La passerelle est une excellente frontière au niveau de la **couche réseau**. Cependant, un chef d'orchestre de pentest fait face à une seconde surface d'attaque, plus subtile : le **contenu** qui revient vers le prompt du LLM. La sortie de commande d'une cible, une page web récupérée, une réponse MCP — rien de tout cela n'est rédigé par l'opérateur, et n'importe lequel de ces éléments peut contenir une injection de prompt (`ignore previous instructions…`), une directive C2/promptware, une charge utile d'exfiltration ou de l'unicode invisible/bidi. Yummi étend la même posture fail-closed à cette couche de contenu.

```text
    sortie d'outil · écritures mémoire · fichiers de compétences (NON rédigés par l'opérateur)
                       │
                       ▼
             ┌───────────────────┐   threatscan: injection · exfiltration · C2
             │     threatscan    │               · unicode invisible
             └─────────┬─────────┘
    sortie d'outil     │   écriture MÉMOIRE / chargement de compétences
   (impossible d'in-   │   (médiatisé par l'utilisateur)
   tervenir en cours)  │
               ▼                ▼
    ⚠ bannière d'avert.   ⛔ bloquer dans la portée stricte
    "untrusted DATA"
               │
               ▼
         prompt du LLM
```

- **`internal/security/threatscan`** — un package feuille (bibliothèque standard uniquement) qui scanne à trois niveaux de portée (`all` / `context` / `strict`). La sortie des outils est **scannée et marquée d'une bannière d'avertissement** (détection, pas de blocage — l'opérateur ne peut pas intervenir en cours de commande) ; les écritures `[REMEMBER]` et les chargements de compétences sont **bloqués** en portée stricte, empêchant une sortie corrompue de planter une injection de prompt persistante et inter-sessions. Il supprime également les **séquences d'échappement ANSI/C1** de la sortie avant qu'elle n'atteigne le cerveau.
- **Provenance de la mémoire** — chaque ligne de la mémoire persistante enregistre son auteur (`agent` / `subagent`) pour audit.
- **Tests de conformité architecturale** (`internal/security/invariants`) — gardes exécutables qui parcourent l'arborescence des sources et **font échouer le build** si `os/exec` apparaît en dehors des fichiers de limites de shell audités, si `internal/mcp` est importé par du code de production (verrouillage d'inactivité) ou si un point de terminaison de télémétrie s'y glisse. Les invariants ci-dessous sont imposés par des tests, et non simplement documentés.

---

## Le moteur d'agent <a id="le-moteur-dagent"></a>

L'agent de Yummi **n'est pas une simple boucle `while`**. Il s'agit d'une **machine d'état de graphe** explicite : le LLM émet une seule action balisée par tour, et un enrouteur déterministe parcourt des nœuds typés via une table d'arêtes fixe jusqu'à ce qu'un nœud terminal soit atteint. Cette conception évite l'épuisement de la fenêtre de contexte et les boucles d'actions infinies — chaque étape réaffiche l'état complet de la tâche dans le prompt plutôt que de s'appuyer sur un historique de conversation fragile.

### Machine d'état de graphe — nœuds et arêtes <a id="machine-détat-de-graphe"></a>

Le moteur vit dans `orchestrator/internal/agent`. Le fichier `graph.go` contient l'ensemble des nœuds et la table d'enroutage `graphEdges` (`node → edge → nextNode`) ; chaque nœud est défini dans son propre fichier `node_*.go`.

**Les dix nœuds :**

| Nœud | Fichier | Responsabilité |
|------|------|----------------|
| `step` | `node_step.go` | Demande au LLM la prochaine action ; insère l'état complet de la tâche + SKILLS + DELEGATION + MEMORY dans le prompt ; enroute en fonction de la balise de contrôle retournée. |
| `tool` | `node_tool.go` | Valide, filtre et exécute la commande en mémoire tampon ; détecte les schémas bloqués/doublons ; supervise les commandes de longue durée. |
| `consult` | `node_consult.go` | Consultation à mi-commande (ex : tâche longue) — demande au LLM `[CONTINUE]`/`[ABORT]`. |
| `retry` | `retry.go` | Récupération des erreurs LLM/analyse avec backoff exponentiel ; redirige vers l'appelant ou abandonne. |
| `summarize` | `node_lifecycle.go` | Compactage du contexte : lorsque le budget de tokens est consommé à ≥80 %, compresse l'historique des observations en un résumé. |
| `approval` | `node_lifecycle.go` | Barrière d'approbation de l'opérateur (bloque la goroutine de boucle sur un canal). |
| `finish` | `node_lifecycle.go` | Terminal : terminé / échoué / arrêté ; synthétise un résumé si le modèle n'en a pas produit. |
| `ask` | `node_lifecycle.go` | Terminal : suspend la tâche et pose une question à l'opérateur. |
| `handoff` | `node_handoff.go` | Délègue une sous-tâche ciblée à un sous-agent spécialiste et réinjecte son résumé. |
| `remember` | `node_remember.go` | Enregistre une mémoire structurée (ex : carte de code) dans le Knowledge Store. |

**Le parcours** (`Execute` dans `graph.go`) — chaque nœud actif redirige vers `step`, de sorte que le moteur *est* la boucle ; il ne s'arrête qu'au niveau d'un nœud terminal :

```text
                     (chaque nœud de travail boucle vers STEP)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ demand. │ ◀───── PLAN_UPDATED ─────  │ exec │── requiert approbation ─▶ approval ─▶ STEP
    │ au LLM  │                            │  +   │── longue durée ─────────▶ consult  ─▶ STEP
    │ la suite│                            │ garde│── bloqué / boucle ──────▶ consult  ─▶ STEP
    └────┬────┘                            └──────┘
         │  finish · budget dépassé ──▶ FINISH  (terminal)
         │  ask_operator · arrêt ─────▶ ASK     (terminal)
         │  llm_error ──▶ retry ──▶ (retour à l'appelant)
         │  summarize · remember · handoff ──▶ (retour à STEP)
         ▼
    Chaque STEP réaffiche l'état COMPLET de la tâche (plan · observations ·
    SKILLS · MEMORY) dans le prompt — jamais l'historique brut du chat.
```

Les nœuds terminaux (`finish`, `ask`) n'ont pas d'arêtes sortantes — `Execute` s'arrête lorsqu'il les atteint. Le nœud `retry` redirige dynamiquement vers le nœud qui l'a appelé. Chaque transition est sauvegardée, ce qui permet à une tâche interrompue de reprendre depuis son dernier nœud.

**La prévention des boucles est multicouche** (nœud `tool`) : détection d'empreinte identique (`[DUPLICATE_BLOCKED]`), un détecteur de blocage à fenêtre glissante pour les cycles `A→B→A→B` (`[STUCK]`), un garde **tool-thrash** (fatigue d'outils) qui intercepte les doublons sémantiques ignorés par le hash — ex : alternance entre `ps -ef` / `ps -ejH` / `ps aux` (`[ABNORMAL_PATTERN]`), et une escalade en cas d'échecs consécutifs qui force une approche différente (`[REPEATED_FAILURE]`) avant d'interrompre l'exécution et d'alerter l'opérateur. De manière cruciale, la mémoire procédurale ne rappelle jamais une commande déjà exécutée *lors de cette tâche*, de sorte que la boucle d'apprentissage ne peut pas renforcer une boucle de répétition.

### Le protocole de balises <a id="the-tag-protocol"></a>

L'agent et le LLM s'échangent un **vocabulaire fermé balisé par des crochets** (défini dans la `Registry` de `protocol.go`). Il ne s'agit *pas* de texte libre ni de XML — c'est une balise `[TAG]` isolée sur sa ligne, facultativement suivie d'un corps JSON. Le registre est l'unique source de vérité ; la référence du protocole dans le prompt est générée à partir de celui-ci.

```text
   AGENT (nœud step)                                    LLM (cerveau)
        │  affiche état de tâche + SKILLS · MEMORY             │
        │  + référence du protocole, puis demande ────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  valide · vérification blocage/fatigue · portée    │
        │  ──▶ vérif. portée passerelle ──▶ docker exec      │
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] début+fin ──────────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] sortie complète ─────────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  terminal — synthétise le rapport si le modèle a omis│
```

Deux propriétés rendent ce système robuste sur les *petits* modèles : la sortie des outils est **scannée contre les menaces et épurée des codes ANSI** avant d'atteindre le cerveau (voir [Sécurité de la couche de contenu](#sécurité-de-la-couche-de-contenu)), et une réponse sans balise de tête est récupérée en **déduisant le contrôle à partir du format du JSON** (présence du champ `command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, …).

**Agent → LLM (balises d'observation / enveloppe) — comment l'agent renvoie les résultats :**

| Balise | Signification |
|-----|---------|
| `[TOOL_RESPONSE]` | Commande réussie ; la sortie suit. |
| `[TOOL_RESPONSE_TRUNCATED]` | Sortie trop longue ; début+fin affichés. Répondez `[SEND_FULL]` pour tout recevoir. |
| `[TOOL_RESPONSE_FULL]` | La sortie complète que vous avez demandée via `[SEND_FULL]`. |
| `[TOOL_FAILED]` | La commande a été lancée mais a échoué — adaptez, ne répétez pas. |
| `[COMMAND_BLOCKED]` | Mal formé, ou la passerelle a rejeté la cible comme étant hors de portée. |
| `[DUPLICATE_BLOCKED]` | Vous venez de lancer cette commande identique — choisissez-en une autre. |
| `[REPEATED_FAILURE]` | Plusieurs échecs d'affilée — changez d'outil/de route, replanifiez ou terminez. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Répétition détectée. |
| `[LONG_RUNNING]` | Une commande a dépassé sa limite souple — répondez `[CONTINUE]` ou `[ABORT]`. |
| `[OUTPUT_OVERFLOW]` | La sortie du terminal surcharge la fenêtre de contexte. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | L'opérateur a refusé / n'a pas répondu à la commande soumise à approbation. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plan accepté / peu d'étapes restantes. |
| `[UNKNOWN]` | L'agent n'a pas pu classifier la situation — à vous de décider. |

**LLM → Agent (balises de contrôle / action) — ce que le modèle renvoie en réponse (une balise, puis un corps JSON) :**

| Balise | Corps | Effet |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Exécute une commande dans le sandbox. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Révise le plan. |
| `[FINISH]` | `{summary, findings}` | Objectif atteint. |
| `[HANDOFF]` | `{role, subgoal}` | Délègue une sous-tâche à un sous-agent spécialiste. |
| `[REMEMBER]` | `{key, value}` | Enregistre une information structurée sous une clé (jamais oubliée). |
| `[ASK_OPERATOR]` | `{question}` | Bloqué ; besoin d'une intervention humaine. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Renvoyer la sortie complète / continuer à attendre / annuler la commande / arrêter la tâche. |

Les modèles faibles omettent souvent la balise initiale et émettent uniquement le JSON — le parser déduit la balise de contrôle à partir de la structure des données (un champ `command` → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, etc.), de sorte que l'agent reste robuste quelle que soit la fidélité du format du modèle.

### Noyau indépendant du cerveau <a id="noyau-indépendant-du-cerveau"></a>

Le LLM est une **dépendance injectée et interchangeable** derrière une interface à méthode unique (`LLM { Chat(...) }`). Le cœur est conçu pour la *qualité et la couverture*, jamais pour les capacités du modèle actuellement connecté. Un petit modèle local (qwen3:8b) sert de cerveau de développement et de test de charge — si le cœur fonctionne sainement sur un petit cerveau, il fonctionnera sur un grand. Connectez un cerveau plus puissant et le même cœur va simplement *étendre* son niveau de performance ; il n'aura jamais besoin d'être modifié.

---

## Le Knowledge Store — mémoire à deux couches + boucle d'apprentissage <a id="le-knowledge-store"></a>

`internal/knowledge` est un package feuille (qui ne dépend en interne de rien d'autre que `scope`) qui est l'**unique propriétaire du modèle du monde**. L'agent, l'exécuteur, l'API et tous les sous-agents écrivent au même endroit — de sorte que les découvertes de l'agent deviennent des connaissances partagées, structurées et interrogeables plutôt que du texte libre enfermé dans une seule tâche.

**Deux couches de mémoire :**

1. **Factuelle (par session)** — tables `targets` et `findings`. Les découvertes portent une provenance : `source` (`manual` / `agent` / `subagent`) et le `task_id` qui les a produites.
2. **Procédurale (portable, nettoyée des cibles)** — la table `procedural_memory`. Chaque commande exécutée est enregistrée sous forme de *tactique* : `{tool, command_pattern, outcome}`, où la cible est remplacée par le jeton `<TARGET>` (via `scope.ExtractTarget`) pour que **nulle donnée spécifique à la cible ne fuie** dans la couche portable. Les tactiques identiques sont dédoublées à l'aide d'un compteur `hit_count`. Le résultat (`worked` / `failed`) est dérivé **mécaniquement** du code de sortie — cette boucle d'apprentissage ne dépend donc même pas du cerveau.

**La boucle d'apprentissage :**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ l'agent lance    │──▶│ l'exécuteur enregistre│──▶│ procedural_memory │
    │ une commande     │   │ une tactique nettoyée │   │ outil·schéma·     │
    └──────────────────┘   └───────────────────────┘   │ résultat·hits     │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step injecte │◀─│ RetrievalStrategy lit les │◀────────┘
    │ un bloc MEMORY    │  │ tactiques pour les outils │
    │ dans le prompt    │  │ actuels                   │
    └───────────────────┘  │ ‹EXCLUT les commandes déjà│
                           │  lancées pour CETTE tâche›│
                           └───────────────────────────┘
```

La récupération de l'information se fait via une interface `RetrievalStrategy`. L'implémentation fournie, `StructuredRetrieval`, est du SQL déterministe indexé sur la session en cours et les outils des commandes récemment exécutées — zéro nouvelle dépendance, entièrement local. Une stratégie sémantique / basée sur les embeddings pourra être greffée ultérieurement **sans toucher au cœur**.

L'étape d'exclusion (EXCLUDES) est essentielle : la mémoire procédurale sert au **transfert entre tâches**, jamais à répercuter le contexte de la tâche en cours dans le prompt. Présenter une commande qui vient d'être exécutée comme une *"connaissance préalable — utilisez-la, ne la redécouvrez pas"* est précisément ce qui pousse un modèle faible dans une boucle de commandes répétitives, c'est pourquoi les commandes de la tâche actuelle sont filtrées.

**Contexte persistant** — la table `persistent_context` (`ON CONFLICT DO UPDATE`) conserve des souvenirs à long terme qui survivent à la fenêtre glissante d'une seule tâche : cartes de connexion de code source, graphes de modules, documentation. Le sous-agent d'analyse de code les construit et les met à jour via `[REMEMBER]` — le « cartographe de code qui n'oublie jamais ».

---

## Skills — playbooks déclaratifs just-in-time <a id="skills"></a>

Entasser tous les playbooks dans le prompt détruit la capacité de raisonnement du modèle. `internal/skill` est un **registre déclaratif** : chaque compétence est un fichier YAML (`persona/v0.1/skills/*.yaml`) contenant `{name, description, triggers, tools, knowledge}`. Au moment de l'exécution, le registre évalue chaque compétence selon le nombre de ses `triggers` (déclencheurs) qui apparaissent dans l'objectif et les outils récemment utilisés, et n'injecte que les **N premières** guides pertinents dans les prompts de planification et d'étape — un RAG local, déterministe et léger. Les compétences **n'introduisent aucune route d'exécution** ; elles rendent simplement le cerveau plus intelligent pour décider des commandes à lancer dans le *sandbox*. Livré avec les compétences `recon`, `web` et `source-analysis`. Pour ajouter une compétence, déposez un fichier YAML — aucune modification de code.

---

## Sous-agents — délégation limitée <a id="sous-agents"></a>

Yummi prend en charge la délégation hiérarchique. Lorsque l'orchestrateur a besoin d'un travail approfondi et spécialisé, le modèle émet `[HANDOFF]{role, subgoal}` et le nœud `handoff` lance un **sous-agent spécialiste** — la même machine d'état de graphe, configurée avec son propre profil (persona) et un sous-ensemble restreint de compétences.

- **Modèle de monde partagé.** Les sous-agents partagent le Store et le Knowledge Store de l'orchestrateur, de sorte que leurs découvertes et tactiques apprises sont automatiquement intégrées au modèle mondial unique. C'est précisément pour cela que le Knowledge Store a dû être conçu en premier.
- **Isolation des tokens.** Un sous-agent exécute sa propre boucle ; le parent ne récupère qu'un `Summary` (résumé) propre (et les résultats déjà enregistrés dans le savoir partagé), et non l'intégralité des échanges internes du sous-agent.
- **Profondeur limitée à 1.** Les sous-agents sont créés avec une table de sous-agents vide, ils ne voient ni n'acceptent donc jamais de `[HANDOFF]` — pas de récursion, pas de bombes fork.
- **Même niveau d'autorité.** Chaque commande d'un sous-agent passe toujours par le chemin d'exécution unique → vérification de portée de la passerelle → sandbox. Un sous-agent possède exactement les droits de l'orchestrateur, ni plus ni moins.

Livré avec les rôles `recon`, `web`, `source-analysis`, `reverser` et `source-analyzer` (`persona/v0.1/subagents/*.yaml`). Ajoutez un rôle en y glissant un fichier YAML.

---

## TRACE — observabilité de l'agent <a id="trace-observability"></a>

L'intelligence de l'agent est rendue **lisible**. Le graphe émet des événements typés `ActivityEvent` — `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish` — enregistrés dans une table `agent_activity` et diffusés via le WebSocket `/ws/events` en tant que `agent.activity`. Les événements des sous-agents comportent un `parent_task_id` afin de les hiérarchiser sous leur span de transfert.

- **Onglet AGENT web** — une frise chronologique intégrée : 🧠 mémoire rappelée, 📘 compétence appliquée, 🔎 découverte, 🤝 transfert, 💾 mémorisé — vous voyez ainsi *ce sur quoi l'agent s'appuie*, et pas seulement ses lignes de texte.
- **Onglet TRACE web** — une interface d'observabilité complète : le suivi des activités en direct (les spans imbriqués des sous-agents) ainsi que les découvertes consultables, les tactiques apprises, le contexte persistant (cartes de code), les compétences et les rôles.
- **APIs de lecture** (lecture seule) : `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

La TUI affiche ces mêmes chips d'activité en direct dans le terminal.

---

## Le chef d'orchestre, le validateur et les modes de sortie <a id="le-chef-dorchestre-le-validateur-et-les-modes-de-sortie"></a>

- **conductor** (`internal/conductor`) — communique directement avec Ollama via un prompt système guidé par le profil. **Il n'y a pas de repli basé sur des règles** : si le LLM n'est pas configuré ou est injoignable, il renvoie un `intent: decline` avec une explication bilingue (turc/anglais, détectée automatiquement). Les réponses sont des intentions JSON structurées : `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** — effectue **uniquement une classification des risques**, pas de décisions sur la portée réseau. Le risque est évalué à partir du `risk_level` propre au modèle, complété par un plancher structurel conservateur (`classifyRisk` ne passe à `critical` que lors d'enchaînements de commandes shell). **Il n'y a pas de liste blanche d'outils codée en dur** — pour enseigner un nouvel outil à l'agent, modifiez les connaissances sous `persona/v0.1/` plutôt qu'un registre dans le code.
- **Mode de sortie (egress)** (défini par l'opérateur, appliqué par la passerelle) — un commutateur global à 3 positions situé au-dessus de la liste de portée IP :
  - `block` (par défaut, fail-closed) — aucune sortie externe autorisée.
  - `allow` — la sortie du sandbox est entièrement acceptée (les noms de domaine fonctionnent sans résolution IP individuelle).
  - `ask` — chaque nouvelle destination externe doit être approuvée par l'opérateur.
  - **Ce mode ne persiste pas en cas de redémarrage de la passerelle — il revient à `block` (fail-closed).**

---

## Passerelle <a id="passerelle"></a>

La couche de contrôle sécurisée pour la mémoire (`gateway/`, binaire `yummi-gateway`) :

- **`policy/`** — le `PolicyEngine` (table `RwLock<HashMap>` avec expiration TTL) ; le blocage l'emporte sur l'autorisation ; le mode de sortie résout une décision `verdict` (`allow` / `block` / `needs_approval`).
- **`network/`** — contrôleur Linux/nftables gérant la chaîne FORWARD (nécessite `NET_ADMIN`). Masquage optionnel de l'empreinte réseau : `ttl_normalize` (pour contrer la détection passive d'OS) et `mac_randomization`.
- **`ipc/`** — socket de domaine Unix, trames JSON préfixées par la longueur (`[longueur BE sur 4 octets][JSON]`). Requêtes : `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** — journal SQLite WAL, chaque ligne étant **liée à la précédente par un hash SHA-256** (infalsifiable). L'ajout/suppression de règles et les changements de mode de sortie y sont enregistrés.
- **`watchdog.rs`** — limite RSS + fraîcheur du signal de vie ; tout écart déclenche un verrouillage d'urgence et l'arrêt du démon.

Un onglet GATEWAY web affiche l'état, les règles actives, le journal d'audit et le commutateur de mode de sortie.

---

## Bac à sable (Sandbox) <a id="bac-à-sable-sandbox"></a>

Un conteneur Kali renforcé : système de fichiers racine en lecture seule, s'exécute avec les droits `1000:1000`, uniquement `NET_RAW`/`NET_ADMIN`, option `no-new-privileges`. Le DNS est forcé via la passerelle afin que toutes les requêtes soient enregistrées. Le point d'entrée attend simplement le socket de la passerelle puis lance un `sleep infinity` — chaque commande est exécutée via un `docker exec` vierge, sans shell persistant.

---

## Interfaces — Interface Web et TUI <a id="interfaces"></a>

Deux interfaces pour **une seule et même API REST/WS** — aucune n'ouvre de nouveau chemin d'exécution :

- **Web UI** (`ui/`, React 18 + Vite, nginx sur `:3000`) — onglets : AGENT (chat + chronologie d'activité en direct), BASH (terminal interactif du sandbox sur WS), TRACE (observabilité + modèle mondial), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) — console opérateur simple et épurée dans le terminal : chat avec proposition → approbation, suivi des tâches de l'agent, et la même chronologie d'activité en direct (compétences, mémoires, découvertes, transferts) via le WebSocket. Lancez avec `yummi --tui` ou `make start-tui`.

---

## Démarrage rapide <a id="démarrage-rapide"></a>

```bash
make install   # prépare l'environnement de dév (Go, Rust, Node, Docker, Ollama)
make build     # compile la CLI + l'orchestrateur + les binaires de la passerelle (l'UI est compilée à part)
make start     # s'assure qu'Ollama est actif, puis démarre la pile avec docker compose
# Tableau de bord web → http://localhost:3000

make start-tui # démarre la pile et lance la console terminal à la place
./bin/yummi --tui   # (si les services sont déjà démarrés)
```

Le premier `make start` télécharge le modèle par défaut (`qwen3:8b`, environ 4 Go).

---

## Flux de travail du développeur <a id="flux-de-travail-du-développeur"></a>

```bash
make build      # CLI + orchestrateur + passerelle (PAS l'UI — compilez-la séparément)
make test       # toutes les suites : CLI + orchestrateur + passerelle (cargo) + UI (vitest)
make lint       # exécute golangci-lint à la racine et dans le module orchestrator
```

```bash
# Build modulaire
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Tests modulaires
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # qwen3:8b réel + sandbox réel e2e (s'auto-ignore si le sandbox est éteint)

# Cycle de vie
make dev · dev-ui · dev-orchestrator     # serveurs de développement avec rechargement à chaud
make force-restart · close               # redémarre / éteint tout + libère la VRAM
```

Deux modules Go : la **racine** (`./go.mod`) contient la CLI + TUI ; l'**orchestrateur** (`orchestrator/go.mod`) gère toute la logique IA/API/exécution. Les tests sous `orchestrator/internal/*` doivent être lancés depuis le dossier `orchestrator/` (module séparé). La passerelle est en Rust ; l'UI utilise vitest. **Pas de mock des composants de Yummi** — les tests sollicitent de vrais stores/moteurs, seule l'interface LLM étant remplacée par des bouchons (stubs).

---

## Configuration <a id="configuration"></a>

Variables d'environnement de l'orchestrateur :

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # vide = le chef d'orchestre décline tout (pas de repli)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Les profils de performance (tiers) déterminent le modèle, la fenêtre de contexte et le mode de réflexion : `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (valeur par défaut en dév) → qwen3:8b.

**La portée réseau n'est pas un réglage de l'orchestrateur.** Elle est configurée exclusivement dans le propre YAML de la passerelle (`scope.allow` / `scope.block` dans `gateway/config/default.yaml`) — la passerelle est l'unique autorité sur la portée réseau.

---

## Structure du dépôt <a id="structure-du-dépôt"></a>

```text
cmd/yummi/              Point d'entrée de la CLI (binaire : yummi)
internal/
  cli/                  démarrage dans le terminal, liaison --tui, sortie couleur
  tui/                  console terminal Charm Bubble Tea (api · ws · app · render · styles)
  config/ · version/    chargeur de config · métadonnées de compilation
orchestrator/           module Go séparé (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     binaire du serveur (lie tous les packages)
  internal/
    agent/              machine d'état de graphe — graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          modèle du monde — targets · findings · procedural · context · récupération
    skill/              registre déclaratif de compétences
    conductor/          moteur LLM · validateur · approbation · profil · persona
    executor/ execution/ chemin d'exécution unique · magasin d'exécution
    api/                routes REST + handlers + pont WS
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   packages feuilles d'intégration
gateway/                frontière de confiance Rust (policy · ipc · network · audit · watchdog)
sandbox/                image du conteneur Kali durci + point d'entrée
ui/                     仪表板 React 18 + Vite (servi par nginx sur le port :3000)
persona/v0.1/           persona défini par des documents, skills/, subagents/
```

---

## Invariants de sécurité <a id="invariants-de-sécurité"></a>

Si vous modifiez Yummi, veillez à préserver ces règles :

1. **Fail-closed par défaut.** Les modifications de portée ou de configuration nftables doivent respecter la règle « interdire tout ce qui n'est pas explicitement autorisé ». La passerelle installe ses règles DROP avant d'ouvrir son socket.
2. **Un seul chemin d'exécution.** Les commandes de pentest atteignent un shell uniquement par le biais de `sandbox.Bridge.Exec`. N'introduisez jamais de chemin d'exécution côté hôte accessible depuis la sortie de l'agent ou du LLM. (`internal/mcp` lance un processus hôte et reste donc inactif — non importé par le code de production.)
3. **La passerelle est l'unique autorité sur la portée réseau.** L'orchestrateur interroge la passerelle ; il ne décide jamais seul de la connectivité.
4. **Le mode de sortie réinitialise sa politique à `block` au redémarrage de la passerelle.**
5. **Séparation entre chef d'orchestre et exécuteur.** Le chef d'orchestre connaît le LLM, les personas, le risque — mais jamais la manière dont les commandes sont exécutées. L'exécuteur connaît la passerelle, le sandbox, l'exécution — mais ignore tout du chat ou des propositions.

---

<div align="center">

Développé en Go · Rust · React · Charm · Ollama. Conventional Commits.

</div>
