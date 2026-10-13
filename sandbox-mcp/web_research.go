package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	maxResearchBodyBytes = 32 << 10
	maxResearchExcerpt   = 4 << 10
)

type researchTrust string

const (
	researchTrustPrimary      researchTrust = "primary"
	researchTrustSecondary    researchTrust = "secondary"
	researchTrustUnclassified researchTrust = "unclassified"
)

type researchCandidate struct {
	URL   string
	Title string
	Trust researchTrust
}

type researchEvidence struct {
	SourceURL   string
	Title       string
	RetrievedAt time.Time
	SHA256      string
	Excerpt     string
	Trust       researchTrust
}

var (
	researchTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title\s*>`)
	researchScriptStyle  = regexp.MustCompile(`(?is)<(?:script|style|noscript)[^>]*>.*?</(?:script|style|noscript)\s*>`)
	researchTag          = regexp.MustCompile(`(?s)<[^>]*>`)
)

// normalizeResearchEvidence converts a sandbox-fetched response into the
// only form allowed to leave sandbox-mcp: bounded, plain-text, cited evidence.
// It deliberately returns neither raw bytes nor provider response structures.
func normalizeResearchEvidence(candidate researchCandidate, body []byte, contentType string, now time.Time) (researchEvidence, error) {
	parsed, err := url.Parse(candidate.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return researchEvidence{}, fmt.Errorf("invalid research source URL")
	}
	if len(body) > maxResearchBodyBytes {
		return researchEvidence{}, fmt.Errorf("research response exceeds %d bytes", maxResearchBodyBytes)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType != "text/html" && mediaType != "text/plain" {
		return researchEvidence{}, fmt.Errorf("unsupported content type %q", mediaType)
	}

	raw := string(body)
	title := strings.TrimSpace(candidate.Title)
	if mediaType == "text/html" {
		if match := researchTitlePattern.FindStringSubmatch(raw); len(match) == 2 {
			title = normalizeResearchText(match[1])
		}
		raw = researchScriptStyle.ReplaceAllString(raw, " ")
		raw = researchTag.ReplaceAllString(raw, " ")
	}
	excerpt := normalizeResearchText(raw)
	if containsResearchInstruction(excerpt) || containsResearchInstruction(title) {
		return researchEvidence{}, fmt.Errorf("untrusted content rejected by sandbox research guard")
	}
	if len(excerpt) > maxResearchExcerpt {
		excerpt = excerpt[:maxResearchExcerpt]
	}
	if title == "" {
		title = parsed.Hostname()
	}
	digest := sha256.Sum256(body)
	return researchEvidence{
		SourceURL:   parsed.String(),
		Title:       title,
		RetrievedAt: now.UTC(),
		SHA256:      hex.EncodeToString(digest[:]),
		Excerpt:     excerpt,
		Trust:       candidate.Trust,
	}, nil
}

func normalizeResearchText(in string) string {
	in = html.UnescapeString(in)
	in = strings.Map(func(r rune) rune {
		switch {
		case r == '\x1b' || r == '\x7f':
			return -1
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			return -1
		default:
			return r
		}
	}, in)
	return strings.Join(strings.Fields(in), " ")
}

func containsResearchInstruction(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{
		"ignore previous instructions", "ignore all previous instructions",
		"system message", "developer message", "tool call", "export cookies",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
