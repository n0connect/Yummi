package conductor

import "testing"

func TestPersonaGoldenFixtures(t *testing.T) {
	report, err := EvaluateGoldenFixtures("../../../persona/v0.1/golden_tests")
	if err != nil {
		t.Fatalf("EvaluateGoldenFixtures: %v", err)
	}
	if report.Total == 0 || report.Passed != report.Total {
		t.Fatalf("persona golden regression: %+v", report)
	}
}

func TestBoundaryDecisionDoesNotTreatEducationAsExecution(t *testing.T) {
	for _, input := range []string{"What is a network scan?", "Güvenlik açığı nedir?", "How does brute force detection work?"} {
		if decision, handled := BoundaryDecision(input); handled {
			t.Errorf("educational query %q was intercepted: %+v", input, decision)
		}
	}
}

// TestBoundaryDecisionDoesNotMatchSecuritySubstrings protects ordinary local
// evidence and retrospective questions from being mistaken for a new network
// operation merely because a word contains scan/pentest/tara as a substring.
func TestBoundaryDecisionDoesNotMatchSecuritySubstrings(t *testing.T) {
	for _, input := range []string{
		"Çıktıda pentester adlı kullanıcı var mı?",
		"scanner diye bir kullanıcı var mı yalnızca mevcut çıktıya bak.",
		"scandal kelimesi geçen bir kullanıcı uydurma.",
		"Bu bir tarama isteği değil.",
		"Bu konuşmada herhangi bir dış hedef taradın mı?",
	} {
		if decision, handled := BoundaryDecision(input); handled {
			t.Errorf("non-execution query %q was intercepted: %+v", input, decision)
		}
	}
}

// TestBoundaryDecisionDoesNotTreatInformationalRequestsAsExecution is a
// live-long-form-testing-found regression test: "bana Kali Linux'ta bulunan
// yaygin bir pentest araci hakkinda kisa bilgi ver (calistirmadan, sadece
// bilgi)" — a purely informational request that explicitly says not to run
// anything — was wrongly routed to ask_clarification for a target, because
// it contains "pentest" as a whole word and the educational carve-out only
// recognized "nedir"/"what is"/"how does" phrasing.
func TestBoundaryDecisionDoesNotTreatInformationalRequestsAsExecution(t *testing.T) {
	for _, input := range []string{
		"bana Kali Linux'ta bulunan yaygin bir pentest araci hakkinda kisa bilgi ver (calistirmadan, sadece bilgi)",
		"nmap hakkında bilgi ver",
		"tell me about common pentest tools, without running anything",
		"Apache 2.4.7'de CVE-2024-99999 diye bir güvenlik açığı oldugunu biliyor musun, detaylarini anlatir misin?",
		"do you know about that vulnerability, can you explain it?",
	} {
		if decision, handled := BoundaryDecision(input); handled {
			t.Errorf("informational query %q was intercepted: %+v", input, decision)
		}
	}
}

func TestBoundaryDecisionStillRequiresTargetForExplicitActions(t *testing.T) {
	for _, input := range []string{
		"hedefi tara",
		"scan the host",
		"şifreli bir zip dosyasını kır",
		"bu sistemde güvenlik açığı var mı bakabilir misin?",
	} {
		decision, handled := BoundaryDecision(input)
		if !handled || decision.Intent != "ask_clarification" {
			t.Errorf("explicit action %q = (%+v, %v), want ask_clarification", input, decision, handled)
		}
	}
}
