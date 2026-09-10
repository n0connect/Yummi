package main

import (
	"context"
	"fmt"
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

// Every output_file field below repeats the same jsonschema description —
// struct tags can't reference a shared const, and each tool's schema needs
// the guidance regardless of which one the model reads first: sandbox-mcp's
// own working directory is fixed at /home/pentester, so a path relative to
// wherever a separate shell command "cd"s into would silently save to the
// wrong place. See runCapturedToFile/saveNote in capture.go for the actual
// write-through — the same already-captured text the model reads is written
// verbatim, not re-derived from the underlying tool's own -o/-f convention.

type amassArgs struct {
	Domain     string `json:"domain" jsonschema:"the target domain to enumerate"`
	Passive    bool   `json:"passive,omitempty" jsonschema:"pass -passive for passive-only enumeration (no direct target contact)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, e.g. /home/pentester/work/<domain>/subs_amass.txt, for a later step to use as file input"`
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
	return runCapturedToFile(ctx, args.OutputFile, amassBinary, argv...)
}

type subfinderArgs struct {
	Domain     string `json:"domain" jsonschema:"the target domain to enumerate subdomains for"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, e.g. /home/pentester/work/<domain>/subs_subfinder.txt, for a later step to use as file input"`
}

func subfinderHandler(ctx context.Context, _ *mcp.CallToolRequest, args subfinderArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	return runCapturedToFile(ctx, args.OutputFile, "subfinder", "-d", domain, "-silent")
}

type theharvesterArgs struct {
	Domain     string `json:"domain" jsonschema:"the target domain to harvest emails/hosts for"`
	Source     string `json:"source,omitempty" jsonschema:"data source, e.g. google, bing, or all (default: all)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, e.g. /home/pentester/work/<domain>/theharvester.txt, for a later step to use as file input"`
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
	return runCapturedToFile(ctx, args.OutputFile, "theHarvester", "-d", domain, "-b", source)
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
	URL        string `json:"url" jsonschema:"the target WordPress site URL"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
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
	return runCapturedEnvToFile(ctx, []string{"HOME=/tmp"}, args.OutputFile, "wpscan", "--url", url)
}

type nucleiArgs struct {
	Target     string `json:"target" jsonschema:"the target URL or host to scan, OR an absolute path to a file of many targets (one per line) for a bulk scan — sandbox-mcp's own working directory is fixed at /home/pentester, so a relative file path here won't resolve"`
	Tags       string `json:"tags,omitempty" jsonschema:"comma-separated nuclei template tags to scope the scan, e.g. 'takeover' or 'cve,exposure' — omit to run nuclei's full default template set, which is thorough but slow, especially in bulk mode"`
	Severity   string `json:"severity,omitempty" jsonschema:"optional comma-separated severity filter, e.g. 'low,medium,high,critical' — narrows a tag-scoped scan to only the findings worth immediate attention"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func nucleiHandler(ctx context.Context, _ *mcp.CallToolRequest, args nucleiArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	var argv []string
	if strings.HasPrefix(target, "/") {
		// An absolute path means bulk mode: -l instead of -u. A scan against
		// many hosts needs tighter per-host timing, or a handful of slow/
		// unresponsive hosts can exhaust the whole scan — confirmed live: an
		// untuned bulk run against ~3,000 mostly-NXDOMAIN candidates never
		// finished inside a 300s wrapper. These values are what a live agent
		// session converged on after two timeouts. Kept as one field (not a
		// separate target/list pair) so this tool's target field is always
		// populated for the orchestrator's own gateway scope check, the same
		// way dnsx_resolve/alterx_mutate's "domain" field already is.
		argv = append(argv, "-l", target, "-c", "15", "-rate-limit", "50", "-bulk-size", "50", "-timeout", "5", "-retries", "1")
	} else {
		if err := rejectIfFlagLike("target", target); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-u", target)
	}
	if tags := strings.TrimSpace(args.Tags); tags != "" {
		if err := rejectIfFlagLike("tags", tags); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-tags", tags)
	}
	if severity := strings.TrimSpace(args.Severity); severity != "" {
		if err := rejectIfFlagLike("severity", severity); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-severity", severity)
	}
	argv = append(argv, "-silent")
	return runCapturedToFile(ctx, args.OutputFile, "nuclei", argv...)
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
	Target     string `json:"target" jsonschema:"the target URL, host, or an absolute path to a file of hosts (one per line) to probe with httpx"`
	Title      bool   `json:"title,omitempty" jsonschema:"extract page title"`
	Tech       bool   `json:"tech,omitempty" jsonschema:"detect technologies"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func httpxHandler(ctx context.Context, _ *mcp.CallToolRequest, args httpxArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	// -server/-ip/-cl/-location/-fr are all display-only (what the response
	// looked like), never a scan-scope change, so they're always on — a
	// senior bug-bounty triage step needs the tech/redirect/IP fingerprint
	// every time, not as an opt-in the model has to remember to request.
	argv := []string{"-u", target, "-silent", "-sc", "-server", "-ip", "-cl", "-location", "-fr"}
	if args.Title {
		argv = append(argv, "-title")
	}
	if args.Tech {
		argv = append(argv, "-td")
	}
	return runCapturedToFile(ctx, args.OutputFile, "httpx", argv...)
}

type dnsxArgs struct {
	Domain     string `json:"domain" jsonschema:"the target domain, host, or an absolute path to a file of hosts (one per line) to resolve"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
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
	// which is this tool's actual job. -cname/-ns/-mx cost nothing extra
	// (same query round-trip, dnsx just reports more of what it already
	// received) and -cname specifically is the one record type a
	// subdomain-takeover check actually depends on — a bare -a-only
	// resolve can never surface a dangling CNAME. -retry 2 is the value a
	// live agent session needed for reliable results against a real domain.
	return runCapturedToFile(ctx, args.OutputFile, "dnsx", "-l", domain, "-silent", "-a", "-resp", "-cname", "-ns", "-mx", "-retry", "2")
}

type alterxArgs struct {
	Domain     string `json:"domain" jsonschema:"the seed domain name, or an absolute path to a file of seed hostnames (one per line), for mutation"`
	Enrich     bool   `json:"enrich,omitempty" jsonschema:"pass -en to extract additional words from the seed input itself, producing more realistic permutations"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
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
	argv := []string{"-l", domain, "-silent"}
	if args.Enrich {
		argv = append(argv, "-en")
	}
	return runCapturedToFile(ctx, args.OutputFile, "alterx", argv...)
}

type dalfoxArgs struct {
	URL        string `json:"url" jsonschema:"the target URL (with a query parameter) to scan for reflected/DOM XSS"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
}

func dalfoxHandler(ctx context.Context, _ *mcp.CallToolRequest, args dalfoxArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	return runCapturedToFile(ctx, args.OutputFile, "dalfox", "url", url, "--silence", "--no-color")
}

type gauArgs struct {
	Domain     string `json:"domain" jsonschema:"the target domain to fetch historical URLs for"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step to use as file input"`
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
	return runCapturedToFile(ctx, args.OutputFile, "gau", "--subs", "--threads", "4", "--timeout", "10", domain)
}

type katanaArgs struct {
	URL        string `json:"url" jsonschema:"the target URL to crawl with katana"`
	Depth      int    `json:"depth,omitempty" jsonschema:"optional crawl depth (default: 2; a wider surface may warrant 3)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to, for a later step (e.g. filtering for .js URLs) to use as file input"`
}

func katanaHandler(ctx context.Context, _ *mcp.CallToolRequest, args katanaArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 2
	}
	return runCapturedToFile(ctx, args.OutputFile, "katana", "-u", url, "-d", fmt.Sprint(depth), "-jc", "-silent")
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

type arjunArgs struct {
	URL        string `json:"url" jsonschema:"the target URL to discover query/body parameters for"`
	Method     string `json:"method,omitempty" jsonschema:"HTTP method to test: GET, POST, or JSON (default: GET)"`
	Wordlist   string `json:"wordlist,omitempty" jsonschema:"optional custom parameter wordlist name under /wordlists"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func arjunHandler(ctx context.Context, _ *mcp.CallToolRequest, args arjunArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	argv := []string{"-u", url}
	if m := strings.TrimSpace(args.Method); m != "" {
		if err := rejectIfFlagLike("method", m); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-m", m)
	}
	if args.Wordlist != "" {
		wl, err := resolveWordlist(args.Wordlist)
		if err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-w", wl)
	}
	return runCapturedToFile(ctx, args.OutputFile, "arjun", argv...)
}

type naabuArgs struct {
	Target     string `json:"target" jsonschema:"the target host or IP to scan for open ports"`
	Ports      string `json:"ports,omitempty" jsonschema:"port range or list, e.g. 80,443 or top-100 or 1-1000"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func naabuHandler(ctx context.Context, _ *mcp.CallToolRequest, args naabuArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	argv := []string{"-host", target, "-silent"}
	if p := strings.TrimSpace(args.Ports); p != "" {
		if err := rejectIfFlagLike("ports", p); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-p", p)
	}
	return runCapturedToFile(ctx, args.OutputFile, "naabu", argv...)
}

type tlsxArgs struct {
	Target     string `json:"target" jsonschema:"the target host or URL to probe for TLS/SSL information"`
	SAN        bool   `json:"san,omitempty" jsonschema:"pass -san to extract Subject Alternative Names from the certificate"`
	Jarm       bool   `json:"jarm,omitempty" jsonschema:"pass -jarm to compute the JARM fingerprint"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func tlsxHandler(ctx context.Context, _ *mcp.CallToolRequest, args tlsxArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	argv := []string{"-u", target, "-silent"}
	if args.SAN {
		argv = append(argv, "-san")
	}
	if args.Jarm {
		argv = append(argv, "-jarm")
	}
	return runCapturedToFile(ctx, args.OutputFile, "tlsx", argv...)
}

type cloudEnumArgs struct {
	Keyword    string `json:"keyword" jsonschema:"keyword or company/brand name to search for across AWS, Azure, and GCP"`
	QuickScan  bool   `json:"quick_scan,omitempty" jsonschema:"pass -q to run quick scan with second-level mutations disabled"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func cloudEnumHandler(ctx context.Context, _ *mcp.CallToolRequest, args cloudEnumArgs) (*mcp.CallToolResult, any, error) {
	kw := strings.TrimSpace(args.Keyword)
	if kw == "" {
		return errResult("keyword is required")
	}
	if err := rejectIfFlagLike("keyword", kw); err != nil {
		return errResult(err.Error())
	}
	argv := []string{"-k", kw}
	if args.QuickScan {
		argv = append(argv, "-q")
	}
	return runCapturedToFile(ctx, args.OutputFile, "cloud_enum", argv...)
}

type jwtToolArgs struct {
	Token      string `json:"token" jsonschema:"the raw JWT token string to inspect and test"`
	Mode       string `json:"mode,omitempty" jsonschema:"test mode (e.g. 'at' for All Tests / tampering, 't' for tamper, 'b' for dictionary brute-force)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func jwtToolHandler(ctx context.Context, _ *mcp.CallToolRequest, args jwtToolArgs) (*mcp.CallToolResult, any, error) {
	tok := strings.TrimSpace(args.Token)
	if tok == "" {
		return errResult("token is required")
	}
	if err := rejectIfFlagLike("token", tok); err != nil {
		return errResult(err.Error())
	}
	argv := []string{tok}
	if m := strings.TrimSpace(args.Mode); m != "" {
		if err := rejectIfFlagLike("mode", m); err != nil {
			return errResult(err.Error())
		}
		argv = append(argv, "-M", m)
	}
	return runCapturedToFile(ctx, args.OutputFile, "jwt_tool", argv...)
}

type subzyArgs struct {
	Target     string `json:"target" jsonschema:"the target domain or path to a targets list file"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func subzyHandler(ctx context.Context, _ *mcp.CallToolRequest, args subzyArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	argv := []string{"run", "--target", target}
	return runCapturedToFile(ctx, args.OutputFile, "subzy", argv...)
}

type jsluiceArgs struct {
	URL        string `json:"url" jsonschema:"the target JavaScript file URL or local file path"`
	Mode       string `json:"mode,omitempty" jsonschema:"extraction mode: 'urls' (extract paths and endpoints) or 'secrets' (extract API keys and tokens). Default is 'urls'"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func jsluiceHandler(ctx context.Context, _ *mcp.CallToolRequest, args jsluiceArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	mode := strings.TrimSpace(args.Mode)
	if mode != "secrets" {
		mode = "urls"
	}
	return runCapturedToFile(ctx, args.OutputFile, "jsluice", mode, url)
}

type graphqlCopArgs struct {
	URL        string `json:"url" jsonschema:"the target GraphQL endpoint URL (e.g. https://example.com/graphql)"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func graphqlCopHandler(ctx context.Context, _ *mcp.CallToolRequest, args graphqlCopArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", url); err != nil {
		return errResult(err.Error())
	}
	return runCapturedToFile(ctx, args.OutputFile, "graphql-cop", "-t", url)
}

type corsyArgs struct {
	URL        string `json:"url" jsonschema:"the target URL to analyze for CORS misconfigurations"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path to also save this tool's raw output to"`
}

func corsyHandler(ctx context.Context, _ *mcp.CallToolRequest, args corsyArgs) (*mcp.CallToolResult, any, error) {
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return errResult("url is required")
	}
	argv := []string{"-u", url}
	return runCapturedToFile(ctx, args.OutputFile, "corsy", argv...)
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "arjun_scan",
		Description: "Discover hidden HTTP parameters for a URL with arjun.",
	}, arjunHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "naabu_scan",
		Description: "Fast port discovery on a target host or IP with naabu.",
	}, naabuHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tlsx_scan",
		Description: "Probe TLS/SSL certificates, extract SAN hostnames, and calculate JARM fingerprints with tlsx.",
	}, tlsxHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cloud_enum_scan",
		Description: "Enumerate multi-cloud assets in AWS S3, Azure Blob Storage, and Google Cloud Storage with cloud_enum.",
	}, cloudEnumHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "jwt_tool_inspect",
		Description: "Analyze, tamper, and audit JSON Web Tokens (JWT) with jwt_tool.",
	}, jwtToolHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "subzy_scan",
		Description: "Check a domain or target list for subdomain takeover vulnerabilities with subzy.",
	}, subzyHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "jsluice_scan",
		Description: "Extract URLs, endpoints, and secrets from JavaScript files with jsluice.",
	}, jsluiceHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "graphql_cop_scan",
		Description: "Audit GraphQL endpoints for security flaws, introspection, and misconfigurations with graphql-cop.",
	}, graphqlCopHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "corsy_scan",
		Description: "Analyze and detect CORS misconfigurations on a URL with corsy.",
	}, corsyHandler)
}
