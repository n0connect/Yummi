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

// amassBinary bypasses Kali's /usr/bin/amass wrapper, which unconditionally
// runs `sudo libpostal_data download all ...` on every invocation when
// /usr/share/libpostal/transliteration is missing (it always is here — that
// dataset is a ~1GB+ multi-part download, not worth baking into the image
// for a dependency amass's own enumeration doesn't need). Confirmed live:
// the wrapper hard-fails non-interactively ("sudo: a terminal is required to
// read the password") for every single call in this non-root, no-sudo
// sandbox, while the real binary underneath enumerates correctly without
// that data at all. var, not const: overridable in tests.
var amassBinary = "/usr/lib/amass/amass"

func amassHandler(ctx context.Context, _ *mcp.CallToolRequest, args amassArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	argv := []string{"enum", "-d", domain}
	if args.Passive {
		argv = append(argv, "-passive")
	}
	return runCaptured(ctx, amassBinary, argv...)
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

type httpxArgs struct {
	Target string `json:"target" jsonschema:"the target URL or host to probe with httpx"`
	Title  bool   `json:"title,omitempty" jsonschema:"extract page title"`
	Tech   bool   `json:"tech,omitempty" jsonschema:"detect technologies"`
}

func httpxHandler(ctx context.Context, _ *mcp.CallToolRequest, args httpxArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	argv := []string{"-u", target, "-silent", "-sc"}
	if args.Title {
		argv = append(argv, "-title")
	}
	if args.Tech {
		argv = append(argv, "-td")
	}
	return runCaptured(ctx, "httpx", argv...)
}

type dnsxArgs struct {
	Domain string `json:"domain" jsonschema:"the target domain or host to resolve"`
}

func dnsxHandler(ctx context.Context, _ *mcp.CallToolRequest, args dnsxArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	// -l (not -d): dnsx's -d/-domain is brute-force mode and demands a
	// wordlist ("-FTL- missing wordlist(w) flag required with domain(d)
	// input", confirmed live) — -l resolves the given host(s) directly,
	// which is this tool's actual job.
	return runCaptured(ctx, "dnsx", "-l", domain, "-silent", "-a", "-resp")
}

type alterxArgs struct {
	Domain string `json:"domain" jsonschema:"the seed domain name for mutation"`
}

func alterxHandler(ctx context.Context, _ *mcp.CallToolRequest, args alterxArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	// -l (not -target, which doesn't exist): confirmed live —
	// "flag provided but not defined: -target". -l is the seed
	// subdomain/hostname list alterx actually mutates.
	return runCaptured(ctx, "alterx", "-l", domain, "-silent")
}

type dalfoxArgs struct {
	URL string `json:"url" jsonschema:"the target URL (with a query parameter) to scan for reflected/DOM XSS"`
}

func dalfoxHandler(ctx context.Context, _ *mcp.CallToolRequest, args dalfoxArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	return runCaptured(ctx, "dalfox", "url", url, "--silence", "--no-color")
}

type gauArgs struct {
	Domain string `json:"domain" jsonschema:"the target domain to fetch historical URLs for"`
}

func gauHandler(ctx context.Context, _ *mcp.CallToolRequest, args gauArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	// gau's own defaults are --threads 1 (its 4 providers run serially) and
	// --timeout 45 (per HTTP request) — confirmed live: an unflagged run
	// against a real domain took 45s+ and felt hung. --threads 4 runs the
	// providers in parallel; --timeout 10 keeps one slow/unresponsive
	// provider (urlscan.io in particular often needs an API key) from
	// stalling the whole call — each provider individually returns in
	// under 12s against a real domain.
	return runCaptured(ctx, "gau", "--subs", "--threads", "4", "--timeout", "10", domain)
}

type katanaArgs struct {
	URL string `json:"url" jsonschema:"the target URL to crawl with katana"`
}

func katanaHandler(ctx context.Context, _ *mcp.CallToolRequest, args katanaArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	return runCaptured(ctx, "katana", "-u", url, "-d", "2", "-jc", "-silent")
}

type checksecArgs struct {
	File string `json:"file" jsonschema:"the binary file path to audit protections for"`
}

func checksecHandler(ctx context.Context, _ *mcp.CallToolRequest, args checksecArgs) (*mcp.CallToolResult, any, error) {
	file := strings.TrimSpace(args.File)
	if file == "" {
		return errResult("file is required")
	}
	return runCaptured(ctx, "checksec", "--file="+file)
}

type radare2Args struct {
	File    string `json:"file" jsonschema:"the binary file path to analyze with radare2"`
	Command string `json:"command,omitempty" jsonschema:"the radare2 commands to execute (e.g. 'aaa; afl' or 'aaa; s main; pdf')"`
}

func radare2Handler(ctx context.Context, _ *mcp.CallToolRequest, args radare2Args) (*mcp.CallToolResult, any, error) {
	file := strings.TrimSpace(args.File)
	if file == "" {
		return errResult("file is required")
	}
	if err := rejectIfFlagLike("file", file); err != nil {
		return errResult(err.Error())
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		cmd = "aaa; afl"
	}
	return runCaptured(ctx, "r2", "-q", "-c", cmd, file)
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "httpx_probe",
		Description: "Probe a URL or host for HTTP status, title, technology stack, and web server with httpx.",
	}, httpxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "dnsx_resolve",
		Description: "Resolve domain and hostnames for A and CNAME records with dnsx.",
	}, dnsxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "alterx_mutate",
		Description: "Generate subdomain mutations and permutations with alterx.",
	}, alterxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "gau_fetch",
		Description: "Fetch historical and indexed URLs for a domain from Wayback, AlienVault, and CommonCrawl with gau.",
	}, gauHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "dalfox_scan",
		Description: "Scan a URL's query parameters for reflected and DOM-based XSS with dalfox.",
	}, dalfoxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "katana_crawl",
		Description: "Crawl web applications, extract endpoints, and scrape JavaScript links with katana.",
	}, katanaHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "checksec_audit",
		Description: "Audit binary security mitigations (RELRO, Stack Canary, NX, PIE) with checksec.",
	}, checksecHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "radare2_disasm",
		Description: "Analyze and disassemble binaries non-interactively with radare2 (r2).",
	}, radare2Handler)
}
