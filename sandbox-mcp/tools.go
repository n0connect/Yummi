package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type nmapArgs struct {
	Target           string `json:"target" jsonschema:"IP address, hostname, or CIDR range to scan"`
	Ports            string `json:"ports,omitempty" jsonschema:"optional port range or list, e.g. 1-1000 or 80,443"`
	ServiceDetection bool   `json:"service_detection,omitempty" jsonschema:"pass -sV for service/version detection"`
	DefaultScripts   bool   `json:"default_scripts,omitempty" jsonschema:"pass -sC to run nmap's default script set"`
}

func nmapHandler(ctx context.Context, _ *mcp.CallToolRequest, args nmapArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	var argv []string
	if args.ServiceDetection {
		argv = append(argv, "-sV")
	}
	if args.DefaultScripts {
		argv = append(argv, "-sC")
	}
	if args.Ports != "" {
		argv = append(argv, "-p", args.Ports)
	}
	argv = append(argv, target)
	return runCaptured(ctx, "nmap", argv...)
}

// wordlistDir defaults to the read-only /wordlists mount (docker-compose.yml)
// but is overridable for tests.
func wordlistDir() string {
	if d := os.Getenv("YUMMI_WORDLIST_DIR"); d != "" {
		return d
	}
	return "/wordlists"
}

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
		return "", fmt.Errorf("wordlist %q not found under %s", name, dir)
	}
	return full, nil
}

type whatwebArgs struct {
	URL string `json:"url" jsonschema:"the target URL"`
}

func whatwebHandler(ctx context.Context, _ *mcp.CallToolRequest, args whatwebArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	return runCaptured(ctx, "whatweb", url)
}

type curlArgs struct {
	URL             string `json:"url" jsonschema:"the target URL"`
	HeadersOnly     bool   `json:"headers_only,omitempty" jsonschema:"pass -I to fetch headers only, no body"`
	FollowRedirects bool   `json:"follow_redirects,omitempty" jsonschema:"pass -L to follow redirects"`
}

func curlHandler(ctx context.Context, _ *mcp.CallToolRequest, args curlArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
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
	argv = append(argv, url)
	return runCaptured(ctx, "curl", argv...)
}

type gobusterArgs struct {
	URL        string `json:"url" jsonschema:"the target URL to enumerate"`
	Wordlist   string `json:"wordlist" jsonschema:"wordlist filename under /wordlists, e.g. common.txt"`
	Extensions string `json:"extensions,omitempty" jsonschema:"optional comma-separated extensions, e.g. php,txt,html"`
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
	return runCaptured(ctx, "gobuster", argv...)
}

type ffufArgs struct {
	URL      string `json:"url" jsonschema:"the target URL; FUZZ is appended automatically if not present"`
	Wordlist string `json:"wordlist" jsonschema:"wordlist filename under /wordlists, e.g. common.txt"`
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
	if !strings.Contains(url, "FUZZ") {
		url = strings.TrimRight(url, "/") + "/FUZZ"
	}
	return runCaptured(ctx, "ffuf", "-u", url, "-w", wl)
}

type niktoArgs struct {
	Host string `json:"host" jsonschema:"the target host or URL"`
}

func niktoHandler(ctx context.Context, _ *mcp.CallToolRequest, args niktoArgs) (*mcp.CallToolResult, any, error) {
	host := strings.TrimSpace(args.Host)
	if host == "" {
		return errResult("host is required")
	}
	return runCaptured(ctx, "nikto", "-h", host)
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
}
