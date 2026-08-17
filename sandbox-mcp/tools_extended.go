package main

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file rounds out the sandbox-mcp catalog to match nearly every
// standalone, target-taking pentest binary sandbox/Dockerfile installs.
// tools.go's original six (nmap/whatweb/curl/gobuster/ffuf/nikto) stay the
// primary, individually-typed tools on the Eino side; these are reached
// through the search_pentest_tools/run_pentest_tool generic dispatch pair
// instead of getting their own dedicated Eino tool. Deliberately excluded:
// crunch (generates a wordlist file, no network target), socat/tcpdump
// (long-running/streaming, not a one-shot "run and return text" fit),
// smbclient (interactive by default) — none of these fit this catalog's
// one-shot request/response shape.

type amassArgs struct {
	Domain  string `json:"domain" jsonschema:"the target domain to enumerate"`
	Passive bool   `json:"passive,omitempty" jsonschema:"pass -passive for passive-only enumeration (no direct target contact)"`
}

func amassHandler(ctx context.Context, _ *mcp.CallToolRequest, args amassArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	argv := []string{"enum", "-d", domain}
	if args.Passive {
		argv = append(argv, "-passive")
	}
	return runCaptured(ctx, "amass", argv...)
}

type subfinderArgs struct {
	Domain string `json:"domain" jsonschema:"the target domain to enumerate subdomains for"`
}

func subfinderHandler(ctx context.Context, _ *mcp.CallToolRequest, args subfinderArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	return runCaptured(ctx, "subfinder", "-d", domain, "-silent")
}

type theharvesterArgs struct {
	Domain string `json:"domain" jsonschema:"the target domain to harvest emails/hosts for"`
	Source string `json:"source,omitempty" jsonschema:"data source, e.g. google, bing, or all (default: all)"`
}

func theharvesterHandler(ctx context.Context, _ *mcp.CallToolRequest, args theharvesterArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	source := args.Source
	if source == "" {
		source = "all"
	}
	return runCaptured(ctx, "theHarvester", "-d", domain, "-b", source)
}

type dnsenumArgs struct {
	Domain string `json:"domain" jsonschema:"the target domain to enumerate DNS records for"`
}

func dnsenumHandler(ctx context.Context, _ *mcp.CallToolRequest, args dnsenumArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	return runCaptured(ctx, "dnsenum", domain)
}

type enum4linuxArgs struct {
	Target string `json:"target" jsonschema:"the target host to enumerate (SMB/Windows/Samba)"`
}

func enum4linuxHandler(ctx context.Context, _ *mcp.CallToolRequest, args enum4linuxArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	// "-a" is a standalone boolean flag here — it does not claim target as
	// its own value, so target still reaches enum4linux as a bare positional.
	if err := rejectIfFlagLike("target", target); err != nil {
		return errResult(err.Error())
	}
	return runCaptured(ctx, "enum4linux", "-a", target)
}

type onesixtyoneArgs struct {
	Target string `json:"target" jsonschema:"the target host to probe for SNMP"`
}

func onesixtyoneHandler(ctx context.Context, _ *mcp.CallToolRequest, args onesixtyoneArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	if err := rejectIfFlagLike("target", target); err != nil {
		return errResult(err.Error())
	}
	return runCaptured(ctx, "onesixtyone", target)
}

type wafw00fArgs struct {
	URL string `json:"url" jsonschema:"the target URL to fingerprint for a web application firewall"`
}

func wafw00fHandler(ctx context.Context, _ *mcp.CallToolRequest, args wafw00fArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	return runCaptured(ctx, "wafw00f", url)
}

type wpscanArgs struct {
	URL string `json:"url" jsonschema:"the target WordPress site URL"`
}

func wpscanHandler(ctx context.Context, _ *mcp.CallToolRequest, args wpscanArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	// wpscan writes its vulnerability-database cache under $HOME/.cache before
	// it does anything else — including just parsing --help — which aborts
	// immediately under the sandbox's read-only root filesystem unless HOME
	// points somewhere writable. /tmp is the sandbox's one writable mount.
	return runCapturedEnv(ctx, []string{"HOME=/tmp"}, "wpscan", "--url", url)
}

type nucleiArgs struct {
	Target string `json:"target" jsonschema:"the target URL or host to scan with nuclei templates"`
}

func nucleiHandler(ctx context.Context, _ *mcp.CallToolRequest, args nucleiArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	return runCaptured(ctx, "nuclei", "-u", target)
}

type sqlmapArgs struct {
	URL string `json:"url" jsonschema:"the target URL to test for SQL injection (include query parameters)"`
}

func sqlmapHandler(ctx context.Context, _ *mcp.CallToolRequest, args sqlmapArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	return runCaptured(ctx, "sqlmap", "-u", url, "--batch")
}

type hydraArgs struct {
	Target   string `json:"target" jsonschema:"the target host to brute-force"`
	Service  string `json:"service" jsonschema:"the service to attack, e.g. ssh, ftp, http-post-form"`
	User     string `json:"user,omitempty" jsonschema:"a single username to try (mutually exclusive with user_list)"`
	UserList string `json:"user_list,omitempty" jsonschema:"wordlist filename under /wordlists for usernames (mutually exclusive with user)"`
	PassList string `json:"pass_list" jsonschema:"wordlist filename under /wordlists for passwords"`
}

func hydraHandler(ctx context.Context, _ *mcp.CallToolRequest, args hydraArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	service := strings.TrimSpace(args.Service)
	if target == "" || service == "" {
		return errResult("target and service are required")
	}
	// Both reach hydra as bare trailing positionals ("hydra [options] server
	// service") — neither has a preceding flag of its own to claim it.
	if err := rejectIfFlagLike("target", target); err != nil {
		return errResult(err.Error())
	}
	if err := rejectIfFlagLike("service", service); err != nil {
		return errResult(err.Error())
	}
	var argv []string
	switch {
	case args.User != "":
		argv = append(argv, "-l", args.User)
	case args.UserList != "":
		wl, err := resolveWordlist(args.UserList)
		if err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-L", wl)
	default:
		return errResult("user or user_list is required")
	}
	passWl, err := resolveWordlist(args.PassList)
	if err != nil {
		return errResult(err.Error())
	}
	argv = append(argv, "-P", passWl, target, service)
	return runCaptured(ctx, "hydra", argv...)
}

type medusaArgs struct {
	Target   string `json:"target" jsonschema:"the target host to brute-force"`
	Module   string `json:"module" jsonschema:"the medusa module to use, e.g. ssh, ftp"`
	User     string `json:"user,omitempty" jsonschema:"a single username to try (mutually exclusive with user_list)"`
	UserList string `json:"user_list,omitempty" jsonschema:"wordlist filename under /wordlists for usernames (mutually exclusive with user)"`
	PassList string `json:"pass_list" jsonschema:"wordlist filename under /wordlists for passwords"`
}

func medusaHandler(ctx context.Context, _ *mcp.CallToolRequest, args medusaArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	module := strings.TrimSpace(args.Module)
	if target == "" || module == "" {
		return errResult("target and module are required")
	}
	var argv []string
	argv = append(argv, "-h", target)
	switch {
	case args.User != "":
		argv = append(argv, "-u", args.User)
	case args.UserList != "":
		wl, err := resolveWordlist(args.UserList)
		if err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-U", wl)
	default:
		return errResult("user or user_list is required")
	}
	passWl, err := resolveWordlist(args.PassList)
	if err != nil {
		return errResult(err.Error())
	}
	argv = append(argv, "-P", passWl, "-M", module)
	return runCaptured(ctx, "medusa", argv...)
}

type snmpwalkArgs struct {
	Target    string `json:"target" jsonschema:"the target host to enumerate via SNMP"`
	Community string `json:"community,omitempty" jsonschema:"SNMP community string (default: public)"`
	OID       string `json:"oid,omitempty" jsonschema:"optional OID subtree to walk (default: walks the whole MIB-II tree)"`
}

func snmpwalkHandler(ctx context.Context, _ *mcp.CallToolRequest, args snmpwalkArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	if err := rejectIfFlagLike("target", target); err != nil {
		return errResult(err.Error())
	}
	community := args.Community
	if community == "" {
		community = "public"
	}
	argv := []string{"-v2c", "-c", community, target}
	if oid := strings.TrimSpace(args.OID); oid != "" {
		if err := rejectIfFlagLike("oid", oid); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, oid)
	}
	return runCaptured(ctx, "snmpwalk", argv...)
}

type dirbArgs struct {
	URL      string `json:"url" jsonschema:"the target URL to enumerate"`
	Wordlist string `json:"wordlist,omitempty" jsonschema:"wordlist filename under /wordlists (default: dirb's own common.txt if omitted)"`
}

func dirbHandler(ctx context.Context, _ *mcp.CallToolRequest, args dirbArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	argv := []string{url}
	if args.Wordlist != "" {
		wl, err := resolveWordlist(args.Wordlist)
		if err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, wl)
	}
	return runCaptured(ctx, "dirb", argv...)
}

// registerExtendedTools wires the second wave of cataloged tools. Called
// from registerTools in tools.go.
func registerExtendedTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "amass_enum",
		Description: "Enumerate subdomains for a domain with amass.",
	}, amassHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "subfinder_enum",
		Description: "Enumerate subdomains for a domain with subfinder.",
	}, subfinderHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "theharvester_scan",
		Description: "Harvest emails, hosts, and subdomains for a domain with theHarvester.",
	}, theharvesterHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "dnsenum_scan",
		Description: "Enumerate DNS records for a domain with dnsenum.",
	}, dnsenumHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "enum4linux_scan",
		Description: "Enumerate SMB/Windows/Samba information on a target with enum4linux.",
	}, enum4linuxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "onesixtyone_scan",
		Description: "Probe a target for SNMP with onesixtyone.",
	}, onesixtyoneHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "wafw00f_scan",
		Description: "Fingerprint a web application firewall on a URL with wafw00f.",
	}, wafw00fHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "wpscan_scan",
		Description: "Scan a WordPress site for known vulnerabilities with wpscan.",
	}, wpscanHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "nuclei_scan",
		Description: "Scan a target with nuclei's vulnerability templates.",
	}, nucleiHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "sqlmap_scan",
		Description: "Test a URL for SQL injection with sqlmap (non-interactive batch mode).",
	}, sqlmapHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "hydra_bruteforce",
		Description: "Brute-force credentials against a network service with hydra.",
	}, hydraHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "medusa_bruteforce",
		Description: "Brute-force credentials against a network service with medusa.",
	}, medusaHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "dirb_scan",
		Description: "Enumerate web content with dirb.",
	}, dirbHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "snmpwalk_enum",
		Description: "Enumerate SNMP MIB data on a target with snmpwalk once a community string is known — pair with onesixtyone_scan, which finds the community string first.",
	}, snmpwalkHandler)
}
