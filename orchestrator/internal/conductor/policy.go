package conductor

import (
	"regexp"
	"strings"
)

var explicitTargetPattern = regexp.MustCompile(`(?i)(https?://\S+|\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?\b|\b[a-z0-9][a-z0-9.-]+\.[a-z]{2,}\b|(?:^|\s)(?:\./|/)[^\s]+|\b[a-z0-9_.-]+\.(?:zip|tar|gz|tgz|bz2|xz|7z|pcap|pcapng|bin|exe|apk|elf|so|dylib|dll|py|js|php|html|json|yaml|yml|xml|raw|dump|db|sqlite)\b)`)
var explicitActionWordPattern = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(tara|scan|pentest|enumerate|crack)(?:$|[^\p{L}\p{N}_])`)

// biliyorMusunPattern requires a non-letter (or string start) immediately
// before "biliyor" — plain strings.Contains matched "biliyor musun" as a
// trailing substring of ANY Turkish "-Abiliyor musun" (can-you-X) verb
// construction, e.g. "kırabiliyor musun" ("can you crack it") silently
// satisfying the educational carve-out below and defeating the
// missing_target golden fixture, which needs exactly that phrasing to still
// require a target. "biliyor" alone (know) vs. "-Abiliyor" (ability suffix,
// works on any verb stem) is a Turkish-specific collision the codebase's
// established \p{L} boundary convention (see explicitActionWordPattern)
// already has a fix for.
var biliyorMusunPattern = regexp.MustCompile(`(?i)(?:^|[^\p{L}])biliyor musun`)

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
	// Found via live long-form testing, twice, with two different trigger
	// keywords: "... hakkında kısa bilgi ver (çalıştırmadan, sadece bilgi)"
	// (pentest) and "CVE-2024-99999 ... biliyor musun, detaylarini anlatir
	// misin?" (güvenlik açığı) were both wrongly routed to ask_clarification
	// for a target — both are knowledge questions ("do you know about X, can
	// you explain") with no execution intent at all, not requests to act.
	// The prefix/suffix list above only recognized "nedir"/"what is"/"how
	// does" phrasing; these markers cover the same educational-not-execution
	// intent expressed the other common ways operators phrase a question
	// instead of a command.
	if biliyorMusunPattern.MatchString(lower) {
		return ChatResponse{}, false
	}
	for _, marker := range []string{
		"hakkında bilgi", "hakkinda bilgi", "tell me about", "çalıştırmadan", "calistirmadan",
		"anlatir misin", "anlatır mısın", "aciklar misin", "açıklar mısın",
		"do you know", "can you explain", "can you tell me",
	} {
		if strings.Contains(lower, marker) {
			return ChatResponse{}, false
		}
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
