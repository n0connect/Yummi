# Changelog

## [Unreleased]

### Features

* **agent:** enforce execution-history provenance boundary preventing model plan forgery and receipt overwrites (`Store.ReplaceModelPlan`)
* **browser,proxy:** implement in-sandbox HTTP/HTTPS and WebSocket connection proxy (`browser_proxy.go`) enforcing gateway hostname scope on Chromium traffic
* **browser:** add 9 production-grade browser automation tools (`browser_open`, `browser_snapshot`, `browser_interact`, `browser_eval`, `browser_screenshot`, `browser_storage`, `browser_tabs`, `browser_logs`, `browser_session`) with deep DOM snapshots and dialog handling
* **ui:** add operator-controlled web search toggle button (`btn-web-search`) with optimistic rollbacks and bilingual i18n
* **ui:** add cited research source cards (`ResearchSources.tsx`) with SHA-256 hashes, URLs, and retrieval timestamps
* **webresearch:** add controlled sandboxed web research tools (`web_search`, `web_fetch`) with HTML text conversion, script/style stripping, and `UNTRUSTED_WEB_CONTENT` tagging

### Bug Fixes

* **browser:** resolve proxy body read panic on HTTP/S error responses and expand accessibility tree with semantic tags (`li`, `td`, `th`, `blockquote`, `code`)
* **sandbox-mcp:** keep persistent Docker transport alive across execution contexts to preserve browser pages and refs
* **qa:** add edge-case QC suite, browser research live validation, and UI verification scripts

## [1.1.0](https://github.com/n0connect/Yummi/compare/v1.0.1...v1.1.0) (2026-10-10)


### Features

* **agent,skills:** integrate 13 elite red-team skills and 5 specialist subagents ([8072768](https://github.com/n0connect/Yummi/commit/807276853846ff489fcf045abffb6b014018b487))
* **agent,skills:** integrate 13 elite red-team skills and 7 specialist subagents from third-party repos ([47a798d](https://github.com/n0connect/Yummi/commit/47a798d500bee1b918b9f7b3da37c00b0715c3c2))
* **agent:** add 12 elite red-team skills and 6 specialist subagents ([bc12050](https://github.com/n0connect/Yummi/commit/bc12050b13d0ddc31dd40d879d14222e202a8d3b))
* **agent:** add 5 red-team skills, 4 subagents, and upgrade legacy skills ([b384e06](https://github.com/n0connect/Yummi/commit/b384e069709b5e2efcdeb5c600dfb35a056a2de3))
* **agent:** add 7 advanced red-team skills and 4 specialist subagents ([555b005](https://github.com/n0connect/Yummi/commit/555b005b478401d1145aac9b0256e2207ba28f1b))
* **agent:** add 7 enterprise red-team skills and 4 specialist subagents ([d3fed98](https://github.com/n0connect/Yummi/commit/d3fed98775e27174cdeaad874004c70e1401bf5b))
* **agent:** add 9 elite red-team skills, 7 specialist subagents, and upgrade catalog to v3.0.0 ([63b49d5](https://github.com/n0connect/Yummi/commit/63b49d559143bb68133ea43b053c9cafdd9d43c1))
* **agent:** add advanced red-team skills and specialist subagents for xxe, nosql, pivoting, windows privesc, container breakout, and cors ([13b901c](https://github.com/n0connect/Yummi/commit/13b901ccd348d3feb15b0844fc91fb9005a711c3))
* **agent:** integrate battle-tested red-team skills and specialist subagents from industry research ([d6302ca](https://github.com/n0connect/Yummi/commit/d6302cabe0b723df0597fb5f95204cd1a4ceb381))
* **agent:** local-model full-capability parity for the graph backend ([fd4eae9](https://github.com/n0connect/Yummi/commit/fd4eae9b13db710a271bdb48671e410b9af8957d))
* **agent:** operator-approved step budget, report_progress tool, route-level loop guard ([f1aff7e](https://github.com/n0connect/Yummi/commit/f1aff7e5441e68bb0f3209de903d38961ae5ea0b))
* **agent:** task brief, playbook push, and overflow recovery ([88d9c6e](https://github.com/n0connect/Yummi/commit/88d9c6e0b2e597ef00a13a68ff387ae5531db37a))
* **api:** read-only watch endpoints for agent-owned sandbox terminals ([3f063db](https://github.com/n0connect/Yummi/commit/3f063db4a2db28576c5be42641b721aa509db0df))
* **api:** skill usage stats and operator close for agent terminals ([de4a969](https://github.com/n0connect/Yummi/commit/de4a9696feb716ac9c160739583d0d8f1d98e132))
* **brain:** grow the skill library from brain-driven tasks too ([ea21e22](https://github.com/n0connect/Yummi/commit/ea21e22cff4491b792c8affc54040736b92a5006))
* **conf:** collapse make conf to the fewest questions that need a human ([3565795](https://github.com/n0connect/Yummi/commit/3565795b124d7954005af091a5af3c6aed0a84e8))
* **desktop:** host-to-sandbox file sharing, drag-and-drop onto the Desktop ([2f2ce24](https://github.com/n0connect/Yummi/commit/2f2ce24af39458c69feaeea44a22d1c392d267ff))
* **gateway:** implement 4 egress modes (block all, block except active rules, allow all, ask) ([81e80f9](https://github.com/n0connect/Yummi/commit/81e80f9361a41c8b9b1322f02f914b841a00b2fb))
* **persona:** expose pwnbox workbench inventory and specialist swarm to connected models ([8911a3f](https://github.com/n0connect/Yummi/commit/8911a3f43bb97e01bbefefbe06ee851a49fc547f))
* **ui:** add archive access button to sidebar and unselect target launcher modes by default ([c9560b7](https://github.com/n0connect/Yummi/commit/c9560b7775410f9fed2625693fbbea33467bf307))
* **ui:** add dedicated persistent TaskSummaryMessage card and retain findings on reload ([98a1c0d](https://github.com/n0connect/Yummi/commit/98a1c0d0412826621a1cc80b1ed70504251faf21))
* **ui:** add pin, export, archive, and delete action buttons to session rail cards ([783f16c](https://github.com/n0connect/Yummi/commit/783f16c57e7b14cd6081bb994ba3a0a8e201257f))
* **ui:** agent terminal page, rail push layout, nav grouping, speaker dividers ([be4940f](https://github.com/n0connect/Yummi/commit/be4940f242738b59bead164166a7b57441df722a))
* **ui:** default desktop tab to locked standby with eye-off overlay and click-to-activate ([9b4bc44](https://github.com/n0connect/Yummi/commit/9b4bc440c75a2fd4210aae4fa8981f7040bf3120))
* **ui:** refine workspace aesthetics, add auto-growing textarea and drag-and-drop file upload ([b9d1a69](https://github.com/n0connect/Yummi/commit/b9d1a691ec3da8196a2828fea30bcfe1b931391a))
* **ui:** show skill suggestions, rejected memory, and terminal close ([c7484bd](https://github.com/n0connect/Yummi/commit/c7484bdaa64f2e5aa545aec33575bdf6e5962f53))


### Bug Fixes

* **agent:** carry forward evidence digest across an Eino resume pause ([e5ac57d](https://github.com/n0connect/Yummi/commit/e5ac57d4da8e3a3ce8841fe7cf83b6ede45151ef))
* **api:** route operator replies to a paused task as its answer ([410fa0f](https://github.com/n0connect/Yummi/commit/410fa0f2f6e36d051f66074aa5ab013e1ea4049c))
* **build:** build only lab-target in lab-target-up recipe ([03e294b](https://github.com/n0connect/Yummi/commit/03e294b0c193ddf0cf0384661bd11739ca166811))
* **build:** skip local Ollama daemon and model check when cloud model backend is configured ([930cf64](https://github.com/n0connect/Yummi/commit/930cf6438b62d33b9434c90df99811d5e5b58f61))
* **chat:** remove hardcoded 120-message truncation and expand session query limits ([cdd8977](https://github.com/n0connect/Yummi/commit/cdd8977f8300027e7b48402c73d1145a5f7aea7c))
* **ci:** bump Go toolchain to 1.27.2, fixing the govulncheck failure ([ea10fe8](https://github.com/n0connect/Yummi/commit/ea10fe8f36b1cb0a709baf88f5ed2e68638c9135))
* **conductor:** reduce false-positive critical risk badges on read-only info commands ([79cd88a](https://github.com/n0connect/Yummi/commit/79cd88aa17a060027a71eeb500616b2be22892a2))
* **eino:** cap max_tokens against metered cloud providers ([6a54655](https://github.com/n0connect/Yummi/commit/6a54655083d9d8f1e9284558ed35074b70544abb))
* **executor:** scope-check URL-shaped MCP targets by host ([837a05a](https://github.com/n0connect/Yummi/commit/837a05ab7462d2f97ed4ce8635e87091877b0f2e))
* **lab-target:** give a reverse shell a route home, teach the agent its own IP ([519f52d](https://github.com/n0connect/Yummi/commit/519f52d4b64ee4e07c82fe33a044529db236b7de))
* **makefile:** add container daemon auto-bootstrap and robust error handling ([9b5db6f](https://github.com/n0connect/Yummi/commit/9b5db6f2f4c9f9faa51449c4e47e619bf70544af))
* resolve clippy derivable_impls on EgressMode and update version badge in README ([e80d607](https://github.com/n0connect/Yummi/commit/e80d6076b2e2b0f3c081c9b59fe32c43f5e91f6f))
* **sandbox-mcp:** format terminal_test.go with gofmt ([0c8e60d](https://github.com/n0connect/Yummi/commit/0c8e60d3aec66682f94c511e3894eb0562b7e909))
* **sandbox,lab:** build-safe desktop launchers, certipy alias, tun guard, lab RCE chain ([e9513cf](https://github.com/n0connect/Yummi/commit/e9513cfbc6644131b64abd7bb8b4dd43c713a94d))
* **sandbox:** fix bash motd banner alignment and suppress kali login notice ([732402e](https://github.com/n0connect/Yummi/commit/732402ec2e187085fae8e9287fb315a7d1b16cc9))
* **security:** exclude metasploit from known_c2_framework pattern ([1d5b75c](https://github.com/n0connect/Yummi/commit/1d5b75cc5b9cd01a3caea8454dee45da54a016fd))
* **terminal:** preserve spaces and special characters in term_keys ([8b2009a](https://github.com/n0connect/Yummi/commit/8b2009a21f4ca15afa21b08a6b478e7e9721cb10))
* **terminal:** preserve wrapped lines with tmux -J and prevent key name hijacking in commands ([4cd5904](https://github.com/n0connect/Yummi/commit/4cd59040d3c3a56360beb49f492771c61fb5542e))
* **terminal:** warn instead of silently garbling on a missed Enter ([f494c01](https://github.com/n0connect/Yummi/commit/f494c0174c96002a6d669587ac49154c2873551e))
* **ui:** add @types/node for vite.config.ts's Node globals ([bb9e980](https://github.com/n0connect/Yummi/commit/bb9e98000f6ea80a1715137d34ce12f804e69fbd))
* **ui:** align session rail layout and restore chat header symmetry ([8a3381f](https://github.com/n0connect/Yummi/commit/8a3381f29168904094e51383aa4fa761d070c167))
* **ui:** expand desktop hidden card to fill viewport and remove activate button ([2a1207b](https://github.com/n0connect/Yummi/commit/2a1207bd598b0eba36722d6dbf54ffe09fdffa46))
* **ui:** expand session rail width and fix search input overflow and glow styling ([8906f51](https://github.com/n0connect/Yummi/commit/8906f517709e8e3cf4ed4aa297d72d029e074fb3))
* **ui:** export full session, not just chat messages; fix rail width and persistence ([b52026f](https://github.com/n0connect/Yummi/commit/b52026f6ae943216995d8525f9e2f20f42b2a6e1))
* **ui:** hydrate tasks as task cards instead of user messages ([824b17c](https://github.com/n0connect/Yummi/commit/824b17caeac85e61b8d4373618e9fb592e82dcda))
* **ui:** preserve execution bubbles across tab navigation and fix timestamp alignment ([0c5bccd](https://github.com/n0connect/Yummi/commit/0c5bccd338617da6e87923c5175eb2715a52c16f))
* **ui:** respect .yummi.conf build language and set en as fallback ([3122838](https://github.com/n0connect/Yummi/commit/312283886a6628968a40bda4590b52d3f29b32dd))
* **ui:** show task goals and summaries when the thread is loaded ([a1e9e52](https://github.com/n0connect/Yummi/commit/a1e9e529ce34ad24c3cebdb06e7178949801c7e9))


### Performance Improvements

* **build:** implement incremental native and container builds in Makefile ([95ec95e](https://github.com/n0connect/Yummi/commit/95ec95e6a55c06705eae1b00ad70dc730fa856bf))

## [v1.0.1b] (2026-10-05)

### Features

* **gateway/egress-modes:** expanded runtime egress policy to 4 discrete modes: `block_all` (fail-closed, drops all outbound traffic), `block_all_except_rules` (default-drop, permits traffic matching active rules or configured scope), `allow_all` (blanket external egress), and `ask_every_time` (interactive operator sign-off per destination IP/hostname), with fail-closed reset to `block_all` on restart
* **ui/task-summary:** persistent `TaskSummaryMessage` completion cards in the chat timeline rendering status pills (`✓ COMPLETED`, `✕ FAILED`, `◼ STOPPED`), steps budget metrics, executive summary prose, structured findings with automatic CTF/pentest flag detection (`FlagChip` with 1-click copy interaction), CVE tagging, and direct `Trace ›` navigation
* **ui/desktop:** on-demand click-to-activate placeholder with eye icon for the XFCE/noVNC graphical desktop (`/desktop`), reducing idle RAM/VRAM consumption and automatically resetting to dormant on tab switch
* **ui/terminals:** introduced dedicated 8th tab (`/agent-terminals`) providing a live watch-only monitor and session inspector for background tmux sessions driven by the agent
* **ui/workspace:** expanded operator workspace from 7 to 8 tabs (`[AGENT] [TERMINALS] [TRACE] | [BASH] [DESKTOP] [LOGS] [GATEWAY] [HEALTH]`) with statusbar grouping
* **ui/chat-input:** bounded auto-expanding multiline chat input with Shift+Enter multiline support, Enter to send, and clean drag-and-drop file attachment
* **agent/steering:** mid-task operator guidance allowing chat messages during active runs to queue directly onto `Task.PendingGuidance` and inject `[OPERATOR GUIDANCE]` into the subsequent turn
* **sandbox/pwnbox:** transformed the unified Kali execution sandbox into **Yummi Pwnbox** (`yummi-pwnbox` hostname, tactical prompt, custom MOTD banner, and branded desktop wallpaper)
* **arsenal/metasploit:** integrated full `metasploit-framework` (`msfconsole`, `msfvenom`, meterpreter stagers/handlers) with desktop GUI launcher and build-time verification
* **arsenal/windows:** integrated `nishang` PowerShell framework with home and Desktop shortcuts, plus `kerbrute` in recontools for rapid Kerberos user enum and spraying
* **vpn/lab:** integrated `openvpn` with auto-created `/dev/net/tun` for HackTheBox, TryHackMe, Proving Grounds, and client lab `.ovpn` files, routed via gateway nftables rules
* **payloads/revshell:** built-in `/usr/local/bin/revshell [PORT]` auto-detecting `tun0`/`eth0` and outputting copy-paste payloads for 8+ languages
* **wordlists/shortcuts:** exported `$ROCKYOU`, `$COMMON_TXT`, `$BIG_TXT`, `$MEDIUM_TXT`, `$USERNAMES`, `$PASSWORDS_TOP` in `.bashrc`
* **mcp/sandbox:** expanded sandbox-resident MCP tool suite to **54 registered tools** across 4 tiers:
  - 6 individually-typed tools (`nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`)
  - 39 cataloged pentest tools including modern bug bounty & recon tools (`httpx_probe`, `dnsx_resolve`, `alterx_mutate`, `gau_fetch`, `katana_crawl`, `dalfox_scan`, `naabu_scan`, `tlsx_scan`, `arjun_scan`, `cloud_enum_scan`, `jwt_tool_inspect`, `subzy_scan`, `jsluice_scan`, `graphql_cop_scan`, `corsy_scan`, `screenshot_triage`, `feroxbuster_scan`, `commix_scan`, `whois_query`, etc.)
  - 2 pure-Go recon utilities (`consolidate_hosts`, `passive_recon_batch`) with zero external subprocess overhead
  - 7 persistent tmux terminal session verbs (`term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close`)
* **terminal/tmux:** agent-driven persistent tmux terminal sessions over MCP, enabling multi-terminal workflows, background listeners, interactive CLI prompts (`sqlmap`, `msfconsole`, `gdb`), and streaming commands inside the sandbox
* **scope/program:** program scope rules management (`program_scope_rules` table) supporting glob/wildcard patterns, in-scope/out-of-scope classification, and automated filtering of out-of-scope findings during pentest report generation
* **reporting:** multi-format security report export supporting HackerOne Markdown, Bugcrowd VRT, Executive Summary, and structured JSON
* **ui/trace:** comprehensive Trace tab expansion with Attack Surface Management (ASM) Asset Tree, Findings drawer, Program Scope rules editor, and Proxy Bridge panel
* **proxy:** proxy traffic ingestion bridge for Burp Suite, Caido, and Mitmproxy HTTP flows, populating the asset map directly from intercepted traffic
* **ui/theme:** light and dark mode theme engine with runtime toggle, persistent preferences, and CSS custom property tokens
* **ui/assistant:** live command execution rationale rendering in the Assistant panel and task execution history
* **sandbox/unification:** unified single hardened Kali sandbox container sharing the runtime between agent `docker exec`, operator Bash terminal (`tmux`), and desktop GUI (`noVNC`/XFCE), running unprivileged as UID 1000
* **eval/lab:** two-stage exploitation target lab (`lab-target/`, `198.51.100.10`) featuring Apache 2.4.49 path traversal RCE (CVE-2021-41773) chained with GTFOBins SUID privilege escalation (`make lab-target-up` / `make lab-target-down`)
* **agent/eino:** per-task auto-approval toggle (`AutoApproveNonCritical`), `UnknownToolsHandler` self-recovery for hallucinated tool names, non-fatal `TOOL_TIMEOUT` recovery, and real-time step activity instrumentation (`RecordActivity`)

### Bug Fixes & Hardening

* **ui/hydration:** fixed chat history hydration in `AssistantPanel.tsx` ensuring `task_summary` messages retain structured findings, step counts, and flags across page refreshes and session switching
* **ui/accordion:** implemented controlled React state on execution details accordion (`ExecutionMessage`) to prevent collapse flickers and blank states during running and finished transitions
* **sandbox/init:** enabled container `init: true` to eliminate zombie process accumulation from repeated `docker exec` sessions
* **terminal/race:** resolved sentinel write races in `term_run` via unified multiline writes (`sendLines`)
* **terminal/concurrency:** added per-session mutex locking (`sync.Map`) to serialize concurrent typing collisions on the same terminal
* **terminal/reconnect:** implemented single-flight reconnection logic in `sandboxmcp.Client` to prevent cascading connection drops and process leaks
* **terminal/idempotency:** ensured `term_open` is idempotent to handle transient network retries without failing existing sessions

## 1.0.0 (2026-08-16)


### Features

* add agent approval mode toggle, remove header pulse dot ([ba71af2](https://github.com/n0connect/Yummi/commit/ba71af2665daddef6b4cf643c1426f51eaf96af9))
* add semantic retrieval and safe auto-skill-creation ([673193a](https://github.com/n0connect/Yummi/commit/673193ad55ae421761c4abeddcc17d0d3528b952))
* **agent/eino:** 5 control tools reusing the graph engine's security primitives ([ea9bf1e](https://github.com/n0connect/Yummi/commit/ea9bf1ea59851a9dc4de9a3fc8cf6c8463b3b48e))
* **agent/eino:** add search_pentest_tools + run_pentest_tool generic dispatch ([6e0777f](https://github.com/n0connect/Yummi/commit/6e0777fb8c80f42c1bedffce5c9073f39447a9b3))
* **agent/eino:** EinoRunner satisfying agent.TaskRunner ([3af2829](https://github.com/n0connect/Yummi/commit/3af2829164a7b916385696316515e2925cea3ef6))
* **agent/eino:** generic pentestTool with nmap_scan wired ([691ed8b](https://github.com/n0connect/Yummi/commit/691ed8b53bd982006ec8fe7f974efbc99f9d616f))
* **agent/eino:** Ollama-backed ChatModel constructor ([c42ea2d](https://github.com/n0connect/Yummi/commit/c42ea2d37f31c517f62966c905213c58c3f8937e))
* **agent/eino:** wire all six pentest tools + instruction update ([14eb3e9](https://github.com/n0connect/Yummi/commit/14eb3e9191f5b66574cd681371ca1308fd4faf0b))
* **agent/eino:** wire search_pentest_tools/run_pentest_tool + 3-tier instruction ([bff8101](https://github.com/n0connect/Yummi/commit/bff81013358e8bf976216ac37dc80a54f38279dc))
* **agent:** activity event model + store persistence + pub/sub ([5130ef1](https://github.com/n0connect/Yummi/commit/5130ef1a505f6f358c89c3d6052498f9bfd82d14))
* **agent:** add MCPExecer interface + StoreMCPExecer ([49c7dbb](https://github.com/n0connect/Yummi/commit/49c7dbb3506b547a9ee774e714cf425b67e4fd2b))
* **agent:** emit legible activity events from the graph ([38829ac](https://github.com/n0connect/Yummi/commit/38829ac086feef5a86376253384ae5abe767a7de))
* **agent:** inject MEMORY block into step prompt via retrieval ([f1701cc](https://github.com/n0connect/Yummi/commit/f1701cc6a415444445b12ca29c17f3ca61d799d4))
* **agent:** nest sub-agent activity under parent task ([fc8ad3e](https://github.com/n0connect/Yummi/commit/fc8ad3e07cfe78db5d0cd4ce11980aa40a81d558))
* **agent:** parallel HANDOFF fan-out to multiple sub-agents ([0ac46ed](https://github.com/n0connect/Yummi/commit/0ac46ed96e288a7f93d7d0f153f6993e41adc772))
* **agent:** record structured findings to knowledge (graceful contract) ([e81cd31](https://github.com/n0connect/Yummi/commit/e81cd31b382a882748e92286afcabba77ceb2c83))
* **agent:** REMEMBER action persists code maps to knowledge context ([7b97b6f](https://github.com/n0connect/Yummi/commit/7b97b6f2c05c0551bebf6303f463dd3838e713b2))
* **agent:** skills injection + real sub-agent handoff (HANDOFF tag/node, depth-1) ([7d79a46](https://github.com/n0connect/Yummi/commit/7d79a46e137f73214d75e28fe5782d2831ff20e5))
* **agent:** tag gateway scope rejections as COMMAND_BLOCKED at execution time, not pre-execution ([b987e77](https://github.com/n0connect/Yummi/commit/b987e7794ca4cdcc897d7f406c702bfa0cfdae34))
* **api:** broadcast agent.activity over the events websocket ([82de782](https://github.com/n0connect/Yummi/commit/82de7822b6b2a807dc00f1cc4acbaa646e7a641e))
* **api:** read endpoints for activity, tactics, context, skills, roles ([a52f16a](https://github.com/n0connect/Yummi/commit/a52f16af51bca2ca0431697884da64c185fd4520))
* **ci:** sandbox-mcp coverage, cargo clippy, and vulnerability scanning ([8878139](https://github.com/n0connect/Yummi/commit/8878139f54cfce92e052efb571ef6567ee1ed349))
* **cli:** introduce Terminal UI and re-architect Makefile dependencies/logging ([e2f9960](https://github.com/n0connect/Yummi/commit/e2f9960b99ff06dba5b57686176a96c28608b5e1))
* **conductor:** remove scope-decision logic from Validator — gateway is now the sole authority ([77d0af5](https://github.com/n0connect/Yummi/commit/77d0af5fe9c0fb0a1cb7fef07c1df5aa60a6ef09))
* **conductor:** TierOverrides for custom Ollama models ([b0efd54](https://github.com/n0connect/Yummi/commit/b0efd543f5849feb295f33bad508bd0fa54ea160))
* **config,retention:** custom-model overrides + opt-in data retention ([f112a2a](https://github.com/n0connect/Yummi/commit/f112a2a9001590e7b25301895661abd063a350bb))
* **desktop:** add the Tauri desktop shell and make the UI environment-aware ([d1ce8cb](https://github.com/n0connect/Yummi/commit/d1ce8cbb0d06098eadcf6d4e6e143821ec2c6331))
* **doctor:** add trust-boundary canary check ([6f29dde](https://github.com/n0connect/Yummi/commit/6f29dde413ba5db08956c3eda0c6a20e7e67bfad))
* **eino:** full agent/memory/skill/sub-agent parity with graph backend ([563e5d4](https://github.com/n0connect/Yummi/commit/563e5d40b8155fcc4c918f076a229f23c7801437))
* **execution:** add MCPTool/MCPArgs to Record and Store.EnqueueMCP ([c40c58e](https://github.com/n0connect/Yummi/commit/c40c58e3aaf0f9536f9d7234b5fb3ce5b6ed873a))
* **execution:** hold threat-flagged assistant output for operator approval ([5f54aad](https://github.com/n0connect/Yummi/commit/5f54aad7b168dda6245a4476e825b840670ef8e8))
* **executor:** dispatch MCP-tagged execution records through sandboxmcp ([5d6d87e](https://github.com/n0connect/Yummi/commit/5d6d87e812e820c293725fdab08187c0f9d1ff12))
* **executor:** record target-scrubbed procedural tactics on execution ([05df233](https://github.com/n0connect/Yummi/commit/05df2332639c837240b28da1a5d2589384375b34))
* expose fingerprint-masking config read-only in the gateway management UI ([de5076f](https://github.com/n0connect/Yummi/commit/de5076f388b45dccd5f3ba23383dbfca5e57f66d))
* **gateway:** add ListRules/GetAuditLog/AddManualRule IPC protocol variants (stub dispatch) ([905fcc8](https://github.com/n0connect/Yummi/commit/905fcc81d5fe9db236ca989a876bc191984d0916))
* **gateway:** add operator-controlled egress mode (block/allow/ask) with check_egress verdict and nftables blanket-accept ([679ba26](https://github.com/n0connect/Yummi/commit/679ba26dcab3330681008bd02d966f5d75195a32))
* **gateway:** add ttl_normalize and mac_randomization to NetworkConfig ([8bdeded](https://github.com/n0connect/Yummi/commit/8bdededacd9b838b052ea8454a98445808d6307e))
* **gateway:** add unconditional safety floor blocking loopback/multicast/link-local/unspecified ([f16233e](https://github.com/n0connect/Yummi/commit/f16233e94b23f7f2fe98086b8790cd61279d4f6b))
* **gateway:** audit rule_added/rule_removed lifecycle events, add recent_rows tail read ([0910fe1](https://github.com/n0connect/Yummi/commit/0910fe128c943739246988262beead5a80931c65))
* **gateway:** deny-all default, Cloudflare hostname resolution, scope-check audit ([0055dcd](https://github.com/n0connect/Yummi/commit/0055dcd5033e268df2347d5aea261ddcb3b4b08d))
* **gateway:** deny-all default, Cloudflare hostname resolution, scope-check audit ([d3b4757](https://github.com/n0connect/Yummi/commit/d3b4757cba2b173dba588c00d0d6c56aef4a37e5))
* **gateway:** hostname allow/block scope policy with wildcards ([9da618e](https://github.com/n0connect/Yummi/commit/9da618e24935e71e831a5294d711d38f3ea61dfa))
* **gateway:** normalize sandbox-originated packet TTL when configured ([ac2f82f](https://github.com/n0connect/Yummi/commit/ac2f82f616bc7e1681227cbe55d6b3bfe8f821a6))
* **gateway:** randomize egress interface MAC at startup when configured (best-effort) ([fce8f86](https://github.com/n0connect/Yummi/commit/fce8f860beafaf7eb1d216b5b18ce67d1a76bf77))
* **gateway:** tag PolicyRule with source (config/agent/manual), add list_rules + add_manual_allow ([3e349f7](https://github.com/n0connect/Yummi/commit/3e349f7e71b372b034ebd37258a35c8e5403b004))
* **gateway:** wire ListRules/GetAuditLog/AddManualRule IPC handlers, audit all rule lifecycle events ([a097adb](https://github.com/n0connect/Yummi/commit/a097adbaf7c7c06b2b5f2a689a1e4d55a6d57962))
* implement docker socket hardening, ipv6 egress support, TOCTOU IP-pinning, and ThreatScan approvals ([c6f81de](https://github.com/n0connect/Yummi/commit/c6f81de46be8c7b72284107fc96982cc49bf317f))
* implement persistent memory, subagents, and remove UI mocks ([6684a6f](https://github.com/n0connect/Yummi/commit/6684a6f789a9d897c5733bb634b8087b58865991))
* **knowledge:** leaf store for targets + findings with provenance ([257e799](https://github.com/n0connect/Yummi/commit/257e79948829e55dd65032cdda70ce2f6425ab1f))
* **knowledge:** RetrievalStrategy interface + StructuredRetrieval ([73e7898](https://github.com/n0connect/Yummi/commit/73e7898195e5aea9a612fd5459b269e4b9f3ae22))
* **knowledge:** target-scrubbed procedural memory with dedup ([3ac970d](https://github.com/n0connect/Yummi/commit/3ac970dea2a6f873d75dd30f8cff2d7a353ca51d))
* mature agentic runtime capabilities ([9ac3aa6](https://github.com/n0connect/Yummi/commit/9ac3aa68e87bfc821c53d89fd6ba317139f56571))
* **ops:** container resource limits and config plumbing for custom models/retention ([a798348](https://github.com/n0connect/Yummi/commit/a7983485d0c10a8c7f66782f28b0c209b99b8883))
* **orchestrator:** add gateway.Client.ListRules/GetAuditLog/AddManualRule ([ca72bb0](https://github.com/n0connect/Yummi/commit/ca72bb055dc24395015df446f873b463d4ba8bb1))
* **orchestrator:** add REST handlers for gateway rule management and audit log ([d0b2c5b](https://github.com/n0connect/Yummi/commit/d0b2c5b1179a58a8c9e813b079745de434d68722))
* **orchestrator:** add sandboxmcp client + per-tool target/render catalog ([3e92ee3](https://github.com/n0connect/Yummi/commit/3e92ee3d306a6533db7c36a63064b2413b9b39e3))
* **orchestrator:** broadcast gateway.rules_changed on rule snapshot diff ([57d47be](https://github.com/n0connect/Yummi/commit/57d47bea54b2c4b38b4d8872cb9a497c05e900b2))
* **orchestrator:** egress-mode client/REST + RunScopedCommand verdict routing (allow/block/needs_approval) ([78c4d18](https://github.com/n0connect/Yummi/commit/78c4d1819910260a54e451e7cd410d4ed7e1a1b1))
* **orchestrator:** extract executor.RunScopedCommand as the single gateway-scope-checked exec path ([79837be](https://github.com/n0connect/Yummi/commit/79837be542550cab8c8718d7780ffbde4b09f44d))
* **orchestrator:** mirror gateway's new IPC wire types (RuleView, AuditRowView) ([c5abd37](https://github.com/n0connect/Yummi/commit/c5abd373f6be1a074c8542362ae950cddeb5dc02))
* **orchestrator:** register gateway REST routes, forward gateway.status over /ws/events ([548f27e](https://github.com/n0connect/Yummi/commit/548f27e445104bd2d9477abf7942003b2fbae92d))
* **orchestrator:** select agent backend via YUMMI_AGENT_BACKEND ([9585c6f](https://github.com/n0connect/Yummi/commit/9585c6f9f1fd952a473667950a8e89aa4adc5987))
* **orchestrator:** wire gateway client into health.Aggregator for rule-change broadcasting ([15af5f2](https://github.com/n0connect/Yummi/commit/15af5f22e73890bbe34f44813dea9eed67f9afe7))
* **orchestrator:** wire sandboxmcp client into executor and the Eino backend ([26e3505](https://github.com/n0connect/Yummi/commit/26e35056886e9ad4172b79e80436d47794afe860))
* persistent chat history, egress ask round-trip, sub-agent approval routing ([4d231be](https://github.com/n0connect/Yummi/commit/4d231becd96abb9a106eb63a7bd9d9c0c3b32eec))
* persistent chat history, egress ask round-trip, sub-agent approval routing ([ce16ea7](https://github.com/n0connect/Yummi/commit/ce16ea75dcc5975480729d46f8879fe0532a7d16))
* **persona:** expand sub-agent roster to 6 roles, add routing hints ([63fabb5](https://github.com/n0connect/Yummi/commit/63fabb595779dada4208ec4429fb971978f0061f))
* **release:** automated versioning via release-please + SECURITY.md ([5a87cef](https://github.com/n0connect/Yummi/commit/5a87cefb1bd538e59493dde5dec295e3c44dcc45))
* runtime hostname allow/block editing + gateway-tab UI panel ([1ecc470](https://github.com/n0connect/Yummi/commit/1ecc470a3612d8a05dcb499ebfd962246be89150))
* runtime hostname allow/block editing + gateway-tab UI panel ([da7e1b2](https://github.com/n0connect/Yummi/commit/da7e1b2c70d21094da2cd1c67ac3a1fba942c40f))
* **sandbox-mcp:** add amass, subfinder, theharvester, dnsenum, enum4linux, onesixtyone, wafw00f, wpscan, nuclei, sqlmap, hydra, medusa, dirb ([a548400](https://github.com/n0connect/Yummi/commit/a5484004cd4fa34469a0ee525739087873619b56))
* **sandbox-mcp:** add snmpwalk_enum to the pentest tool catalog ([ecd7f47](https://github.com/n0connect/Yummi/commit/ecd7f47c118d9c600b6c8917beeb5c54127c8d9a))
* **sandbox-mcp:** add whatweb, curl, gobuster, ffuf, nikto tools ([40598a5](https://github.com/n0connect/Yummi/commit/40598a5d2344acd5c22e180a5331c7b27413eeb8))
* **sandbox-mcp:** scaffold standalone MCP server module with nmap_scan ([5f58640](https://github.com/n0connect/Yummi/commit/5f58640960180a31f621e08bacfc9ac745671700))
* **sandboxmcp:** add Client.ListTools for the search-then-dispatch pattern ([9107e97](https://github.com/n0connect/Yummi/commit/9107e978a406ad7a13268c4114de436a7f057812))
* **sandboxmcp:** extend catalog with target/render for the 13 new tools ([50e8ae7](https://github.com/n0connect/Yummi/commit/50e8ae7ee68b8bc43f08db51ca0c8fcde29dfeea))
* **security:** add destructive-command risk floor, override auto mode ([927be6d](https://github.com/n0connect/Yummi/commit/927be6d91f8288d2508c12d363eb338387e4520e))
* **security:** architectural fitness tests, ANSI stripping, output cap, vuln scan ([5d1cde3](https://github.com/n0connect/Yummi/commit/5d1cde37d6fb49ce931a0c77c0141bd3d2d88306))
* **security:** content-layer threat scanning, LLM resilience, WS backoff ([da382d0](https://github.com/n0connect/Yummi/commit/da382d08a7160f8e1c44c621311fdd276ac559c4))
* semantic retrieval + safe auto-skill-creation ([e23757c](https://github.com/n0connect/Yummi/commit/e23757c16557ef80ec9469f1eaecdb011bed0013))
* **skillcurator:** dedup drafts against live registry and pending queue ([7ae8faf](https://github.com/n0connect/Yummi/commit/7ae8fafc66aa1052e8799de6569e4bcbae781d1f))
* **skill:** declarative capability modules (load/select/render), 3 skills ([a29628b](https://github.com/n0connect/Yummi/commit/a29628b7e934f8a87fb7e1350cf4ac9527992ad6))
* **tui:** complete the LLM↔Agent control surface + framed console ([bcfb496](https://github.com/n0connect/Yummi/commit/bcfb496ade10bd7a9af47ee6dd66d134c228f555))
* **tui:** interactive egress-approval holds, matching web's gateway Ask mode UI ([91a146c](https://github.com/n0connect/Yummi/commit/91a146cf8cbd28e71592acb41050c579c408e08d))
* **tui:** rewrite into a working operator console (correct API + live activity) ([1b53726](https://github.com/n0connect/Yummi/commit/1b5372672c156a5a35050c1b07e65bd3da0528db))
* **ui:** activity event types + websocket dispatch ([021b934](https://github.com/n0connect/Yummi/commit/021b9346a5198a31d78ad681cd5b55e21e315891))
* **ui:** add egress-mode selector (block/allow/ask) to the GATEWAY tab ([26394fa](https://github.com/n0connect/Yummi/commit/26394fadcaf180f6c524da151bd10e5cfdc79141))
* **ui:** add GATEWAY tab with rule management and audit log viewer ([093b078](https://github.com/n0connect/Yummi/commit/093b0781d79d90897e704be09d8f95008267d2a7))
* **ui:** build-time TR/EN internationalization ([e7b8c80](https://github.com/n0connect/Yummi/commit/e7b8c8024097f990ef3b02a11487fd23076f107b))
* **ui:** inline agent activity timeline (memory/skill/finding/handoff chips) ([7f30a05](https://github.com/n0connect/Yummi/commit/7f30a050b9c2adc31fa0bad7a6b74ff2e3313853))
* **ui:** TRACE tab — live activity trace + browsable world model ([2888667](https://github.com/n0connect/Yummi/commit/2888667b20f80d95c096590a5e96fa0f5364cf0c))
* wire knowledge store through orchestrator startup ([ef50ac3](https://github.com/n0connect/Yummi/commit/ef50ac36fc6ab4bd1e36d08dbe8116b8488ac830))
* wire skills + sub-agents (recon, web) through orchestrator startup ([c91e161](https://github.com/n0connect/Yummi/commit/c91e1611a2a9e9c44319515d25327628e3500228))


### Bug Fixes

* **agent/eino:** CreateAndPlan reaches Planned status, run_command surfaces its approval gate on the Task, docker-compose passes YUMMI_AGENT_BACKEND through ([93abe59](https://github.com/n0connect/Yummi/commit/93abe59d0a6137b8e25f06b93d0705932f049124))
* **agent/eino:** strip rationale/risk_level before the MCP call ([2ab5001](https://github.com/n0connect/Yummi/commit/2ab5001af2f1b90527b482577dee7fcc3f6a07e9))
* **agent:** aborted long-running commands also left their plan step frozen ([dc56e18](https://github.com/n0connect/Yummi/commit/dc56e189310ab1a8236a90bf72726b0b75fb697a))
* **agent:** ask_operator resume was a dead end — operator's answer never reached the model ([257c2e0](https://github.com/n0connect/Yummi/commit/257c2e0ba81faae572d6205b421c5f24ffed8728))
* **agent:** break the command-loop reinforcement in LLM↔agent comms ([ab645f2](https://github.com/n0connect/Yummi/commit/ab645f2c1f9d2002c15cb945264de389b0dd38df))
* **agent:** finished tasks could report findings from a tool that never completed ([ef8c77a](https://github.com/n0connect/Yummi/commit/ef8c77aabaa0d0857223370b18c6ee03cacc49d2))
* **agent:** plan-step tracking degrades gracefully, warn against unexecuted findings ([637af1e](https://github.com/n0connect/Yummi/commit/637af1ececf2d95908fb233f6038bb6f8344394c))
* **agent:** sanitize model-authored text before it reaches any client ([c10a527](https://github.com/n0connect/Yummi/commit/c10a527104cf5064db776643b15a7c266b342d23))
* **agent:** Store.Get aliased Task.Plan's backing array with in-place mutators ([bde8556](https://github.com/n0connect/Yummi/commit/bde855643043960766838dca9be13b8f9daf2537))
* **ci:** resolve golangci-lint v1/v2 config mismatch and 4 real ESLint errors ([9763672](https://github.com/n0connect/Yummi/commit/976367232c114d582ea13789a0f66c73a80c0b71))
* **conductor:** strip hidden reasoning and protocol tags from chat-visible text ([038af18](https://github.com/n0connect/Yummi/commit/038af18f2a3bc7f7883e13ef5ae9196d8e0fd085))
* **conductor:** wire up dropped persona character block, fix shell-word risk regex ([53a6cbc](https://github.com/n0connect/Yummi/commit/53a6cbcc0ce03a901a6621dd75f109561ff8c1ad))
* **conductor:** wire up dropped persona character block, fix shell-word risk regex ([98948c6](https://github.com/n0connect/Yummi/commit/98948c6e24ed483f5f8bc6949f51896314362185))
* correct three setup/behavior bugs found while writing docs/ ([6148cb4](https://github.com/n0connect/Yummi/commit/6148cb43bbf7d4925f355337f7b7b155d419017f))
* **deps:** resolve x/text, goldmark, and chi/v5 vulnerabilities ([8a717c4](https://github.com/n0connect/Yummi/commit/8a717c40dfec34169e0eec7b777adfbcb87923dd))
* **executor:** surface stderr on a failed MCP tool call ([7bb7652](https://github.com/n0connect/Yummi/commit/7bb765279ee91e94c3930122b82c316ef20a1d18))
* gateway Block-mode airtightness, risk-classification quoting, sub-agent mode propagation, content-layer scan gaps ([c9c6603](https://github.com/n0connect/Yummi/commit/c9c6603984580c9a9555aaf892d612a0829f5b8c))
* **gateway:** apply configured Unix socket permissions on the IPC listener ([1033007](https://github.com/n0connect/Yummi/commit/1033007fcf0040f22dc94ca410300e5c128d1090))
* **gateway:** bump anyhow to patch RUSTSEC-2026-0190 ([dde3836](https://github.com/n0connect/Yummi/commit/dde38366024cf9ac5551a44fed0d76264879b75b))
* **gateway:** check conn.Close error in test helper (pre-existing lint) ([b4ca8ee](https://github.com/n0connect/Yummi/commit/b4ca8ee96e7f3920b3742eb8dcf537042ad5d813))
* **gateway:** make Block egress mode airtight, remove dead observe_only config ([703731c](https://github.com/n0connect/Yummi/commit/703731c2cf967e46675c7178ed156e8917568d9a))
* **gateway:** manual allow rules were completely inert against a broader block ([9fca4f3](https://github.com/n0connect/Yummi/commit/9fca4f31919e37c7fdcb50b813825f00c7cfa1cb))
* **gateway:** non-IP targets (hostnames) get a mode-level egress verdict instead of an 'invalid IP' error ([8317c6d](https://github.com/n0connect/Yummi/commit/8317c6deeed0c3f0f26fcf159242f5bd3cce0d99))
* **knowledge:** semantic retrieval no longer pads results with irrelevant noise ([1d5f908](https://github.com/n0connect/Yummi/commit/1d5f908e94f4c42e2407747e77332f32ba6e84b6))
* **knowledge:** semantic retrieval now respects outcome, not just similarity ([f29a4cf](https://github.com/n0connect/Yummi/commit/f29a4cf8b10697eaf335caaaeefa3dd90c078c9f))
* makefile build issue ([29f094a](https://github.com/n0connect/Yummi/commit/29f094a100ad03c42f73ea606aada35f7fe8a4d7))
* **makefile:** unset VITE_YUMMI_LANG for _test-ui ([6e4cbc5](https://github.com/n0connect/Yummi/commit/6e4cbc52cef8f1314f778a63ca998245c93df978))
* nginx name conflicts ([b241926](https://github.com/n0connect/Yummi/commit/b241926f8ebf83ec85efb7630ae70e8db33f0a11))
* **orchestrator:** bump Docker builder to golang:1.25-bookworm ([477b52b](https://github.com/n0connect/Yummi/commit/477b52b7d1eac678980e01aa7edb82118b3b6b49))
* **orchestrator:** correct risk-classification quoting, propagate approval mode to sub-agents, scan tactics and handoff summaries, fix buildState race ([8274995](https://github.com/n0connect/Yummi/commit/8274995c6fa9e7a2b0e5b40781d5ae237deb712a))
* **orchestrator:** docker health check honors DOCKER_HOST after socket-proxy hardening ([3e51b1e](https://github.com/n0connect/Yummi/commit/3e51b1ec5755838f0a52178f9e3b5144fc9486ed))
* **orchestrator:** route direct-exec API through executor.RunScopedCommand, fixing missing nftables unlock ([ccaa032](https://github.com/n0connect/Yummi/commit/ccaa0324c45b505f87c57d9c1837231e6e1ae7ab))
* **persona:** egress is gateway-decided per operator mode — stop model over-refusing/misreporting as 'no permission' ([e87fae1](https://github.com/n0connect/Yummi/commit/e87fae1884ef4208b25e3772e49fc1ffcca4bb04))
* **release:** target main explicitly, not the repo's default branch ([5d962ee](https://github.com/n0connect/Yummi/commit/5d962ee06ba6361e38352a45dffe8667eea51814))
* relocate approval-mode UI, audit hardcoded config, rename stale n0/n1/p* refs ([4ab98ff](https://github.com/n0connect/Yummi/commit/4ab98fff80579a9e64ef3c3faf118ff281543819))
* **sandbox-mcp:** bump Go toolchain to 1.26.6, patches stdlib CVEs ([7a62e09](https://github.com/n0connect/Yummi/commit/7a62e0999d300d069287605f74dfa52666333997))
* **sandbox-mcp:** wpscan_scan was completely non-functional in production ([a97c5c2](https://github.com/n0connect/Yummi/commit/a97c5c293fc870ec77bd067dc8680c1baf35da43))
* scan skill Name for threat patterns, close skill-approval TOCTOU race ([7748924](https://github.com/n0connect/Yummi/commit/774892440e4626c432f7dc239ab32c7ec3120d91))
* scan skill Name for threat patterns, close skill-approval TOCTOU race ([fded616](https://github.com/n0connect/Yummi/commit/fded61681a4b64469055cea9f38e4eae83ed0400))
* **skill:** add json tags so /skills returns lowercase keys for the UI ([a2c254c](https://github.com/n0connect/Yummi/commit/a2c254c972b354552c233b7487f11e227f6bc4a7))
* tighten docker-proxy permissions and IPv6 default-block parity ([3262339](https://github.com/n0connect/Yummi/commit/3262339e83551df6d2540a7e12c30b753f76f1f6))
* **tui,web:** chat-history load bug, gateway/skills TUI parity, sub-agent step budget ([a51fa18](https://github.com/n0connect/Yummi/commit/a51fa188fe12df88fa54271f876d143151b5abbc))
* **tui:** close behavior gaps vs web UI and fix input/viewport bugs ([2f72a2b](https://github.com/n0connect/Yummi/commit/2f72a2b6d6c51dcc2cd58ca060d2fb1f2c0feef0))
* **tui:** close behavior gaps vs web UI and fix input/viewport bugs ([dba86d0](https://github.com/n0connect/Yummi/commit/dba86d033005a94a1432f2c7ac24e469752a523b))
* **tui:** stop terminal escape-sequence injection from tool output ([bd57391](https://github.com/n0connect/Yummi/commit/bd5739184cf002eff29e3ab6db92ed8700da721b))
* **ui:** make execution output a real expand/collapse and split the running cue ([67a931e](https://github.com/n0connect/Yummi/commit/67a931ea3a531d3296e1934d3fbbf6d6ff0e3f97))
* **ui:** stop cross-session data leaks and sanitize model text client-side ([92b8df6](https://github.com/n0connect/Yummi/commit/92b8df6cc53756146725a6ab23596a1ee3bf15b9))
* **web:** SessionRail dialog and toggle button no longer share an aria-label ([8b39bbe](https://github.com/n0connect/Yummi/commit/8b39bbecf52e811b2ab3ffb1abd4f6c7e354782e))
