package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// ReportFormat specifies the target export template format.
type ReportFormat string

const (
	FormatHackerOne ReportFormat = "hackerone"
	FormatBugcrowd  ReportFormat = "bugcrowd"
	FormatExecutive ReportFormat = "executive"
	FormatJSON      ReportFormat = "json"
)

// ReportData aggregates all session intelligence needed for comprehensive reporting.
type ReportData struct {
	SessionID   string                        `json:"session_id"`
	GeneratedAt time.Time                     `json:"generated_at"`
	Targets     []knowledge.Target            `json:"targets"`
	Findings    []knowledge.Finding           `json:"findings"`
	Assets      []knowledge.AssetNode         `json:"assets"`
	Contexts    []knowledge.PersistentContext `json:"contexts"`
}

// Generate formats the collected session intelligence into the desired report string.
func Generate(data ReportData, format ReportFormat) (string, string, error) {
	switch format {
	case FormatHackerOne:
		return generateHackerOne(data), "text/markdown", nil
	case FormatBugcrowd:
		return generateBugcrowd(data), "text/markdown", nil
	case FormatExecutive:
		return generateExecutive(data), "text/markdown", nil
	case FormatJSON:
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", "", fmt.Errorf("marshal json report: %w", err)
		}
		return string(b), "application/json", nil
	default:
		return generateExecutive(data), "text/markdown", nil
	}
}

func generateHackerOne(d ReportData) string {
	var sb strings.Builder
	sb.WriteString("# Vulnerability Assessment & Bug Bounty Report\n\n")
	fmt.Fprintf(&sb, "**Report Date:** %s  \n", d.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&sb, "**Session Reference:** `%s`  \n\n", d.SessionID)

	sb.WriteString("## 1. Scope & Target Assets\n\n")
	if len(d.Targets) == 0 {
		sb.WriteString("No explicit root targets declared.\n\n")
	} else {
		for _, t := range d.Targets {
			fmt.Fprintf(&sb, "- `%s` (In Scope: %v)\n", t.Value, t.InScope)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 2. Executive Summary\n\n")
	critCount, highCount, medCount, lowCount, infoCount := countSeverities(d.Findings)
	fmt.Fprintf(&sb, "During the security assessment, a total of **%d findings** were identified across the target scope:\n", len(d.Findings))
	fmt.Fprintf(&sb, "- **Critical:** %d\n- **High:** %d\n- **Medium:** %d\n- **Low:** %d\n- **Informational:** %d\n\n", critCount, highCount, medCount, lowCount, infoCount)

	sb.WriteString("## 3. Detailed Findings & Steps to Reproduce\n\n")
	if len(d.Findings) == 0 {
		sb.WriteString("No vulnerability findings recorded during this session.\n\n")
	} else {
		for idx, f := range d.Findings {
			cvss, cwe := estimateCVSSAndCWE(f.Severity, f.Title)
			fmt.Fprintf(&sb, "### 3.%d. %s\n\n", idx+1, f.Title)
			fmt.Fprintf(&sb, "- **Severity:** `%s` (CVSS v3.1: %s)\n", strings.ToUpper(f.Severity), cvss)
			fmt.Fprintf(&sb, "- **Weakness Classification:** %s\n", cwe)
			fmt.Fprintf(&sb, "- **Discovery Source:** %s\n\n", f.Source)

			sb.WriteString("#### Description & Impact\n")
			if f.Description != "" {
				sb.WriteString(f.Description + "\n\n")
			} else {
				sb.WriteString("Vulnerability identified during automated security workflow.\n\n")
			}

			sb.WriteString("#### Proof of Concept / Evidence\n")
			if f.Evidence != "" {
				sb.WriteString("```\n" + f.Evidence + "\n```\n\n")
			} else {
				sb.WriteString("_Referenced in session execution traces._\n\n")
			}

			sb.WriteString("#### Remediation Recommendation\n")
			sb.WriteString(recommendMitigation(f.Title, f.Severity) + "\n\n")
			sb.WriteString("---\n\n")
		}
	}

	return sb.String()
}

func generateBugcrowd(d ReportData) string {
	var sb strings.Builder
	sb.WriteString("# Bugcrowd VRT Security Submission Report\n\n")
	fmt.Fprintf(&sb, "**Date:** %s | **Session:** `%s`\n\n", d.GeneratedAt.UTC().Format("2006-01-02"), d.SessionID)

	sb.WriteString("## Targets Tested\n")
	for _, t := range d.Targets {
		fmt.Fprintf(&sb, "- `%s`\n", t.Value)
	}
	sb.WriteString("\n## Submissions\n\n")

	for i, f := range d.Findings {
		_, cwe := estimateCVSSAndCWE(f.Severity, f.Title)
		fmt.Fprintf(&sb, "### Finding #%d: %s\n", i+1, f.Title)
		fmt.Fprintf(&sb, "**VRT Category:** %s  \n", cwe)
		fmt.Fprintf(&sb, "**Priority:** P%d (%s)  \n\n", severityToPriority(f.Severity), strings.ToUpper(f.Severity))
		fmt.Fprintf(&sb, "**Description:**\n%s\n\n", f.Description)
		if f.Evidence != "" {
			sb.WriteString("**Evidence / Validation:**\n```\n" + f.Evidence + "\n```\n\n")
		}
	}
	return sb.String()
}

func generateExecutive(d ReportData) string {
	var sb strings.Builder
	sb.WriteString("# Yummi Pentest & Attack Surface Executive Report\n\n")
	fmt.Fprintf(&sb, "> **Generated:** %s | **Session ID:** `%s`\n\n", d.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"), d.SessionID)

	sb.WriteString("## 1. Scope and Attack Surface\n\n")
	fmt.Fprintf(&sb, "- **Declared Targets:** %d\n", len(d.Targets))
	fmt.Fprintf(&sb, "- **Discovered Asset Nodes:** %d\n\n", len(d.Assets))

	if len(d.Assets) > 0 {
		sb.WriteString("### Discovered Asset Inventory\n\n")
		sb.WriteString("| Type | Asset Value | Discovered |\n")
		sb.WriteString("|---|---|---|\n")
		for _, a := range d.Assets {
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s |\n", a.Type, a.Value, time.Unix(a.CreatedAt, 0).UTC().Format("15:04:05"))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 2. Findings Summary\n\n")
	critCount, highCount, medCount, lowCount, infoCount := countSeverities(d.Findings)
	sb.WriteString("| Severity | Count |\n|---|---|\n")
	fmt.Fprintf(&sb, "| Critical | %d |\n| High | %d |\n| Medium | %d |\n| Low | %d |\n| Info | %d |\n\n", critCount, highCount, medCount, lowCount, infoCount)

	sb.WriteString("## 3. Detailed Findings Matrix\n\n")
	for _, f := range d.Findings {
		fmt.Fprintf(&sb, "### [%s] %s\n\n", strings.ToUpper(f.Severity), f.Title)
		if f.Description != "" {
			sb.WriteString(f.Description + "\n\n")
		}
		if f.Evidence != "" {
			sb.WriteString("**Authoritative Evidence Digest:**\n```\n" + f.Evidence + "\n```\n\n")
		}
	}

	return sb.String()
}

func countSeverities(findings []knowledge.Finding) (crit, high, med, low, info int) {
	for _, f := range findings {
		switch strings.ToLower(f.Severity) {
		case "critical":
			crit++
		case "high":
			high++
		case "medium":
			med++
		case "low":
			low++
		default:
			info++
		}
	}
	return
}

func severityToPriority(sev string) int {
	switch strings.ToLower(sev) {
	case "critical":
		return 1
	case "high":
		return 2
	case "medium":
		return 3
	case "low":
		return 4
	default:
		return 5
	}
}

func estimateCVSSAndCWE(severity, title string) (string, string) {
	lower := strings.ToLower(title)
	cwe := "CWE-200: Exposure of Sensitive Information"
	if strings.Contains(lower, "sqli") || strings.Contains(lower, "sql") {
		cwe = "CWE-89: SQL Injection"
	} else if strings.Contains(lower, "xss") || strings.Contains(lower, "script") {
		cwe = "CWE-79: Cross-Site Scripting"
	} else if strings.Contains(lower, "rce") || strings.Contains(lower, "command injection") {
		cwe = "CWE-78: OS Command Injection"
	} else if strings.Contains(lower, "ssrf") {
		cwe = "CWE-918: Server-Side Request Forgery"
	} else if strings.Contains(lower, "cors") {
		cwe = "CWE-942: Permissive CORS Policy"
	} else if strings.Contains(lower, "jwt") || strings.Contains(lower, "token") {
		cwe = "CWE-287: Improper Authentication"
	}

	switch strings.ToLower(severity) {
	case "critical":
		return "9.8 (CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H)", cwe
	case "high":
		return "8.1 (CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:N)", cwe
	case "medium":
		return "5.3 (CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N)", cwe
	case "low":
		return "3.1 (CVSS:3.1/AV:N/AC:H/PR:N/UI:R/S:U/C:L/I:N/A:N)", cwe
	default:
		return "0.0 (Informational)", cwe
	}
}

func recommendMitigation(title, severity string) string {
	lower := strings.ToLower(title)
	if strings.Contains(lower, "sqli") || strings.Contains(lower, "sql") {
		return "Use parameterized prepared statements and ORM abstractions. Avoid direct string concatenation in queries."
	} else if strings.Contains(lower, "xss") {
		return "Apply contextual output encoding and implement a strict Content Security Policy (CSP)."
	} else if strings.Contains(lower, "ssrf") {
		return "Enforce strict allowlists for outbound network requests, restrict internal loopback access, and block AWS/cloud metadata IP ranges (169.254.169.254)."
	} else if strings.Contains(lower, "cors") {
		return "Avoid reflecting the Origin header dynamically with credentials enabled. Maintain a strict whitelist of trusted partner origins."
	} else if strings.Contains(lower, "jwt") {
		return "Enforce strict algorithm verification on the server, reject 'none' algorithm tokens, and store signing keys in a secure secret manager."
	}
	return "Review application logic and apply defense-in-depth access controls and input validation."
}
