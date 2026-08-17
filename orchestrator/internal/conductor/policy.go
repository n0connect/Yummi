package conductor

import (
	"regexp"
	"strings"
)

var explicitTargetPattern = regexp.MustCompile(`(?i)(https?://\S+|\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?\b|\b[a-z0-9][a-z0-9.-]+\.[a-z]{2,}\b|(?:^|\s)(?:\./|/)[^\s]+|\b[a-z0-9_.-]+\.(?:zip|tar|gz|pcap|bin|exe|apk)\b)`)
var explicitActionWordPattern = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(tara|scan|pentest|enumerate|crack)(?:$|[^\p{L}\p{N}_])`)

// BoundaryDecision enforces deterministic safety/clarification invariants
// before a probabilistic model is consulted. It intentionally covers only
// high-confidence cases represented by persona golden fixtures.
func BoundaryDecision(message string) (ChatResponse, bool) {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	if text == "" {
		return ChatResponse{Intent: "ask_clarification", Message: "Please describe the goal.", Confidence: 1, References: []string{"policy:empty_goal"}}, true
	}
	for _, phrase := range []string{"tüm internet", "butun internet", "whole internet", "entire internet", "scan the internet"} {
		if strings.Contains(lower, phrase) {
			return ChatResponse{Intent: "decline", Message: localize(text,
				"Sınırsız internet taraması güvenli ve yetkili bir hedef tanımlamıyor. Açıkça yetkilendirilmiş bir hedef paylaşın.",
				"An unrestricted internet scan has no bounded authorized target. Provide an explicitly authorized target."),
				Confidence: 1, References: []string{"policy:bounded_target_required"}}, true
		}
	}
	for _, phrase := range []string{"ne yapacağımı bilmiyorum", "ne yapacagimi bilmiyorum", "i don't know what to do", "not sure what to do"} {
		if strings.Contains(lower, phrase) {
			return ChatResponse{Intent: "ask_clarification", Message: localize(text,
				"Hedefinizi ve ulaşmak istediğiniz sonucu biraz daha açıklar mısınız?",
				"Could you clarify the target and the outcome you want?"), Confidence: 1,
				References: []string{"policy:goal_required"}}, true
		}
	}
	// Explanatory questions are not execution requests. Keep the deterministic
	// boundary narrow so educational uses do not require a fictitious target.
	for _, prefix := range []string{"what is ", "how does ", "nedir ", "nasıl çalışır ", "nasil calisir "} {
		if strings.HasPrefix(lower, prefix) {
			return ChatResponse{}, false
		}
	}
	if strings.HasSuffix(lower, " nedir?") || strings.HasSuffix(lower, " nedir") {
		return ChatResponse{}, false
	}

	needsTarget := explicitActionWordPattern.MatchString(lower)
	for _, keyword := range []string{
		"güvenlik açığı", "guvenlik acigi", "şifreli", "sifreli", "brute force", "bruteforce",
	} {
		if strings.Contains(lower, keyword) {
			needsTarget = true
			break
		}
	}
	if needsTarget && !explicitTargetPattern.MatchString(text) {
		return ChatResponse{Intent: "ask_clarification", Message: localize(text,
			"İşlem yapılacak açık ve yetkili hedefi (host, URL veya dosya yolu) paylaşır mısınız?",
			"Please provide the explicit authorized target (host, URL, or file path)."), Confidence: 1,
			References: []string{"policy:explicit_target_required"}}, true
	}
	return ChatResponse{}, false
}
