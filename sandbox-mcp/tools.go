package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rejectIfFlagLike errors when value would be parsed as a command-line flag
// rather than positional data by the receiving binary — e.g. a "target" of
// "--script=/tmp/x" reaching nmap bare (nothing precedes it in argv to claim
// it as an argument) lets a value the model supplies — possibly steered by
// hostile scanned content, exactly the class of attack internal/security/
// threatscan defends against on the way back INTO the model — redirect the
// tool's own behavior far beyond "scan this target": arbitrary NSE scripts,
// alternate output paths, etc. Every handler below checks this ONLY for
// fields it passes as a bare positional argument (no immediately-preceding
// flag of its own); a value passed as "-u", url is already safe regardless
// of its contents, since "-u" claims the next argv slot as its literal value
// no matter what it looks like.
func rejectIfFlagLike(field, value string) error {
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s %q looks like a command-line flag, not a value — refusing to pass it through", field, value)
	}
	return nil
}

// headerFlags turns a list of "Name: Value" strings into repeated "-H value"
// argv pairs — shared by curl and ffuf, the two tools in this catalog whose
// methodology (403-bypass headers, custom API auth headers, content-type for
// a fuzzed JSON body) genuinely needs caller-supplied headers. Each entry is
// checked with rejectIfFlagLike: "-H" claims the very next argv slot as its
// literal value regardless of content, so this only guards against a header
// string that is itself flag-shaped (e.g. "--data=@/etc/passwd" passed off
// as a "header"), not against ordinary header syntax like "X-Forwarded-For:
// 127.0.0.1", which never starts with "-".
func headerFlags(headers []string) ([]string, error) {
	var argv []string
	for _, h := range headers {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if err := rejectIfFlagLike("header", h); err != nil {
			return nil, err
		}
		argv = append(argv, "-H", h)
	}
	return argv, nil
}

type nmapArgs struct {
	Target           string `json:"target" jsonschema:"IP address, hostname, or CIDR range to scan"`
	Ports            string `json:"ports,omitempty" jsonschema:"optional port range or list, e.g. 1-1000 or 80,443"`
	ServiceDetection bool   `json:"service_detection,omitempty" jsonschema:"pass -sV for service/version detection"`
	DefaultScripts   bool   `json:"default_scripts,omitempty" jsonschema:"pass -sC to run nmap's default script set"`
	OutputFile       string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func nmapHandler(ctx context.Context, _ *mcp.CallToolRequest, args nmapArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	if err := rejectIfFlagLike("target", target); err != nil {
		return errResult(err.Error())
	}
	var argv []string
	if args.ServiceDetection {
		argv = append(argv, "-sV")
	}
	if args.DefaultScripts {
		argv = append(argv, "-sC")
	}
	if args.Ports != "" {
		p := strings.TrimSpace(args.Ports)
		lowerP := strings.ToLower(p)
		if strings.HasPrefix(lowerP, "top-") {
			argv = append(argv, "--top-ports", strings.TrimPrefix(lowerP, "top-"))
		} else if strings.HasPrefix(lowerP, "top") && len(lowerP) > 3 && lowerP[3] >= '0' && lowerP[3] <= '9' {
			argv = append(argv, "--top-ports", strings.TrimPrefix(lowerP, "top"))
		} else {
			argv = append(argv, "-p", p)
		}
	}
	argv = append(argv, target)
	return runCapturedToFile(ctx, args.OutputFile, "nmap", argv...)
}

// wordlistDir defaults to the read-only /wordlists mount (docker-compose.yml)
// but is overridable for tests.
func wordlistDir() string {
	if d := os.Getenv("YUMMI_WORDLIST_DIR"); d != "" {
		return d
	}
	return "/wordlists"
}

// commonWordlistFallback is dirb's own bundled default wordlist, already
// present in the Kali-based sandbox image independent of the operator-
// populated /wordlists mount (docker-compose.yml: "${HOME}/.yummi/wordlists
// — place your own here", empty by default on a fresh install). Used ONLY
// as a fallback for the literal "common.txt" name — every tool schema in
// this package that takes a wordlist argument suggests exactly that
// filename as its example ("wordlist filename under /wordlists, e.g.
// common.txt"), so a fresh install where the operator hasn't populated
// anything yet makes that example fail outright. Live-observed: with no
// fallback, gobuster_dir was unusable on a fresh install, and the model
// spent 5-9 real step-budget turns rediscovering this exact same system
// path by trial and error (ls/find across candidate directories) on two
// separate live runs — once exhausting the entire step budget before
// finishing. Deliberately a single hardcoded path, not a general search
// across system wordlist directories for an arbitrary operator-supplied
// name — that would reopen exactly the kind of file-existence oracle the
// traversal rejection below exists to prevent.
// var, not const: overridden in tests, since the real path only exists
// inside the Kali-based sandbox image, not a bare `go test` environment.
var commonWordlistFallback = "/usr/share/dirb/wordlists/common.txt"

// resolveWordlist rejects anything that is not a clean relative path under
// wordlistDir() — a model cannot smuggle "../../etc/passwd" as a "wordlist".
func resolveWordlist(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("wordlist is required")
	}
	dir := wordlistDir()
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid wordlist path %q", name)
	}
	full := filepath.Join(dir, clean)
	if !strings.HasPrefix(full, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid wordlist path %q", name)
	}
	if _, err := os.Stat(full); err != nil {
		if clean == "common.txt" {
			if _, fbErr := os.Stat(commonWordlistFallback); fbErr == nil {
				return commonWordlistFallback, nil
			}
		}
		return "", fmt.Errorf("wordlist %q not found under %s", name, dir)
	}
	return full, nil
}

type whatwebArgs struct {
	URL        string `json:"url" jsonschema:"the target URL"`
	Aggression int    `json:"aggression,omitempty" jsonschema:"optional -a aggression level 1-4 (whatweb's own default is 1; 3 is the common thorough-but-not-noisy choice for a senior recon pass)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func whatwebHandler(ctx context.Context, _ *mcp.CallToolRequest, args whatwebArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	var argv []string
	if args.Aggression > 0 {
		argv = append(argv, "-a", fmt.Sprint(args.Aggression))
	}
	argv = append(argv, url)
	return runCapturedToFile(ctx, args.OutputFile, "whatweb", argv...)
}

type curlArgs struct {
	URL             string   `json:"url" jsonschema:"the target URL"`
	HeadersOnly     bool     `json:"headers_only,omitempty" jsonschema:"pass -I to fetch headers only, no body"`
	FollowRedirects bool     `json:"follow_redirects,omitempty" jsonschema:"pass -L to follow redirects"`
	Method          string   `json:"method,omitempty" jsonschema:"optional HTTP method, e.g. POST, PUT, DELETE, PATCH (default: GET, or the method curl infers from -d)"`
	Data            string   `json:"data,omitempty" jsonschema:"optional request body to send with -d, e.g. a JSON payload for a POST/GraphQL request"`
	Headers         []string `json:"headers,omitempty" jsonschema:"optional list of request headers, each as 'Name: Value', e.g. 'X-Forwarded-For: 127.0.0.1' for an access-control bypass test or 'Content-Type: application/json' for a JSON body"`
	OutputFile      string   `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func curlHandler(ctx context.Context, _ *mcp.CallToolRequest, args curlArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	argv := []string{"-s"}
	if args.HeadersOnly {
		argv = append(argv, "-I")
	} else {
		argv = append(argv, "-i")
	}
	if args.FollowRedirects {
		argv = append(argv, "-L")
	}
	if method := strings.TrimSpace(args.Method); method != "" {
		if err := rejectIfFlagLike("method", method); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-X", method)
	}
	hdrArgv, err := headerFlags(args.Headers)
	if err != nil {
		return errResult(err.Error())
	}
	argv = append(argv, hdrArgv...)
	if args.Data != "" {
		argv = append(argv, "-d", args.Data)
	}
	argv = append(argv, url)
	return runCapturedToFile(ctx, args.OutputFile, "curl", argv...)
}

type gobusterArgs struct {
	URL        string `json:"url" jsonschema:"the target URL to enumerate"`
	Wordlist   string `json:"wordlist" jsonschema:"wordlist filename under /wordlists, e.g. common.txt"`
	Extensions string `json:"extensions,omitempty" jsonschema:"optional comma-separated extensions, e.g. php,txt,html"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func gobusterHandler(ctx context.Context, _ *mcp.CallToolRequest, args gobusterArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	wl, err := resolveWordlist(args.Wordlist)
	if err != nil {
		return errResult(err.Error())
	}
	argv := []string{"dir", "-u", url, "-w", wl}
	if args.Extensions != "" {
		argv = append(argv, "-x", args.Extensions)
	}
	return runCapturedToFile(ctx, args.OutputFile, "gobuster", argv...)
}

type ffufArgs struct {
	URL        string   `json:"url" jsonschema:"the target URL; FUZZ is appended automatically if not present"`
	Wordlist   string   `json:"wordlist" jsonschema:"wordlist filename under /wordlists, e.g. common.txt"`
	MatchCodes string   `json:"match_codes,omitempty" jsonschema:"optional comma-separated status codes to keep, e.g. 200,204,301,302,307,401,403 — without this ffuf shows every response, which is usually too noisy to read"`
	Method     string   `json:"method,omitempty" jsonschema:"optional HTTP method, e.g. POST for fuzzing body/JSON parameters (default: GET)"`
	Data       string   `json:"data,omitempty" jsonschema:"optional request body for a POST fuzz — may itself contain the literal word FUZZ to fuzz a body/JSON field instead of the URL, e.g. '{\"FUZZ\":\"test\"}'"`
	Headers    []string `json:"headers,omitempty" jsonschema:"optional list of request headers, each as 'Name: Value', e.g. 'Content-Type: application/json' when fuzzing a JSON body"`
	OutputFile string   `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func ffufHandler(ctx context.Context, _ *mcp.CallToolRequest, args ffufArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	wl, err := resolveWordlist(args.Wordlist)
	if err != nil {
		return errResult(err.Error())
	}
	// Only append /FUZZ to the URL when neither the URL nor a POST body
	// already carries the FUZZ keyword — a body-based fuzz job (data
	// containing FUZZ) targets the request body, not the path, and must not
	// also get a spurious /FUZZ suffix appended to its URL.
	if !strings.Contains(url, "FUZZ") && !strings.Contains(args.Data, "FUZZ") {
		url = strings.TrimRight(url, "/") + "/FUZZ"
	}
	argv := []string{"-u", url, "-w", wl, "-s"}
	if args.MatchCodes != "" {
		if err := rejectIfFlagLike("match_codes", args.MatchCodes); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-mc", args.MatchCodes)
	}
	if method := strings.TrimSpace(args.Method); method != "" {
		if err := rejectIfFlagLike("method", method); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-X", method)
	}
	hdrArgv, err := headerFlags(args.Headers)
	if err != nil {
		return errResult(err.Error())
	}
	argv = append(argv, hdrArgv...)
	if args.Data != "" {
		argv = append(argv, "-d", args.Data)
	}
	return runCapturedToFile(ctx, args.OutputFile, "ffuf", argv...)
}

type niktoArgs struct {
	Host       string `json:"host" jsonschema:"the target host or URL"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func niktoHandler(ctx context.Context, _ *mcp.CallToolRequest, args niktoArgs) (*mcp.CallToolResult, any, error) {
	host := strings.TrimSpace(args.Host)
	if host == "" {
		return errResult("host is required")
	}
	return runCapturedToFile(ctx, args.OutputFile, "nikto", "-h", host)
}

// registerTools wires every cataloged tool onto server. Split out from
// main() so tests can build a server without also starting a stdio loop.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "nmap_scan",
		Description: "Scan a target with nmap for open ports and services.",
	}, nmapHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "whatweb_scan",
		Description: "Fingerprint a web target with whatweb.",
	}, whatwebHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "curl_probe",
		Description: "Fetch a URL with curl to inspect headers/response for recon.",
	}, curlHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "gobuster_dir",
		Description: "Enumerate web content with gobuster dir mode.",
	}, gobusterHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ffuf_fuzz",
		Description: "Fuzz a URL path with ffuf.",
	}, ffufHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "nikto_scan",
		Description: "Scan a web host with nikto for known issues.",
	}, niktoHandler)
	registerExtendedTools(server)
	registerConsolidateTool(server)
	registerPassiveReconTool(server)
	registerTerminalTools(server)
}
