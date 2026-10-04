package sandboxmcp

import (
	"strings"
	"testing"
)

func TestTarget(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
		ok   bool
	}{
		{"nmap_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", true},
		{"nmap_scan", map[string]any{"target": ""}, "", false},
		{"gobuster_dir", map[string]any{"url": "http://x"}, "http://x", true},
		{"unknown_tool", map[string]any{"target": "x"}, "", false},
	}
	for _, c := range cases {
		got, ok := Target(c.tool, c.args)
		if got != c.want || ok != c.ok {
			t.Errorf("Target(%q, %v) = (%q, %v), want (%q, %v)", c.tool, c.args, got, ok, c.want, c.ok)
		}
	}
}

func TestNeedsTarget(t *testing.T) {
	cases := []struct {
		tool string
		want bool
	}{
		{"nmap_scan", true},
		{"checksec_audit", false},
		{"radare2_disasm", false},
		{"consolidate_hosts", false},
		{"unknown_tool", false},
	}
	for _, c := range cases {
		if got := NeedsTarget(c.tool); got != c.want {
			t.Errorf("NeedsTarget(%q) = %v, want %v", c.tool, got, c.want)
		}
	}
}

func TestConsolidateHosts_TargetAndRender(t *testing.T) {
	args := map[string]any{
		"input_files": []string{"/home/pentester/work/x/a.txt", "/home/pentester/work/x/b.txt"},
		"domain":      "example.com",
		"output_file": "/home/pentester/work/x/union.txt",
	}
	if _, ok := Target("consolidate_hosts", args); ok {
		t.Fatal("consolidate_hosts has no target field — Target() should report not-ok")
	}
	want := "consolidate_hosts domain=example.com inputs=[/home/pentester/work/x/a.txt, /home/pentester/work/x/b.txt] -> /home/pentester/work/x/union.txt"
	if got := RenderCommand("consolidate_hosts", args); got != want {
		t.Errorf("RenderCommand = %q, want %q", got, want)
	}
	args["high_confidence_only"] = true
	if got := RenderCommand("consolidate_hosts", args); !strings.HasSuffix(got, "(high-confidence only)") {
		t.Errorf("RenderCommand with high_confidence_only = %q, want a trailing note", got)
	}
}

func TestPassiveReconBatch_TargetAndRender(t *testing.T) {
	args := map[string]any{
		"domain":     "example.com",
		"output_dir": "/home/pentester/work/example.com",
	}
	target, ok := Target("passive_recon_batch", args)
	if !ok || target != "example.com" {
		t.Fatalf("Target(passive_recon_batch) = (%q, %v), want (\"example.com\", true)", target, ok)
	}
	want := "passive_recon_batch domain=example.com sources=[subfinder, amass, theharvester, gau] -> /home/pentester/work/example.com/"
	if got := RenderCommand("passive_recon_batch", args); got != want {
		t.Errorf("RenderCommand (default sources) = %q, want %q", got, want)
	}
	args["sources"] = []string{"amass", "gau"}
	want = "passive_recon_batch domain=example.com sources=[amass, gau] -> /home/pentester/work/example.com/"
	if got := RenderCommand("passive_recon_batch", args); got != want {
		t.Errorf("RenderCommand (explicit sources) = %q, want %q", got, want)
	}
}

func TestRenderCommand(t *testing.T) {
	got := RenderCommand("nmap_scan", map[string]any{
		"target": "10.0.0.5", "service_detection": true, "default_scripts": true, "ports": "1-1000",
	})
	want := "nmap -sV -sC -p 1-1000 10.0.0.5"
	if got != want {
		t.Errorf("RenderCommand = %q, want %q", got, want)
	}

	gotTop := RenderCommand("nmap_scan", map[string]any{
		"target": "10.0.0.5", "ports": "top-25",
	})
	wantTop := "nmap --top-ports 25 10.0.0.5"
	if gotTop != wantTop {
		t.Errorf("RenderCommand (top-ports) = %q, want %q", gotTop, wantTop)
	}
}

func TestPrimarySixCatalog_NewFieldsRender(t *testing.T) {
	cases := []struct {
		tool       string
		args       map[string]any
		wantTarget string
		wantRender string
	}{
		{
			"nmap_scan",
			map[string]any{"target": "10.0.0.5", "output_file": "/home/pentester/work/x/nmap.txt"},
			"10.0.0.5", "nmap 10.0.0.5  [saves output to /home/pentester/work/x/nmap.txt]",
		},
		{"whatweb_scan", map[string]any{"url": "http://x"}, "http://x", "whatweb http://x"},
		{"whatweb_scan", map[string]any{"url": "http://x", "aggression": 3}, "http://x", "whatweb -a 3 http://x"},
		{"curl_probe", map[string]any{"url": "http://x"}, "http://x", "curl -s -i http://x"},
		{
			"curl_probe",
			map[string]any{
				"url": "http://x/graphql", "method": "POST", "data": `{"query":"{__schema{types{name}}}"}`,
				"headers": []string{"Content-Type: application/json"},
			},
			"http://x/graphql",
			`curl -s -i -X POST -H Content-Type: application/json -d {"query":"{__schema{types{name}}}"} http://x/graphql`,
		},
		{
			"gobuster_dir",
			map[string]any{"url": "http://x", "wordlist": "common.txt", "output_file": "/home/pentester/work/x/gobuster.txt"},
			"http://x", "gobuster dir -u http://x -w common.txt  [saves output to /home/pentester/work/x/gobuster.txt]",
		},
		{"ffuf_fuzz", map[string]any{"url": "http://x", "wordlist": "common.txt"}, "http://x", "ffuf -u http://x -w common.txt -s"},
		{
			"ffuf_fuzz",
			map[string]any{"url": "http://x", "wordlist": "common.txt", "match_codes": "200,301,403"},
			"http://x", "ffuf -u http://x -w common.txt -s -mc 200,301,403",
		},
		{
			"nikto_scan",
			map[string]any{"host": "http://x", "output_file": "/home/pentester/work/x/nikto.txt"},
			"http://x", "nikto -h http://x  [saves output to /home/pentester/work/x/nikto.txt]",
		},
	}
	for _, c := range cases {
		gotTarget, ok := Target(c.tool, c.args)
		if !ok || gotTarget != c.wantTarget {
			t.Errorf("%s: Target(%v) = (%q, %v), want (%q, true)", c.tool, c.args, gotTarget, ok, c.wantTarget)
		}
		if gotRender := RenderCommand(c.tool, c.args); gotRender != c.wantRender {
			t.Errorf("%s: RenderCommand(%v) = %q, want %q", c.tool, c.args, gotRender, c.wantRender)
		}
	}
}

func TestRenderCommand_UnknownTool(t *testing.T) {
	if got := RenderCommand("nope", nil); got != "nope" {
		t.Errorf("RenderCommand(unknown) = %q, want tool name unchanged", got)
	}
}

func TestExtendedCatalog_TargetAndRender(t *testing.T) {
	cases := []struct {
		tool       string
		args       map[string]any
		wantTarget string
		wantRender string
	}{
		{"amass_enum", map[string]any{"domain": "example.com", "passive": true}, "example.com", "amass enum -d example.com -passive"},
		{
			"amass_enum",
			map[string]any{"domain": "example.com", "passive": true, "output_file": "/home/pentester/work/example.com/subs_amass.txt"},
			"example.com", "amass enum -d example.com -passive  [saves output to /home/pentester/work/example.com/subs_amass.txt]",
		},
		{"subfinder_enum", map[string]any{"domain": "example.com"}, "example.com", "subfinder -d example.com -silent"},
		{"theharvester_scan", map[string]any{"domain": "example.com"}, "example.com", "theHarvester -d example.com -b all"},
		{"httpx_probe", map[string]any{"target": "http://x"}, "http://x", "httpx -u http://x -silent -sc -server -ip -cl -location -fr"},
		{
			"httpx_probe",
			map[string]any{"target": "http://x", "title": true, "tech": true},
			"http://x", "httpx -u http://x -silent -sc -server -ip -cl -location -fr -title -td",
		},
		{"dnsx_resolve", map[string]any{"domain": "example.com"}, "example.com", "dnsx -l example.com -silent -a -resp -cname -ns -mx -retry 2"},
		{
			"dnsx_resolve",
			map[string]any{"domain": "/home/pentester/work/example.com/candidates_all.txt", "output_file": "/home/pentester/work/example.com/resolved.txt"},
			"/home/pentester/work/example.com/candidates_all.txt",
			"dnsx -l /home/pentester/work/example.com/candidates_all.txt -silent -a -resp -cname -ns -mx -retry 2  [saves output to /home/pentester/work/example.com/resolved.txt]",
		},
		{"alterx_mutate", map[string]any{"domain": "example.com"}, "example.com", "alterx -l example.com -silent"},
		{"alterx_mutate", map[string]any{"domain": "example.com", "enrich": true}, "example.com", "alterx -l example.com -silent -en"},
		{"gau_fetch", map[string]any{"domain": "example.com"}, "example.com", "gau --subs --threads 4 --timeout 10 example.com"},
		{"nuclei_scan", map[string]any{"target": "https://x", "tags": "takeover"}, "https://x", "nuclei -u https://x -tags takeover -silent"},
		{
			"nuclei_scan",
			map[string]any{"target": "/home/pentester/work/example.com/hosts.txt", "tags": "takeover"},
			"/home/pentester/work/example.com/hosts.txt",
			"nuclei -l /home/pentester/work/example.com/hosts.txt -c 15 -rate-limit 50 -bulk-size 50 -timeout 5 -retries 1 -tags takeover -silent",
		},
		{"dnsenum_scan", map[string]any{"domain": "example.com"}, "example.com", "dnsenum example.com"},
		{"enum4linux_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", "enum4linux -a 10.0.0.5"},
		{"onesixtyone_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", "onesixtyone 10.0.0.5"},
		{"wafw00f_scan", map[string]any{"url": "http://x"}, "http://x", "wafw00f http://x"},
		{"wpscan_scan", map[string]any{"url": "http://x"}, "http://x", "wpscan --url http://x"},
		{"nuclei_scan", map[string]any{"target": "http://x"}, "http://x", "nuclei -u http://x -silent"},
		{
			"nuclei_scan",
			map[string]any{"target": "http://x", "tags": "cve", "severity": "high,critical"},
			"http://x", "nuclei -u http://x -tags cve -severity high,critical -silent",
		},
		{"katana_crawl", map[string]any{"url": "http://x"}, "http://x", "katana -u http://x -d 2 -jc -silent"},
		{"katana_crawl", map[string]any{"url": "http://x", "depth": 3}, "http://x", "katana -u http://x -d 3 -jc -silent"},
		{
			"katana_crawl",
			map[string]any{"url": "http://x", "output_file": "/home/pentester/work/example.com/katana.txt"},
			"http://x", "katana -u http://x -d 2 -jc -silent  [saves output to /home/pentester/work/example.com/katana.txt]",
		},
		{"dalfox_scan", map[string]any{"url": "http://x?p=1"}, "http://x?p=1", "dalfox url http://x?p=1 --silence"},
		{"sqlmap_scan", map[string]any{"url": "http://x?id=1"}, "http://x?id=1", "sqlmap -u http://x?id=1 --batch"},
		{"dirb_scan", map[string]any{"url": "http://x"}, "http://x", "dirb http://x"},
		{"dirb_scan", map[string]any{"url": "http://x", "wordlist": "common.txt"}, "http://x", "dirb http://x common.txt"},
		{
			"hydra_bruteforce",
			map[string]any{"target": "10.0.0.5", "service": "ssh", "user": "admin", "pass_list": "rockyou.txt"},
			"10.0.0.5", "hydra -l admin -P rockyou.txt 10.0.0.5 ssh",
		},
		{
			"medusa_bruteforce",
			map[string]any{"target": "10.0.0.5", "module": "ssh", "user_list": "users.txt", "pass_list": "rockyou.txt"},
			"10.0.0.5", "medusa -h 10.0.0.5 -U users.txt -P rockyou.txt -M ssh",
		},
		{"snmpwalk_enum", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", "snmpwalk -v2c -c public 10.0.0.5"},
		{
			"snmpwalk_enum",
			map[string]any{"target": "10.0.0.5", "community": "private", "oid": "1.3.6.1.2.1.1"},
			"10.0.0.5", "snmpwalk -v2c -c private 10.0.0.5 1.3.6.1.2.1.1",
		},
		{"arjun_scan", map[string]any{"url": "https://example.com/api", "method": "POST"}, "https://example.com/api", "arjun -u https://example.com/api -m POST"},
		{"naabu_scan", map[string]any{"target": "example.com", "ports": "80,443"}, "example.com", "naabu -host example.com -silent -p 80,443"},
		{"tlsx_scan", map[string]any{"target": "example.com", "san": true, "jarm": true}, "example.com", "tlsx -u example.com -silent -san -jarm"},
		{"cloud_enum_scan", map[string]any{"keyword": "acme", "quick_scan": true}, "acme", "cloud_enum -k acme -q"},
		{"jwt_tool_inspect", map[string]any{"token": "eyJhbGciOi...", "mode": "at"}, "", "jwt_tool eyJhbGciOi... -M at"},
		{"subzy_scan", map[string]any{"target": "example.com"}, "example.com", "subzy run --target example.com"},
		{"jsluice_scan", map[string]any{"url": "https://example.com/app.js", "mode": "secrets"}, "https://example.com/app.js", "jsluice secrets https://example.com/app.js"},
		{"graphql_cop_scan", map[string]any{"url": "https://example.com/graphql"}, "https://example.com/graphql", "graphql-cop -t https://example.com/graphql"},
		{"corsy_scan", map[string]any{"url": "https://example.com"}, "https://example.com", "corsy -u https://example.com"},
		{
			"screenshot_triage",
			map[string]any{"target": "/home/pentester/work/example.com/resolved_hosts.txt", "output_dir": "/home/pentester/work/example.com/shots"},
			"/home/pentester/work/example.com/resolved_hosts.txt",
			"httpx -l /home/pentester/work/example.com/resolved_hosts.txt -ss -system-chrome -sc -title -td -server  [screenshots saved to /home/pentester/work/example.com/shots]",
		},
	}
	for _, c := range cases {
		gotTarget, ok := Target(c.tool, c.args)
		wantOk := c.wantTarget != ""
		if ok != wantOk || gotTarget != c.wantTarget {
			t.Errorf("%s: Target = (%q, %v), want (%q, %v)", c.tool, gotTarget, ok, c.wantTarget, wantOk)
		}
		if gotRender := RenderCommand(c.tool, c.args); gotRender != c.wantRender {
			t.Errorf("%s: RenderCommand = %q, want %q", c.tool, gotRender, c.wantRender)
		}
	}
}
