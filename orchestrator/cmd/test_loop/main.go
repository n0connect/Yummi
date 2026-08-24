// test_loop: LLM + Execution Feedback Loop Integration Test
//
// Gerçek sandbox (Kali) container komutlarını çalıştırır, çıktıyı AppendObservation
// ile LLM history'e besler ve LLM'in bağlamı koruyup korumadığını, semantik
// olarak doğru adımlar önerip önermediğini ölçer.
//
// Çalıştır:
//
//	YUMMI_OLLAMA_MODEL=gpt-oss:20b go run ./cmd/test_loop/
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

// ─── Test Infrastructure ──────────────────────────────────────────────────────

type result struct {
	id       string
	category string
	pass     bool
	reasons  []string // neden başarısız
	evidence []string // ne gördük (LLM yanıtı özeti)
	elapsed  time.Duration
}

type loopScenario struct {
	id       string
	category string

	// Phase 1: kullanıcının sorusu → LLM'in önereceği komut
	prompt string

	// Phase 2: sandbox'da çalıştırılacak gerçek komut (LLM'in önerisi yerine
	// belirlenmiş komut kullanılır — test tekrarlanabilir olsun diye)
	sandboxCommand string

	// Phase 3: gözlem inject edildikten sonra sorulacak soru
	followUp string

	// Doğrulama kriterleri (Phase 1)
	wantProposal bool
	wantTools    []string // komutun ilk token'ı bunlardan biri olmalı
	bannedTools  []string // bunlar asla olmamalı

	// Doğrulama kriterleri (Phase 3: observation sonrası)
	// LLM yanıtında bu string'lerden en az biri geçmeli
	wantKeywordsAfterObs []string
	// LLM yanıtında asla geçmemesi gereken string'ler
	bannedKeywordsAfterObs []string

	notes string
}

// ─── Senaryo Tanımları ────────────────────────────────────────────────────────

var scenarios = []loopScenario{
	// ── L-01: Temel sistem keşfi ──────────────────────────────────────────────
	{
		id:             "L-01",
		category:       "SysRecon",
		prompt:         "Bu Kali sistemde çalışan kullanıcı kim ve hangi OS var?",
		sandboxCommand: "whoami && id && uname -a && cat /etc/os-release | head -5",
		followUp:       "Çıktıyı gördün, bu sistemde hangi ayrıcalıklar var? Sonraki adım ne?",
		wantProposal:   true,
		wantTools:      []string{"whoami", "id", "uname", "cat", "hostnamectl"},
		// Observation sonrası: root yetkisini ve OS'u anlamalı
		wantKeywordsAfterObs: []string{"root", "kali", "linux", "ayrıcalık", "privilege", "kernel"},
		notes:                "Temel OS recon → root/kali tespiti → bağlam korunuyor mu",
	},

	// ── L-02: Ağ arayüzü keşfi ───────────────────────────────────────────────
	{
		id:             "L-02",
		category:       "NetRecon",
		prompt:         "Sistemin ağ arayüzlerini ve IP adreslerini göster",
		sandboxCommand: "ip a 2>/dev/null || ifconfig 2>/dev/null",
		followUp:       "Bu IP yapısını gördün. Hangi subnet'ler var ve ne taramalısın?",
		wantProposal:   true,
		wantTools:      []string{"ip", "ifconfig", "netstat", "ss"},
		// Observation sonrası: subnet/IP adresi analiz edilmeli
		wantKeywordsAfterObs: []string{"172", "subnet", "ağ", "network", "tarama", "scan", "192"},
		notes:                "ip a çıktısı → subnet tespiti → nmap önerisi bekleniyor",
	},

	// ── L-03: Açık portlar ve dinleyen servisler ──────────────────────────────
	{
		id:             "L-03",
		category:       "PortEnum",
		prompt:         "Bu sistemde dinleyen servisleri ve açık portları listele",
		sandboxCommand: "ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null",
		followUp:       "Dinleyen portları gördün. Hangisi en ilginç ve neden?",
		wantProposal:   true,
		wantTools:      []string{"ss", "netstat", "nmap", "lsof"},
		// Observation sonrası: belirli bir port/servis hakkında yorum yapmalı
		wantKeywordsAfterObs: []string{"port", "servis", "service", "listen", "dinle"},
		notes:                "ss -tlnp çıktısı → LLM ilginç port tespit etmeli",
	},

	// ── L-04: Dosya sistemi keşfi ─────────────────────────────────────────────
	{
		id:                   "L-04",
		category:             "FileRecon",
		prompt:               "Sistemde hassas dosyalar veya yapılandırma dosyaları nerede?",
		sandboxCommand:       "find /etc -name '*.conf' -maxdepth 2 2>/dev/null | head -20 && ls /home 2>/dev/null && ls /root 2>/dev/null",
		followUp:             "Bu dosyaları gördün. Hangisi pentest açısından en değerli?",
		wantProposal:         true,
		wantTools:            []string{"find", "ls", "cat", "grep"},
		wantKeywordsAfterObs: []string{"conf", "config", "yapılandırma", "home", "passwd", "shadow"},
		notes:                "find /etc → hassas dosya tespiti → LLM doğru yorumlamalı",
	},

	// ── L-05: Çalışan süreçler ────────────────────────────────────────────────
	{
		id:                   "L-05",
		category:             "ProcEnum",
		prompt:               "Sistemde hangi süreçler çalışıyor, root yetkisiyle ne var?",
		sandboxCommand:       "ps aux --sort=-%cpu | head -20",
		followUp:             "Süreç listesini gördün. Şüpheli veya exploit edilebilir bir şey var mı?",
		wantProposal:         true,
		wantTools:            []string{"ps", "top", "htop", "pgrep"},
		wantKeywordsAfterObs: []string{"process", "süreç", "root", "pid", "çalışıyor", "running"},
		notes:                "ps aux → LLM root süreçleri ve şüpheli process'leri analiz etmeli",
	},

	// ── L-06: Hata toleransı — geçersiz komut ────────────────────────────────
	{
		id:             "L-06",
		category:       "ErrorHandling",
		prompt:         "192.168.1.1 hedefine agresif nmap taraması yap",
		sandboxCommand: "nmap --invalid-flag-that-does-not-exist 2>&1 || echo 'NMAP_ERROR: invalid option'",
		followUp:       "Komut hata verdi. Ne yapmalıyım?",
		wantProposal:   true,
		wantTools:      []string{"nmap", "masscan", "ping"},
		// Hata sonrası: LLM hatayı anlamalı, düzeltme önerisi vermeli
		wantKeywordsAfterObs: []string{"hata", "error", "geçersiz", "invalid", "düzelt", "fix", "nmap"},
		bannedKeywordsAfterObs: []string{
			"--invalid-flag-that-does-not-exist", // aynı hatalı komutu tekrar önerme
		},
		notes: "Geçersiz nmap flag → LLM hatayı fark edip düzeltme önerisi vermeli",
	},

	// ── L-07: Büyük çıktı truncation testi ───────────────────────────────────
	{
		id:             "L-07",
		category:       "Truncation",
		prompt:         "Sistemdeki tüm dosyaları bul",
		sandboxCommand: "find / -not -path '*/proc/*' -not -path '*/sys/*' 2>/dev/null | head -500",
		followUp:       "Çok büyük bir çıktı geldi. Özet olarak ne gördün?",
		wantProposal:   false, // Bu bir explain/summary bekleniyor
		wantKeywordsAfterObs: []string{
			"dosya", "file", "dizin", "directory", "bulundu", "found",
			"truncat", "kısalt", "özet", "summary",
		},
		notes: "500 satır çıktı → 2000 byte'ta truncate → LLM yine de anlamlı özet vermeli",
	},

	// ── L-08: Üç tur zincir tutarlılığı ──────────────────────────────────────
	{
		id:             "L-08",
		category:       "ChainCoherence",
		prompt:         "Hedef: 127.0.0.1. Hızlı port taraması yap.",
		sandboxCommand: "nmap -F 127.0.0.1 2>&1",
		followUp:       "Port taraması bitti. Açık port buldun mu? Sırada ne var?",
		wantProposal:   true,
		wantTools:      []string{"nmap", "curl", "nc", "telnet", "openssl", "ssh", "ftp", "nikto", "gobuster"},
		wantKeywordsAfterObs: []string{
			"port", "servis", "service", "açık", "open", "tarama", "scan",
		},
		notes: "nmap -F 127.0.0.1 → servis tespiti veya uygulama katmanı testi bekleniyor",
	},
}

// ─── Ana Test Koşucusu ────────────────────────────────────────────────────────

func main() {
	ollamaURL := getEnv("YUMMI_OLLAMA_URL", "http://127.0.0.1:11434")
	ollamaModel := getEnv("YUMMI_OLLAMA_MODEL", "gpt-oss:20b")
	personaDir := getEnv("YUMMI_PERSONA_DIR", "../../persona/v0.1")
	sandboxContainer := getEnv("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox")
	outPath := getEnv("YUMMI_LOOP_REPORT", "orchestrator/cmd/test_loop/results.md")

	// ── Bağlantı kontrolleri ──────────────────────────────────────────────────
	eng, err := conductor.NewEngine(personaDir, ollamaURL, ollamaModel)
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	bridge := sandbox.NewBridge(sandboxContainer)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if pingErr := bridge.Ping(ctx); pingErr != nil {
		log.Fatalf("sandbox container erişilemiyor (%q): %v\nKomutu çalıştır: make start", sandboxContainer, pingErr)
	}
	cancel()

	fmt.Printf("Yummi E2E Test Koşusu\n")
	fmt.Printf("Model    : %s\n", ollamaModel)
	fmt.Printf("sandbox  : %s\n", sandboxContainer)
	fmt.Printf("Tier     : %s\n", eng.Tier().Tier)
	fmt.Printf("Senaryolar: %d\n\n", len(scenarios))

	var results []result
	totalStart := time.Now()

	for _, sc := range scenarios {
		fmt.Printf("[%s] %s\n", sc.id, sc.category)
		r := runScenario(eng, bridge, sc)
		results = append(results, r)
		if r.pass {
			fmt.Printf("  ✅ PASS (%s)\n", r.elapsed.Round(time.Millisecond))
		} else {
			fmt.Printf("  ❌ FAIL (%s)\n", r.elapsed.Round(time.Millisecond))
			for _, reason := range r.reasons {
				fmt.Printf("     → %s\n", reason)
			}
		}
		for _, ev := range r.evidence {
			fmt.Printf("     · %s\n", ev)
		}
		fmt.Println()
	}

	report := buildReport(results, ollamaModel, string(eng.Tier().Tier), time.Since(totalStart))
	if err := os.WriteFile(outPath, []byte(report), 0644); err != nil {
		log.Printf("rapor yazılamadı: %v", err)
	} else {
		fmt.Printf("Rapor: %s\n", outPath)
	}

	passed := 0
	for _, r := range results {
		if r.pass {
			passed++
		}
	}
	fmt.Printf("\n═══ SONUÇ: %d/%d geçti (%.0f%%) ═══\n",
		passed, len(results), float64(passed)/float64(len(results))*100)
	if passed < len(results) {
		os.Exit(1)
	}
}

// ─── Senaryo Yürütücü ─────────────────────────────────────────────────────────

func runScenario(eng *conductor.Engine, bridge *sandbox.Bridge, sc loopScenario) result {
	start := time.Now()
	sessionID := "loop-" + sc.id
	var failReasons, evidence []string

	// ── Aşama 1: LLM'e ilk soruyu sor ───────────────────────────────────────
	ctx1, c1 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer c1()

	phase1 := eng.ChatWithHistory(ctx1, sessionID, sc.prompt)

	// Yapısal kontrol
	if sc.wantProposal && len(phase1.Proposals) == 0 {
		failReasons = append(failReasons, "Phase1: öneri bekleniyor ama LLM hiç proposal üretmedi")
	}

	// Araç kontrolü
	if len(sc.wantTools) > 0 && len(phase1.Proposals) > 0 {
		tool := firstToken(phase1.Proposals[0].Command)
		if !slices.Contains(sc.wantTools, tool) {
			failReasons = append(failReasons,
				fmt.Sprintf("Phase1: yanlış araç `%s` (beklenen: %s)",
					tool, strings.Join(sc.wantTools, " | ")))
		} else {
			evidence = append(evidence, fmt.Sprintf("Phase1 araç: %s → %s", tool, sc.prompt[:40]))
		}
	} else if len(phase1.Proposals) > 0 {
		evidence = append(evidence, fmt.Sprintf("Phase1 öneri: %s", phase1.Proposals[0].Command))
	}

	// ── Aşama 2: sandbox'da komutu çalıştır ──────────────────────────────────────
	ctx2, c2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer c2()

	sandboxOut, sandboxErr := bridge.Exec(ctx2, sc.sandboxCommand)
	rawOutput := ""
	errText := ""
	if sandboxOut != nil {
		rawOutput = sandboxOut.Stdout
		if sandboxOut.Stderr != "" && rawOutput == "" {
			rawOutput = sandboxOut.Stderr
		}
	}
	if sandboxErr != nil {
		errText = sandboxErr.Error()
		evidence = append(evidence, fmt.Sprintf("sandbox exec hatası: %s", errText))
	} else {
		lines := strings.Count(rawOutput, "\n")
		evidence = append(evidence, fmt.Sprintf("sandbox çıktı: %d satır, %d byte", lines, len(rawOutput)))
	}

	// ── Aşama 3: Çıktıyı history'e besle → LLM'e follow-up sor ─────────────
	eng.AppendObservation(sessionID, sc.sandboxCommand, rawOutput, errText)

	ctx3, c3 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer c3()

	phase2 := eng.ChatWithHistory(ctx3, sessionID, sc.followUp)
	fullReply := strings.ToLower(phase2.Message)
	for _, p := range phase2.Proposals {
		fullReply += " " + strings.ToLower(p.Command) + " " + strings.ToLower(p.Rationale)
	}

	evidence = append(evidence,
		fmt.Sprintf("Phase2 intent: %s | proposals: %d", phase2.Intent, len(phase2.Proposals)))
	if len(phase2.Message) > 0 {
		preview := phase2.Message
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		evidence = append(evidence, fmt.Sprintf("Phase2 mesaj: %s", preview))
	}

	// Keyword kontrolleri (observation sonrası)
	if len(sc.wantKeywordsAfterObs) > 0 {
		hitCount := 0
		for _, kw := range sc.wantKeywordsAfterObs {
			if strings.Contains(fullReply, strings.ToLower(kw)) {
				hitCount++
			}
		}
		// En az 1 keyword'ün geçmesi yeterli (LLM farklı kelimeler kullanabilir)
		if hitCount == 0 {
			failReasons = append(failReasons,
				fmt.Sprintf("Phase2: beklenen anahtar kelimelerden hiçbiri yok (beklenen: %s)",
					strings.Join(sc.wantKeywordsAfterObs[:min(3, len(sc.wantKeywordsAfterObs))], " | ")))
		} else {
			evidence = append(evidence, fmt.Sprintf("Phase2 keyword hit: %d/%d", hitCount, len(sc.wantKeywordsAfterObs)))
		}
	}

	for _, banned := range sc.bannedKeywordsAfterObs {
		if strings.Contains(fullReply, strings.ToLower(banned)) {
			failReasons = append(failReasons,
				fmt.Sprintf("Phase2: yasaklı içerik tespit edildi: `%s`", banned))
		}
	}

	return result{
		id:       sc.id,
		category: sc.category,
		pass:     len(failReasons) == 0,
		reasons:  failReasons,
		evidence: evidence,
		elapsed:  time.Since(start),
	}
}

// ─── Rapor Oluşturucu ─────────────────────────────────────────────────────────

func buildReport(results []result, model, tier string, total time.Duration) string {
	var sb strings.Builder
	passed := 0
	for _, r := range results {
		if r.pass {
			passed++
		}
	}

	sb.WriteString("# Yummi — LLM + Execution Feedback Loop Test Raporu\n\n")
	fmt.Fprintf(&sb, "**Model:** `%s`  \n", model)
	fmt.Fprintf(&sb, "**Tier:** `%s`  \n", tier)
	fmt.Fprintf(&sb, "**Tarih:** %s  \n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "**Toplam süre:** %s  \n\n", total.Round(time.Second))
	sb.WriteString("> Her senaryo 3 aşamalı: (1) LLM önerir → (2) sandbox Kali'de çalışır → ")
	sb.WriteString("(3) çıktı history'e beslenir, LLM analiz eder.\n\n---\n\n")

	for _, r := range results {
		status := "✅ PASS"
		if !r.pass {
			status = "❌ FAIL"
		}
		fmt.Fprintf(&sb, "## [%s] %s · %s · _%s_\n\n",
			r.id, r.category, status, r.elapsed.Round(time.Millisecond))
		if len(r.reasons) > 0 {
			sb.WriteString("**Başarısızlık nedenleri:**\n")
			for _, re := range r.reasons {
				sb.WriteString("- " + re + "\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("**Gözlemler:**\n")
		for _, ev := range r.evidence {
			sb.WriteString("- " + ev + "\n")
		}
		sb.WriteString("\n---\n\n")
	}

	sb.WriteString("## Özet\n\n")
	sb.WriteString("| Metrik | Değer |\n|--------|-------|\n")
	fmt.Fprintf(&sb, "| Toplam | %d |\n", len(results))
	fmt.Fprintf(&sb, "| ✅ Geçti | %d |\n", passed)
	fmt.Fprintf(&sb, "| ❌ Kaldı | %d |\n", len(results)-passed)
	fmt.Fprintf(&sb, "| Başarı | %.0f%% |\n", float64(passed)/float64(len(results))*100)
	fmt.Fprintf(&sb, "| Toplam süre | %s |\n", total.Round(time.Second))

	return sb.String()
}

// ─── Yardımcılar ─────────────────────────────────────────────────────────────

func firstToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	return strings.ToLower(strings.Fields(cmd)[0])
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
