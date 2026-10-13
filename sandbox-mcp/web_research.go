package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const (
	maxResearchBodyBytes = 1 << 20
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
	SourceURL   string        `json:"source_url"`
	Title       string        `json:"title"`
	RetrievedAt time.Time     `json:"retrieved_at"`
	SHA256      string        `json:"sha256"`
	Excerpt     string        `json:"excerpt"`
	Trust       researchTrust `json:"trust"`
}

var (
	researchANSI = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
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
		var pageTitle string
		raw, pageTitle, err = extractResearchHTML(raw)
		if err != nil {
			return researchEvidence{}, err
		}
		if pageTitle != "" {
			title = pageTitle
		}
	}
	excerpt := normalizeResearchText(raw)
	title = normalizeResearchText(title)
	if containsResearchInstruction(excerpt) || containsResearchInstruction(title) {
		return researchEvidence{}, fmt.Errorf("untrusted content rejected by sandbox research guard")
	}
	title = normalizeResearchText(title)
	if len(title) > 300 {
		title = string([]rune(title)[:min(300, len([]rune(title)))])
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
	in = researchANSI.ReplaceAllString(in, "")
	in = stdhtml.UnescapeString(in)
	in = strings.Map(func(r rune) rune {
		switch {
		case r == '\x1b' || r == '\x7f':
			return -1
		case unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && r != '\n' && r != '\t'):
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
		"reveal your system prompt", "export cookies", "send your api key", "<|im_start|>",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func extractResearchHTML(raw string) (string, string, error) {
	z := html.NewTokenizer(strings.NewReader(raw))
	var body, title strings.Builder
	skip, inTitle := false, false
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return "", "", fmt.Errorf("source HTML parsing failed")
			}
			return body.String(), normalizeResearchText(title.String()), nil
		case html.StartTagToken:
			tag, _ := z.TagName()
			name := string(tag)
			if name == "script" || name == "style" || name == "noscript" {
				skip = true
			}
			if name == "title" {
				inTitle = true
			}
		case html.EndTagToken:
			tag, _ := z.TagName()
			name := string(tag)
			if name == "script" || name == "style" || name == "noscript" {
				skip = false
			}
			if name == "title" {
				inTitle = false
			}
		case html.TextToken:
			text := z.Text()
			if !skip {
				body.Write(text)
				body.WriteByte(' ')
			}
			if inTitle {
				title.Write(text)
			}
		}
	}
}
