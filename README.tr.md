<p align="center">
  <a href="https://github.com/n0connect/yummi">
    <img src="ui/src/shared/assets/yummi_logo.svg" alt="Yummi logo" width="300">
  </a>
</p>

<p align="center">Gizlilik öncelikli, fail-closed (arıza durumunda korumalı) bir yapay zeka sızma testi yürütücüsü.</p>

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
Yummi, trafik kontrolünü ve operatör yetkisini açık tutarak doğal dili pratik güvenlik iş akışlarına dönüştürür. Otonom bir Kırmızı Takım (Red-Team) beyni, sıkılaştırılmış ve ağdan izole edilmiş bir Kali sandbox'ını yönetir; bellek güvenli bir Rust güven sınırı ise yapay zekanın izin verilmeyen hiçbir hedefe dokunamamasını sağlar.
</p>

---

## İçindekiler

- [Yummi Nedir?](#yummi-nedir)
- [Sistem Mimarisi](#sistem-mimarisi)
- [Güven Sınırı (En Önemli Tek Değişmez)](#güven-sınırı)
- [İçerik Katmanı Güvenliği](#icerik-katmani-guvenligi)
- [Ajan Motoru](#ajan-motoru)
  - [Graf Durum Makinesi — Düğümler ve Kenarlar](#graf-durum-makinesi)
  - [Etiket Protokolü](#etiket-protokolu)
  - [Beyinden Bağımsız Çekirdek](#beyinden-bagimsiz-cekirdek)
- [Bilgi Deposu (Knowledge Store) — İki Katmanlı Bellek + Öğrenme Döngüsü](#bilgi-deposu)
- [Yetenekler (Skills) — Bildirimsel Tam Zamanında Yol Haritaları](#yetenekler)
- [Alt Ajanlar — Sınırlı Yetkilendirme](#alt-ajanlar)
- [TRACE — Ajan Gözlemlenebilirliği](#trace-gozlemlenebilirligi)
- [Yürütücü, Doğrulayıcı ve Egress Modları](#yurutucu-dogrulayici-ve-egress-modlari)
- [Geçit (Gateway - Rust Güven Sınırı)](#gecit)
- [Sandbox](#sandbox)
- [Arayüzler — Web UI ve TUI](#arayuzler)
- [Hızlı Başlangıç](#hizli-baslangic)
- [Geliştirici İş Akışı](#gelistirici-is-akisi)
- [Yapılandırma](#yapilandirma)
- [Depo Düzeni](#depo-duzeni)
- [Güvenlik Değişmezleri](#guvenlik-degismezleri)

---

## Yummi Nedir? <a id="yummi-nedir"></a>

Yummi, kesin bir kural etrafında inşa edilmiş **dört bileşen ve bir alternatif arayüzden** oluşur: *Yapay zeka önerir, geçit (gateway) uygular.* Yapay zekanın çalıştırdığı her sızma testi komutu tam olarak tek bir yerden geçer ve sistem çökmelerine karşı dayanıklı, fail-closed bir ağ politikası tarafından denetlenir.

```
Kullanıcı / Operatör  →  Orkestratör (Go)  →  Geçit (Rust, IPC)  →  Sandbox (Kali Docker)
   Web UI / TUI          yönetici + ajan      fail-closed nftables   yalnızca araç çalıştırma
                         bilgi + API          politika motoru + denetim
```

- **orchestrator** (Go) — Yapay zeka yürütücüsünü, ajan tabanlı graf motorunu, bilgi deposunu ve REST/WebSocket API'sini yönetir. nftables kurallarına asla doğrudan dokunmaz.
- **gateway** (Rust, binary `yummi-gateway`) — Sandbox'tan dış dünyaya trafik açabilen tek bileşendir. Başlangıçta tüm trafiği engelleyen (`DROP`) nftables kuralları yükler; çökerse, bu kurallar korunur (**fail-closed**).
- **sandbox** (Kali Docker) — Salt okunur dosya sistemi, root olmayan kullanıcı, yalnızca `NET_RAW`/`NET_ADMIN` yetkileri. Pasiftir: Sadece orkestratör içinde `docker exec` çalıştırdığında harekete geçer.
- **ui** (React 18 + Vite) — `:3000` portundaki web paneli; `/api/` ve `/ws/` isteklerini `:9080` portundaki orkestratöre yönlendirir.
- **TUI** (Charm Bubble Tea) — Tamamen terminal tabanlı bir operatör konsoludur (`yummi --tui`), aynı REST/WS API'sini kullanır.

---

## Sistem Mimarisi <a id="sistem-mimarisi"></a>

```text
        ┌──────────────────────────────────────────────────────┐
        │            Web Paneli      ·      Terminal TUI       │
        └───────────────────────────┬──────────────────────────┘
                                    │  REST + WebSocket · :9080
        ┌───────────────────────────▼──────────────────────────┐
        │ ORKESTRATÖR · Go                                     │
        │                                                      │
        │   API ─▶ yönetici ─▶ ajan grafı ─▶ yürütücü          │
        │                          │                           │
        │                          ▼  (ortak dünya modeli)     │
        │            Bilgi Deposu: bulgular · bellek           │
        │                          · bağlam · TRACE            │
        └───────────────────────────┬──────────────────────────┘
                                    │  Unix soketi · uzunluk önekli JSON IPC
        ┌───────────────────────────▼──────────────────────────┐
        │ GEÇİT (GATEWAY) · Rust güven sınırı                  │
        │ fail-closed nftables · çıkış · denetim · watchdog    │
        └─────────────┬────────────────────────────┬───────────┘
          docker exec │  ◀── TEK geçiş yolu         │ kontrollü çıkış (egress)
        ┌─────────────▼───────────┐      ┌──────────▼───────────┐
        │ SANDBOX · Kali          │      │  Harici hedefler     │
        │ salt okunur · non-root  │      │ (geçit politikasıyla)│
        └─────────────────────────┘      └──────────────────────┘
```

Sinyal düz bir hat üzerinden akar — operatör → API → yönetici → ajan → yürütücü → geçit → sandbox. `docker exec`, geçidin kapsam kontrolünün ardından güvenli bölgeden sandbox'a giden **tek yoldur**; sandbox dış dünyaya *sadece* geçidin nftables kuralları aracılığıyla ulaşır. Bilgi Deposu, ajanın ortak dünya modeli olarak ajan yapısına bağlıdır.

### Ağ Topolojisi

```
yummi_internal (172.20.0.0/24): sandbox .10 · geçit .2 (aynı zamanda DNS) · orkestratör .3 · ui .4 · docker-proxy .5
yummi_external:                 yalnızca geçit · orkestratör · ui — sandbox kasıtlı olarak bu ağda yer almaz.
                                Dışarıya tek çıkış yolu geçidin nftables kurallarıdır.
Ana bilgisayar portları: 3000 → ui nginx · 9080 → orkestratör API'si (yalnızca localhost)
```

Sandbox için DNS sorguları geçit (`172.20.0.2`) üzerinden zorlanır, böylece her çözümleme günlüğe kaydedilir.

---

## Güven Sınırı <a id="güven-sınırı"></a>

Bu, kod tabanındaki **en önemli tek değişmezdir**: *açıkça izin verilene kadar engelle.*

- Geçit, IPC soketi açılmadan **önce** tüm trafiği düşüren (`DROP`) nftables kurallarını kurar. Sandbox, açık bir `Allow` (İzin Ver) kuralı gelene kadar sıfır bağlantıya sahiptir.
- Geçit süreci ölürse, çekirdekteki `DROP` kuralları kalır — sistem **fail-closed** çalışır, asla açık kalmaz.
- **Engelleme (Block) kuralları her zaman İzin Verme (Allow) kurallarından üstündür.**
- Sızma testi komutları bir kabuğa (shell) **yalnızca tek bir yerden** ulaşır: `docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>` çalıştıran `sandbox.Bridge.Exec`. Ajan asla kendisi doğrudan `exec.Command` çağırmaz — yalnızca yürütücünün boşalttığı yürütme deposuna (execution store) ekler (geçit kapsam kontrolü → sandbox exec). Ana makine tarafındaki hiçbir komut yoluna ajan/LLM çıktısından erişilemez.

> Geçit modülü ana bilgisayarda değil, Docker içinde çalıştırılır ve ağ politikalarını konteyner ortamının çekirdek desteğiyle uygular. Doğrudan macOS üzerinde (Docker dışında) çalıştırıldığında, kuralların uygulanabileceği bir Linux çekirdeği bulunmaz.

---

## İçerik Katmanı Güvenliği <a id="icerik-katmani-guvenligi"></a>

Geçit, sınıfının en iyisi bir **ağ katmanı** sınırıdır. Ancak bir sızma testi yöneticisi daha ince bir saldırı yüzeyiyle karşı karşıyadır: LLM istemine (prompt) geri akan **içerik**. Bir hedefin komut çıktısı, kazınmış bir web sayfası, bir MCP yanıtı — bunların hiçbiri operatör tarafından yazılmamıştır ve herhangi biri istem enjeksiyonu (`ignore previous instructions…`), bir C2/promptware yönergesi, veri sızdırma yükü veya görünmez/bidi unicode içerebilir. Yummi aynı fail-closed duruşunu içerik katmanına da taşır.

```text
    araç çıktısı · bellek yazımları · yetenek dosyaları  (Operatör tarafından yazılmamış)
                       │
                       ▼
             ┌───────────────────┐   threatscan: enjeksiyon · sızdırma · C2
             │     threatscan    │               · görünmez unicode
             └─────────┬─────────┘
        araç çıktısı   │   BELLEK yazma / yetenek yükleme
     (çalışma anında   │   (kullanıcı aracılı)
     müdahale edilemez)│
               ▼                ▼
    ⚠ uyarı başlığı       ⛔ sıkı kapsamda engelle
    "untrusted DATA"
               │
               ▼
          LLM istemi (prompt)
```

- **`internal/security/threatscan`** — Üç kapsamda (`all` / `context` / `strict`) tarama yapan yaprak pakettir (yalnızca stdlib). Araç çıktısı **taranır ve uyarı başlığı eklenir** (engelleme değil, tespit — operatör komutun ortasında müdahale edemez); `[REMEMBER]` yazımları ve yetenek yüklemeleri sıkı (`strict`) kapsamda **engellenir**, böylece zehirli bir çıktı kalıcı, oturumlar arası bir istem enjeksiyonu oluşturamaz. Ayrıca çıktı beyne ulaşmadan önce **ANSI/C1 kaçış dizilerini** temizler.
- **Bellek Kaynağı (Memory provenance)** — Her kalıcı bellek satırı, denetim için onu kimin yazdığını (`agent` / `subagent`) kaydeder.
- **Mimari Uygunluk Testleri** (`internal/security/invariants`) — Kaynak ağacını tarayan ve denetlenmiş kabuk sınır dosyalarının dışında `os/exec` kullanımı tespit edilirse, üretim kodunda `internal/mcp` içe aktarılırsa (uyku kilidi) veya bir telemetri uç noktası sızarsa **derlemeyi başarısız kılan** çalıştırılabilir korumalardır. Aşağıdaki değişmezler sadece belgelenmekle kalmaz, testlerle de zorunlu tutulur.

---

## Ajan Motoru <a id="ajan-motoru"></a>

Yummi'nin ajanı **düz bir `while` döngüsü değildir**. Belirgin bir **graf durum makinesidir**: LLM her adımda tek bir etiketli eylem üretir ve deterministik bir yönlendirici, terminal bir düğüme ulaşana kadar sabit bir kenar tablosu üzerinden tiplendirilmiş düğümleri gezer. Bu tasarım, bağlam penceresinin tükenmesini ve sonsuz eylem döngülerini engeller — her adım, zayıf sohbet geçmişine güvenmek yerine tam görev durumunu istem (prompt) içinde yeniden işler.

### Graf Durum Makinesi — Düğümler ve Kenarlar <a id="graf-durum-makinesi"></a>

Motor `orchestrator/internal/agent` dizininde yer alır. `graph.go` düğüm kümesini ve `graphEdges` yönlendirme tablosunu (`node → edge → nextNode`) tutar; her düğüm kendi `node_*.go` dosyasında bulunur.

**On düğüm:**

| Düğüm | Dosya | Sorumluluk |
|------|------|----------------|
| `step` | `node_step.go` | LLM'den bir sonraki eylemi ister; tam görev durumunu + YETENEKLERİ + YETKİLENDİRMEYİ + BELLEĞİ isteme ekler; dönen kontrol etiketine göre yönlendirir. |
| `tool` | `node_tool.go` | Arabelleğe alınmış komutu doğrular, denetler ve çalıştırır; sıkışmış/yinelenen kalıpları tespit eder; uzun süren komutları gözetler. |
| `consult` | `node_consult.go` | Komut ortası danışma (örn. uzun süren komutlar için) — LLM'ye `[CONTINUE]`/`[ABORT]` sorar. |
| `retry` | `retry.go` | Üstel geri çekilme ile LLM/ayrıştırma hatası kurtarma; çağrıcısına geri yönlendirir veya pes eder. |
| `summarize` | `node_lifecycle.go` | Bağlam sıkıştırma: Token bütçesi %80 veya daha fazla tüketildiğinde, gözlem geçmişini bir özet haline getirir. |
| `approval` | `node_lifecycle.go` | Operatör onay kapısı (döngü goroutine'ini bir kanal üzerinde bloke eder). |
| `finish` | `node_lifecycle.go` | Terminal düğüm: Tamamlandı / başarısız / durduruldu; model hiç üretmediyse bir özet sentezler. |
| `ask` | `node_lifecycle.go` | Terminal düğüm: Duraklat ve operatöre bir soru sor. |
| `handoff` | `node_handoff.go` | Odaklanmış bir alt görevi uzman bir alt ajana devreder ve özetini geri besler. |
| `remember` | `node_remember.go` | Yapılandırılmış bir belleği (örn. kod haritası) bilgi deposuna kaydeder. |

**Yolculuk** (`graph.go` içindeki `Execute` fonksiyonu) — Çalışan her düğüm `step` düğümüne geri yönlendirir, yani motorun kendisi döngüdür; yalnızca terminal bir dügümde sonlanır:

```text
                     (her çalışan düğüm STEP düğümüne geri döner)
        ┌────────────────────────────────────────────────────────────┐
        │                                                            │
        ▼               run_command          ok / is_error           │
    ┌─────────┐ ─────────────────────────▶ ┌──────┐ ──────────────────┤
    │  STEP   │                            │ TOOL │                   │
    │ LLM'den │ ◀───── PLAN_UPDATED ─────  │ exec │── onay_gerekli ──────▶ approval ─▶ STEP
    │ sıradaki│                            │ +    │── uzun_suren ────────▶ consult  ─▶ STEP
    │ eylemi  │                            │guard │── sikisma / kısırdng ─▶ consult  ─▶ STEP
    │ iste    │                            └──────┘
    └────┬────┘
         │  finish · bütçe_bitti ──▶ FINISH  (terminal)
         │  operatore_sor · dur  ──▶ ASK     (terminal)
         │  llm_hatası ──▶ retry ──▶ (çağırana geri dön)
         │  summarize · remember · handoff ──▶ (STEP düğümüne geri dön)
         ▼
    Her STEP, ham sohbet geçmişi yerine tam görev durumunu (plan · gözlemler ·
    YETENEKLER · BELLEK) isteme (prompt) yeniden yansıtır.
```

Terminal düğümlerin (`finish`, `ask`) giden kenarları yoktur — `Execute` bunlara ulaştığında durur. `retry` dinamik olarak kendisini çağıran düğüme geri yönlendirir. Her geçiş kontrol noktasından (checkpoint) geçer, böylece çöken bir görev kaldığı düğümden devam eder.

**Döngü kırma mekanizması katmanlıdır** (`tool` düğümü): Birebir kopya tespiti (`[DUPLICATE_BLOCKED]`), `A→B→A→B` döngüleri için kayan pencereli sıkışma algılayıcı (`[STUCK]`), hash eşleşmelerinin kaçırdığı anlamsal yakın kopyaları yakalayan bir **tool-thrash** koruması — örn. `ps -ef` / `ps -ejH` / `ps aux` döngüleri (`[ABNORMAL_PATTERN]`) ve operatöre teslim olmadan önce farklı bir yaklaşımı zorlayan ardışık hata kademelendirmesi (`[REPEATED_FAILURE]`). En kritik olanı, yöntemsel bellek *bu görevde* zaten çalıştırılmış bir komutu asla hatırlamaz, böylece öğrenme döngüsü tekrarlama döngüsünü pekiştiremez.

### Etiket Protokolü <a id="the-tag-protocol"></a>

Ajan ve LLM, köşeli parantezlerle etiketlenmiş **kapalı bir kelime dağarcığı** ile konuşur (`protocol.go` içindeki `Registry`). Bu serbest metin veya XML *değildir* — kendi satırında yer alan bir `[TAG]` yardımıyla isteğe bağlı bir JSON gövdesidir. Kayıt defteri tek doğruluk kaynağıdır; istemin protokol referansı buradan üretilir.

```text
   AJAN (step dügümü)                                   LLM (beyin)
        │  tam görev durumunu + YETENEKLER · BELLEK           │
        │  + protokol referansını yansıt ve sor ─────────────▶│
        │                                                    │
        │◀──────────────── [RUN_COMMAND] {command, risk} ────│
        │                                                    │
        │  doğrula · sıkışma/kısırdöngü kontrolü · kapsam     │
        │  ──▶ Geçit kapsam denetimi ──▶ docker exec (sandbox)│
        │                                                    │
        │  [TOOL_RESPONSE_TRUNCATED] baş+son kısmı ──────────▶│
        │◀──────────────────────────────── [SEND_FULL] ──────│
        │  [TOOL_RESPONSE_FULL] tam çıktı ───────────────────▶│
        │◀──────────────── [FINISH] {summary, findings} ─────│
        │  terminal — model atladıysa raporu sentezle         │
```

İki özellik bunu *küçük* modellerde kararlı kılar: Araç çıktısı beyne ulaşmadan önce **tehdit taramasından geçirilir ve ANSI karakterlerinden temizlenir** (bkz. [İçerik Katmanı Güvenliği](#icerik-katmani-guvenligi)) ve öncü etiketi eksik olan bir yanıt, **JSON yapısından kontrol çıkarımı yapılarak** kurtarılır (`command` alanı → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, vb.).

**Ajan → LLM (gözlem / zarf etiketleri)** — Ajanın sonuçları geri iletme şekli:

| Etiket | Anlamı |
|-----|---------|
| `[TOOL_RESPONSE]` | Komut başarılı oldu; çıktı aşağıdadır. |
| `[TOOL_RESPONSE_TRUNCATED]` | Çıktı çok uzun; baş ve son kısmı gösteriliyor. Tamamını almak için `[SEND_FULL]` yanıtını gönderin. |
| `[TOOL_RESPONSE_FULL]` | `[SEND_FULL]` ile talep ettiğiniz tam çıktı. |
| `[TOOL_FAILED]` | Komut çalıştı ancak başarısız oldu — uyum sağlayın, tekrar etmeyin. |
| `[COMMAND_BLOCKED]` | Hatalı biçimlendirilmiş veya geçit hedefi kapsam dışı bularak reddetti. |
| `[DUPLICATE_BLOCKED]` | Bu komutu az önce çalıştırdınız — farklı bir komut seçin. |
| `[REPEATED_FAILURE]` | Üst üste birkaç hata — aracı/yolu değiştirin, yeniden planlayın veya bitirin. |
| `[STUCK]` / `[ABNORMAL_PATTERN]` | Yinelenen kalıp tespit edildi. |
| `[LONG_RUNNING]` | Komut yumuşak limiti aştı — `[CONTINUE]` veya `[ABORT]` yanıtı verin. |
| `[OUTPUT_OVERFLOW]` | Terminal çıktısı bağlam penceresine baskı yapıyor. |
| `[APPROVAL_DENIED]` / `[APPROVAL_TIMEOUT]` | Operatör onay gerektiren komutu reddetti / yanıt vermedi. |
| `[PLAN_UPDATED]` / `[BUDGET_WARNING]` | Plan kabul edildi / az adım kaldı. |
| `[UNKNOWN]` | Ajan durumu sınıflandıramadı — siz karar verin. |

**LLM → Ajan (kontrol / eylem etiketleri)** — Modelin yanıt verdiği etiketler (bir etiket, ardından bir JSON gövdesi):

| Etiket | Gövde | Etki |
|-----|------|--------|
| `[RUN_COMMAND]` | `{command, step_id, rationale, risk_level, findings?, findings_detail?}` | Sandbox komutu çalıştır. |
| `[UPDATE_PLAN]` | `{steps:[…], reason}` | Planı güncelle. |
| `[FINISH]` | `{summary, findings}` | Hedefe ulaşıldı. |
| `[HANDOFF]` | `{role, subgoal}` | Uzman bir alt ajana görev devret. |
| `[REMEMBER]` | `{key, value}` | Yapılandırılmış belleği bir anahtar altında kaydet (asla unutulmaz). |
| `[ASK_OPERATOR]` | `{question}` | Bloke olundu; bir insana ihtiyaç var. |
| `[SEND_FULL]` · `[CONTINUE]` · `[ABORT]` · `[STOP_TASK]` | — | Tam çıktıyı gönder / beklemeye devam et / komutu iptal et / görevi durdur. |

Zayıf modeller genellikle öncü etiketi atlar ve yalnızca JSON üretir — ayrıştırıcı, veri yükü şablonundan kontrol etiketini çıkarır (`command` alanı → `RUN_COMMAND`, `role`+`subgoal` → `HANDOFF`, vb.), böylece model formatı ne kadar kötü takip ederse etsin ajan kararlılığını korur.

### Beyinden Bağımsız Çekirdek <a id="beyinden-bagimsiz-cekirdek"></a>

LLM, tek metotlu bir arayüzün (`LLM { Chat(...) }`) arkasında **enjekte edilen, değiştirilebilir bir bağımlılıktır**. Çekirdek, o anda bağlı olan modelin yeteneklerine göre değil, *kalite ve kapsam* için tasarlanmıştır. Küçük bir yerel model (qwen3:8b) geliştirme beyni ve kalite stres testidir — eğer çekirdek küçük bir beyin üzerinde sağlıklı çalışıyorsa, büyük bir beyin üzerinde de çalışır. Daha güçlü bir beyin bağladığınızda, aynı çekirdek sadece yetenek seviyesini *genişletir*; asla değişmesi gerekmez.

---

## Bilgi Deposu — İki Katmanlı Bellek + Öğrenme Döngüsü <a id="bilgi-deposu"></a>

`internal/knowledge` bağımsız bir pakettir (internal içinde sadece `scope` paketine bağlıdır) ve **dünya modelinin tek sahibidir**. Ajan, yürütücü, API ve tüm alt ajanlar tek bir yere yazar — böylece ajanın keşifleri, tek bir görevde sıkışıp kalmış serbest metinler yerine yapılandırılmış, sorgulanabilir, paylaşılan bir bilgiye dönüşür.

**İki bellek katmanı:**

1. **Olgusal (etkinlik başına)** — `targets` (hedefler) ve `findings` (bulgular) tabloları. Bulgular kaynak bilgisi taşır: `source` (`manual` / `agent` / `subagent`) ve bulguyu üreten `task_id`.
2. **Yöntemsel (taşınabilir, hedeften arındırılmış)** — `procedural_memory` tablosu. Çalıştırılan her komut bir taktik olarak kaydedilir: `{tool, command_pattern, outcome}`. Burada hedef ifadesi doğrudan `<TARGET>` yer tutucusu ile değiştirilir (`scope.ExtractTarget` aracılığıyla), böylece **taşınabilir katmana hedefe özel hiçbir veri sızmaz**. Birebir aynı taktikler bir `hit_count` ile tekilleştirilir. Sonuç (`worked` / `failed`), çıkış kodundan **mekanik olarak** türetilir — yani bu öğrenme döngüsü beyne bile bağlı değildir.

**Öğrenme döngüsü:**

```text
    ┌──────────────────┐   ┌───────────────────────┐   ┌───────────────────┐
    │ ajan bir komut   │──▶│ yürütücü hedeften     │──▶│ procedural_memory │
    │ çalıştırır       │   │ arınmış taktiği kayd. │   │ araç·kalıp·       │
    └──────────────────┘   └───────────────────────┘   │ sonuç·hit_count   │
             ▲                                         └─────────┬─────────┘
             │                                                   │
    ┌────────┴──────────┐  ┌───────────────────────────┐         │
    │ node_step isteme  │◀─│ RetrievalStrategy mevcut  │◀────────┘
    │ bir BELLEK bloğu  │  │ araçlar için taktikleri   │
    │ enjekte eder      │  │ okur                      │
    └───────────────────┘  │ ‹BU görevde çalıştırılmış │
                           │  komutları HARİÇ tutar›   │
                           └───────────────────────────┘
```

Bilgi getirme (retrieval) işlemi bir `RetrievalStrategy` arayüzünün arkasındadır. Mevcut uygulama olan `StructuredRetrieval`, mevcut oturuma ve son çalıştırılan komutların araçlarına göre anahtarlanan deterministik SQL'dir — yeni bir bağımlılık yoktur, tamamen yereldir. Semantik/vektör tabanlı bir strateji daha sonra **çekirdeğe dokunmadan** sisteme eklenebilir.

HARİÇ TUTMA (EXCLUDES) adımı kritik bir önem taşır: Yöntemsel bellek **görevler arası aktarım** içindir, mevcut görevin kendi çalışma kümesini isteme geri yansıtmak için değildir. Henüz çalıştırılmış bir komutu *"önceki bilgi — bunu kullan, tekrar keşfetme"* olarak yeniden sunmak, zayıf bir modeli komut kısırdöngüsüne sokan şeydir; bu nedenle mevcut görevin komutları geri çağırmadan filtrelenir.

**Kalıcı bağlam** — `persistent_context` tablosu (`ON CONFLICT DO UPDATE`), tek bir görevin kayan penceresinden daha uzun ömürlü olan kalıcı bellekleri tutar: Kaynak kod bağlantı haritaları, modül grafikleri, belgeler. Kaynak analiz alt ajanı bunları `[REMEMBER]` aracılığıyla oluşturur ve günceller — "asla unutmayan kod haritalandırıcı."

---

## Yetenekler — Bildirimsel Tam Zamanında Yol Haritaları <a id="yetenekler"></a>

Her senaryo yönergesini isteme (prompt) tıkıştırmak muhakeme yeteneğini yok eder. `internal/skill` **bildirimsel bir kayıt defteridir**: Her yetenek `{name, description, triggers, tools, knowledge}` içeren bir YAML dosyasıdır (`persona/v0.1/skills/*.yaml`). Çalışma zamanında kayıt defteri, hedefte ve son kullanılan araçlarda kaç tane `triggers` (tetikleyici) göründüğüne göre her yeteneği puanlar ve yalnızca **en alakalı ilk N** yetenek kılavuzunu plan ve adım istemlerine enjekte eder — yalın, deterministik, yerel bir RAG yapısı. Yetenekler **hiçbir yürütme yolu** sunmaz; beynin hangi *sandbox* komutlarını çalıştıracağı konusunda daha akıllı olmasını sağlar. `recon`, `web` ve `source-analysis` yetenekleri ile birlikte gelir. Bir yetenek eklemek için bir YAML dosyası bırakmanız yeterlidir — kod değişikliği gerekmez.

---

## Alt Ajanlar — Sınırlı Yetkilendirme <a id="alt-ajanlar"></a>

Yummi hiyerarşik yetkilendirmeyi destekler. Orkestratörün derin, uzmanlaşmış bir çalışmaya ihtiyacı olduğunda, model `[HANDOFF]{role, subgoal}` üretir ve `handoff` düğümü **uzman bir alt ajanı** çalıştırır. Bu alt ajan, kendi personası ve yeteneklerinin kapsamlı bir alt kümesiyle yapılandırılmış aynı graf durum makinesidir.

- **Shared world model.** Alt ajanlar orkestratörün Veri Deposu + Bilgi Deposunu paylaşır, böylece bulguları ve öğrenilen taktikleri otomatik olarak ortak dünya modeline katılır. Bilgi Deposunun ilk önce tasarlanmasının nedeni tam olarak budur.
- **Token koruması.** Bir alt ajan kendi döngüsünü çalıştırır; üst ajana alt ajanın dahili adımları değil, yalnızca temiz bir `Summary` (Özet) (ve ortak bilgideki bulgular) geri döner.
- **Derinlik limiti 1.** Alt ajanlar boş bir alt ajan haritasıyla oluşturulur, bu nedenle asla `[HANDOFF]` görmez veya kabul etmezler — yineleme (recursion) veya çatal bombaları (fork bombs) oluşamaz.
- **Aynı yetki seviyesi.** Her alt ajan komutu hala tek yürütme yolundan geçer → geçit kapsam denetimi → sandbox. Bir alt ajan, orkestratörün yetkisine sahiptir, daha fazlığına değil.

`recon`, `web`, `source-analysis`, `reverser` ve `source-analyzer` rolleriyle (`persona/v0.1/subagents/*.yaml`) birlikte gelir. YAML dosyası ekleyerek yeni bir rol tanımlanabilir.

---

## TRACE — Ajan Gözlemlenebilirliği <a id="trace-gozlemlenebilirligi"></a>

Ajanın zekası **okunabilir** hale getirilmiştir. Graf yapısı tiplendirilmiş `ActivityEvent`'ler üretir — `plan · command · command_result · memory_recalled · skill_applied · handoff_started · handoff_completed · finding · context_saved · ask · finish`. Bunlar `agent_activity` tablosuna kaydedilir ve `/ws/events` WebSocket adresi üzerinden `agent.activity` olarak aktarılır. Alt ajan olayları, devir alanı altında yuvalanmaları için bir `parent_task_id` taşır.

- **Web AGENT Sekmesi** — Satır içi bir aktivite zaman çizelgesidir: 🧠 bellek hatırlandı, 📘 yetenek uygulandı, 🔎 bulgu, 🤝 devir, 💾 hatırlandı — böylece sadece metni değil, *ajanın neyden beslendiğini* görürsünüz.
- **Web TRACE Sekmesi** — Tam bir gözlemlenebilirlik alanıdır: Canlı aktivite izi (iç içe geçmiş alt ajan dalları) artı taranabilir Bulgular, öğrenilen Taktikler, kalıcı Bağlam (kod haritaları), Yetenekler ve Roller.
- **Okuma API'leri** (salt okunur): `GET /sessions/{id}/activity`, `/tactics`, `/sessions/{id}/context`, `/skills`, `/agent/roles`.

TUI arayüzü de aynı aktivite çiplerini terminalde canlı olarak gösterir.

---

## Yürütücü, Doğrulayıcı ve Egress Modları <a id="yurutucu-dogrulayici-ve-egress-modlari"></a>

- **conductor** (`internal/conductor`) — Bir persona yönlendirmeli sistem istemiyle doğrudan Ollama ile konuşur. **Kural tabanlı bir geri dönüş (fallback) yoktur**: LLM yapılandırılmamışsa veya erişilemez durumdaysa, iki dilli (Türkçe/İngilizce, otomatik algılanan) bir açıklamayla birlikte `intent: decline` döner. Yanıtlar yapılandırılmış JSON niyetleridir: `suggest_command`, `start_task`, `explain`, `ask_clarification`, `decline`.
- **validator** — Sadece **risk sınıflandırması** yapar, ağ kapsam kararları vermez. Risk derecesi, modelin kendi `risk_level` değerinden artı muhafazakar bir yapısal tabandan gelir (`classifyRisk` yalnızca kabuk zincirleme operatörlerinde `critical` seviyesine yükseltilir). **Sert kodlanmış bir araç izin listesi yoktur** — ajana yeni bir araç öğretmek için bir kayıt defterini değil, `persona/v0.1/` altındaki bilgileri düzenleyin.
- **Egress modu** (operatör tarafından ayarlanan, geçit tarafından zorunlu kılınan) — IP başına kapsam listesinin üzerinde yer alan çalışma zamanı 3 yollu ana şalterdir:
  - `block` (varsayılan, fail-closed) — harici çıkış yok.
  - `allow` — sandbox çıkışı tamamen kabul edilir (alan adları IP çözümlemesi olmadan çalışır).
  - `ask` — her yeni harici hedef operatör onayı gerektirir.
  - **Bu mod geçit yeniden başlatıldığında korunmaz — tekrar `block` (fail-closed) durumuna sıfırlanır.**

---

## Geçit <a id="gecit"></a>

Bellek güvenli denetim katmanı (`gateway/`, binary `yummi-gateway`):

- **`policy/`** — `PolicyEngine` (TTL süre sonu olan `RwLock<HashMap>`); Engelleme (Block) kuralları İzin Verme (Allow) kurallarından üstündür; çıkış modu bir `verdict` (`allow` / `block` / `needs_approval`) kararına varır.
- **`network/`** — FORWARD zincirini yöneten Linux/nftables tabanlı denetleyicidir (`NET_ADMIN` gerektirir). İsteğe bağlı parmak izi maskeleme: `ttl_normalize` (pasif işletim sistemi parmak izi analizini engellemek için) ve `mac_randomization`.
- **`ipc/`** — Unix alan soketi, uzunluk önekli JSON çerçeveleme (`[4-byte BE length][JSON]`). İstekler: `check_scope`, `get_status`, `ping`, `add_temporary_rule`, `remove_rule`, `set_egress_mode`, `list_rules`, `get_audit_log`.
- **`audit/`** — SQLite WAL günlüğü, her satır bir öncekine **SHA-256 hash zinciriyle** bağlıdır (kurcalamaya karşı korumalı). Kural ekleme/silme ve çıkış modu değişiklikleri kaydedilir.
- **`watchdog.rs`** — RSS limiti + kalp atışı (heartbeat) tazeliği denetimi; bir ihlal acil durum kilitlemesini ve daemon çıkışını tetikler.

Web GATEWAY sekmesi durumu, aktif kuralları, denetim günlüğünü ve egress modu anahtarını gösterir.

---

## Sandbox <a id="sandbox"></a>

Sıkılaştırılmış bir Kali konteyneri: Salt okunur kök dosya sistemi, `1000:1000` olarak çalışır, yalnızca `NET_RAW`/`NET_ADMIN` yetkileri, `no-new-privileges`. DNS sorguları geçit üzerinden zorlanır, böylece tüm aramalar günlüğe kaydedilir. Giriş noktası (entrypoint) sadece geçidin soketini bekler ve ardından `sleep infinity` moduna geçer — her komut kalıcı bir kabuk değil, taze bir `docker exec` komutudur.

---

## Arayüzler — Web UI ve TUI <a id="arayuzler"></a>

İki arayüz, **aynı REST/WS API'sini** kullanır — hiçbiri yeni bir yürütme yolu açmaz:

- **Web UI** (`ui/`, React 18 + Vite, `:3000` portunda nginx) — Sekmeler: AGENT (sohbet + canlı etkinlik zaman çizelgesi), BASH (WS üzerinden etkileşimli sandbox terminali), TRACE (gözlemlenebilirlik + dünya modeli), LOGS, GATEWAY, HEALTH.
- **TUI** (`internal/tui`, Charm Bubble Tea) — Basit ve temiz bir terminal operatör konsolu: Öneri → onay ile sohbet, ajan görev takibi ve WebSocket üzerinden aynı canlı etkinlik zaman çizelgesi (yetenek/bellek/bulgu/devir çipleri). `yummi --tui` veya `make start-tui` ile başlatın.

---

## Hızlı Başlangıç <a id="hizli-baslangic"></a>

```bash
make install   # geliştirme ortamını hazırlar (Go, Rust, Node, Docker, Ollama)
make build     # CLI + orkestratör + geçit ikili dosyalarını derler (UI ayrıca derlenir)
make start     # Ollama'nın çalıştığından emin olur, ardından yığını docker compose ile ayağa kaldırır
# Web paneli → http://localhost:3000

make start-tui # yığını başlatır ve bunun yerine terminal konsolunu açar
./bin/yummi --tui   # (servisler zaten ayaktaysa)
```

İlk `make start` komutu varsayılan modeli çeker (`qwen3:8b`, ~4 GB).

---

## Geliştirici İş Akışı <a id="gelistirici-is-akisi"></a>

```bash
make build      # CLI + orkestratör + geçit (UI HARİÇ — onu ayrıca derleyin)
make test       # tüm testler: CLI + orkestratör + geçit (cargo) + UI (vitest)
make lint       # repo kökünde ve orkestratör modülünde golangci-lint
```

```bash
# Ayrıntılı derleme
make _build-cli · _build-orchestrator · _build-gateway · _build-ui
make docker-build · docker-orchestrator · docker-ui

# Ayrıntılı test
make _test-cli · _test-orchestrator · _test-gateway · _test-ui
make test-agent-live    # gerçek qwen3:8b + canlı sandbox e2e (sandbox kapalıysa kendini atlar)

# Yaşam döngüsü
make dev · dev-ui · dev-orchestrator     # sıcak yeniden yüklemeli (hot-reload) geliştirme sunucuları
make force-restart · close               # her şeyi yeniden başlatır / durdurur + VRAM'i boşaltır
```

İki Go modülü vardır: Kök dizin (`./go.mod`) CLI + TUI kısmıdır; orkestratör dizini (`orchestrator/go.mod`) ise tüm yapay zeka/API/yürütme kısmıdır. `orchestrator/internal/*` altındaki testler `orchestrator/` dizininden çalıştırılmalıdır (ayrı bir modül). Geçit Rust; UI ise vitest kullanır. **Yummi'nin kendi bileşenlerinin taklitleri (mocks) yoktur** — testler yalnızca LLM sınırı taklit edilerek gerçek depoları/motorları çalıştırır.

---

## Yapılandırma <a id="yapilandirma"></a>

Orkestratör ortam değişkenleri:

```bash
YUMMI_ORCHESTRATOR_HOST=127.0.0.1   YUMMI_ORCHESTRATOR_PORT=9080
YUMMI_DB_PATH=~/.yummi/sessions.db  YUMMI_HOME=~/.yummi
YUMMI_DOCKER_SOCKET=/var/run/docker.sock   YUMMI_SANDBOX_CONTAINER=yummi-sandbox
YUMMI_GATEWAY_SOCKET=/run/yummi/gateway.sock
YUMMI_OLLAMA_URL=http://localhost:11434    # boş = yönetici her şeyi reddeder (geri dönüş yok)
YUMMI_OLLAMA_TIER=standard                 # lite | pro | expert | standard
YUMMI_PERSONA_DIR=persona/v0.1
```

Seviye (tier) ön tanımları bir model + bağlam penceresi + düşünme modu belirler: `lite` → llama3.2:3b · `pro` → llama3.1:8b · `expert` → llama3.3:70b · `standard` (geliştirme varsayılanı) → qwen3:8b.

**Ağ kapsamı bir orkestratör ayarı değildir.** Yalnızca geçidin kendi YAML dosyasında yapılandırılır (`gateway/config/default.yaml` içindeki `scope.allow` / `scope.block`) — geçit, tek kapsam otoritesidir.

---

## Depo Düzeni <a id="depo-duzeni"></a>

```text
cmd/yummi/              CLI giriş noktası (binary: yummi)
internal/
  cli/                  terminal başlatma, --tui bağlantıları, renkli çıktılar
  tui/                  Charm Bubble Tea terminal konsolu (api · ws · app · render · stiller)
  config/ · version/    yapılandırma yükleyici · derleme metadatası
orchestrator/           ayrı Go modülü (github.com/n0connect/yummi/orchestrator)
  cmd/orchestrator/     sunucu ikili dosyası (her paketi birbirine bağlar)
  internal/
    agent/              graf durum makinesi — graph.go, node_*.go, protocol.go, activity.go …
    knowledge/          dünya modeli — hedefler · bulgular · yöntemsel · bağlam · bilgi getirme
    skill/              bildirimsel yetenek kayıt defteri
    conductor/          LLM motoru · doğrulayıcı · onay · seviye · persona
    executor/ execution/ tek yürütme yolu · yürütme deposu
    api/                REST yolları + işleyiciler + WS köprüsü
    gateway/ sandbox/ scope/ session/ health/ llm/ tlsutil/ ui/   entegrasyon yaprakları
gateway/                Rust güven sınırı (politika · ipc · ağ · denetim · watchdog)
sandbox/                sıkılaştırılmış Kali konteyner imajı + giriş noktası
ui/                     React 18 + Vite paneli (3000 portunda nginx tarafından sunulur)
persona/v0.1/           belge tabanlı persona, skills/, subagents/
```

---

## Güvenlik Değişmezleri <a id="guvenlik-degismezleri"></a>

Yummi'de değişiklik yaparsanız bunları koruyun:

1. **Varsayılan olarak fail-closed.** Kapsam/nftables değişiklikleri "açıkça izin verilene kadar engelle" kuralını korumalıdır. Geçit, soketini açmadan önce DROP kurallarını yükler.
2. **Tek yürütme yolu.** Sızma testi komutları bir kabuğa yalnızca `sandbox.Bridge.Exec` üzerinden ulaşır. Ajan/LLM çıktısından erişilebilecek bir ana makine komut yolu asla sunmayın. (`internal/mcp` bir ana bilgisayar süreci başlatır ve bu nedenle uykuda tutulur — üretim kodu tarafından içe aktarılmaz.)
3. **Geçit, tek kapsam otoritesidir.** Orkestratör sorar; erişilebilirliğe kendisi asla karar vermez.
4. **Egress modu, geçit yeniden başlatıldığında `block` moduna sıfırlanır.**
5. **Yönetici/Yürütücü ayrımı.** Yönetici LLM'yi, personaları, riskleri bilir — komutların nasıl çalıştığını asla bilmez. Yürütücü geçidi/sandbox'ı/yürütmeyi bilir — sohbeti veya önerileri asla bilmez.

---

<div align="center">

Go · Rust · React · Charm · Ollama ile geliştirilmiştir. Conventional Commits standartları kullanılır.

</div>
