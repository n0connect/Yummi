package sandboxmcp

import (
	"fmt"
	"strings"
)

// ToolSpec names the argument field that carries a cataloged tool's network
// target and how to render its structured arguments into a human-readable
// command line for the execution record, approval prompt, and audit trail.
type ToolSpec struct {
	TargetField string
	Render      func(args map[string]any) string
}

var catalog = map[string]ToolSpec{
	"nmap_scan":         {TargetField: "target", Render: renderNmap},
	"whatweb_scan":      {TargetField: "url", Render: renderWhatweb},
	"curl_probe":        {TargetField: "url", Render: renderCurl},
	"gobuster_dir":      {TargetField: "url", Render: renderGobuster},
	"ffuf_fuzz":         {TargetField: "url", Render: renderFfuf},
	"nikto_scan":        {TargetField: "host", Render: renderNikto},
	"amass_enum":        {TargetField: "domain", Render: renderAmass},
	"subfinder_enum":    {TargetField: "domain", Render: renderSubfinder},
	"theharvester_scan": {TargetField: "domain", Render: renderTheharvester},
	"dnsenum_scan":      {TargetField: "domain", Render: renderDnsenum},
	"enum4linux_scan":   {TargetField: "target", Render: renderEnum4linux},
	"onesixtyone_scan":  {TargetField: "target", Render: renderOnesixtyone},
	"wafw00f_scan":      {TargetField: "url", Render: renderWafw00f},
	"wpscan_scan":       {TargetField: "url", Render: renderWpscan},
	"nuclei_scan":       {TargetField: "target", Render: renderNuclei},
	"sqlmap_scan":       {TargetField: "url", Render: renderSqlmap},
	"hydra_bruteforce":  {TargetField: "target", Render: renderHydra},
	"medusa_bruteforce": {TargetField: "target", Render: renderMedusa},
	"dirb_scan":         {TargetField: "url", Render: renderDirb},
	"snmpwalk_enum":     {TargetField: "target", Render: renderSnmpwalk},
	"httpx_probe":       {TargetField: "target", Render: renderHttpx},
	"dnsx_resolve":      {TargetField: "domain", Render: renderDnsx},
	"alterx_mutate":     {TargetField: "domain", Render: renderAlterx},
	"gau_fetch":         {TargetField: "domain", Render: renderGau},
	"katana_crawl":      {TargetField: "url", Render: renderKatana},
	"dalfox_scan":       {TargetField: "url", Render: renderDalfox},
	"checksec_audit":    {TargetField: "", Render: renderChecksec},
	"radare2_disasm":    {TargetField: "", Render: renderRadare2},
}

// Target returns the network target named by tool's schema, or ("", false)
// for an unknown tool or a missing/empty target field.
func Target(tool string, args map[string]any) (string, bool) {
	spec, ok := catalog[tool]
	if !ok {
		return "", false
	}
	v, _ := args[spec.TargetField].(string)
	return v, v != ""
}

// RenderCommand turns args into a human-readable command line. Falls back to
// the bare tool name for an unrecognized tool.
func RenderCommand(tool string, args map[string]any) string {
	spec, ok := catalog[tool]
	if !ok {
		return tool
	}
	return spec.Render(args)
}

func renderNmap(a map[string]any) string {
	var parts []string
	if b, _ := a["service_detection"].(bool); b {
		parts = append(parts, "-sV")
	}
	if b, _ := a["default_scripts"].(bool); b {
		parts = append(parts, "-sC")
	}
	if p, _ := a["ports"].(string); p != "" {
		parts = append(parts, "-p", p)
	}
	target, _ := a["target"].(string)
	parts = append(parts, target)
	return "nmap " + strings.Join(parts, " ")
}

func renderWhatweb(a map[string]any) string {
	url, _ := a["url"].(string)
	return "whatweb " + url
}

func renderCurl(a map[string]any) string {
	var parts []string
	if b, _ := a["headers_only"].(bool); b {
		parts = append(parts, "-I")
	} else {
		parts = append(parts, "-i")
	}
	if b, _ := a["follow_redirects"].(bool); b {
		parts = append(parts, "-L")
	}
	url, _ := a["url"].(string)
	parts = append(parts, url)
	return "curl -s " + strings.Join(parts, " ")
}

func renderGobuster(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	s := fmt.Sprintf("gobuster dir -u %s -w %s", url, wl)
	if ext, _ := a["extensions"].(string); ext != "" {
		s += " -x " + ext
	}
	return s
}

func renderFfuf(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	return fmt.Sprintf("ffuf -u %s -w %s", url, wl)
}

func renderNikto(a map[string]any) string {
	host, _ := a["host"].(string)
	return "nikto -h " + host
}

func renderAmass(a map[string]any) string {
	domain, _ := a["domain"].(string)
	s := "amass enum -d " + domain
	if b, _ := a["passive"].(bool); b {
		s += " -passive"
	}
	return s
}

func renderSubfinder(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return "subfinder -d " + domain + " -silent"
}

func renderTheharvester(a map[string]any) string {
	domain, _ := a["domain"].(string)
	source, _ := a["source"].(string)
	if source == "" {
		source = "all"
	}
	return fmt.Sprintf("theHarvester -d %s -b %s", domain, source)
}

func renderDnsenum(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return "dnsenum " + domain
}

func renderEnum4linux(a map[string]any) string {
	target, _ := a["target"].(string)
	return "enum4linux -a " + target
}

func renderOnesixtyone(a map[string]any) string {
	target, _ := a["target"].(string)
	return "onesixtyone " + target
}

func renderWafw00f(a map[string]any) string {
	url, _ := a["url"].(string)
	return "wafw00f " + url
}

func renderWpscan(a map[string]any) string {
	url, _ := a["url"].(string)
	return "wpscan --url " + url
}

func renderNuclei(a map[string]any) string {
	target, _ := a["target"].(string)
	return "nuclei -u " + target
}

func renderSqlmap(a map[string]any) string {
	url, _ := a["url"].(string)
	return "sqlmap -u " + url + " --batch"
}

func renderHydra(a map[string]any) string {
	target, _ := a["target"].(string)
	service, _ := a["service"].(string)
	passList, _ := a["pass_list"].(string)
	var parts []string
	if user, _ := a["user"].(string); user != "" {
		parts = append(parts, "-l", user)
	} else if userList, _ := a["user_list"].(string); userList != "" {
		parts = append(parts, "-L", userList)
	}
	parts = append(parts, "-P", passList, target, service)
	return "hydra " + strings.Join(parts, " ")
}

func renderMedusa(a map[string]any) string {
	target, _ := a["target"].(string)
	module, _ := a["module"].(string)
	passList, _ := a["pass_list"].(string)
	parts := []string{"-h", target}
	if user, _ := a["user"].(string); user != "" {
		parts = append(parts, "-u", user)
	} else if userList, _ := a["user_list"].(string); userList != "" {
		parts = append(parts, "-U", userList)
	}
	parts = append(parts, "-P", passList, "-M", module)
	return "medusa " + strings.Join(parts, " ")
}

func renderDirb(a map[string]any) string {
	url, _ := a["url"].(string)
	s := "dirb " + url
	if wl, _ := a["wordlist"].(string); wl != "" {
		s += " " + wl
	}
	return s
}

func renderSnmpwalk(a map[string]any) string {
	target, _ := a["target"].(string)
	community, _ := a["community"].(string)
	if community == "" {
		community = "public"
	}
	s := fmt.Sprintf("snmpwalk -v2c -c %s %s", community, target)
	if oid, _ := a["oid"].(string); oid != "" {
		s += " " + oid
	}
	return s
}

func renderHttpx(a map[string]any) string {
	target, _ := a["target"].(string)
	s := "httpx -u " + target + " -silent -sc"
	if b, _ := a["title"].(bool); b {
		s += " -title"
	}
	if b, _ := a["tech"].(bool); b {
		s += " -td"
	}
	return s
}

func renderDnsx(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return "dnsx -l " + domain + " -silent -a -resp"
}

func renderAlterx(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return "alterx -l " + domain + " -silent"
}

func renderGau(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return "gau --subs --threads 4 --timeout 10 " + domain
}

func renderKatana(a map[string]any) string {
	url, _ := a["url"].(string)
	return "katana -u " + url + " -d 2 -jc -silent"
}

func renderDalfox(a map[string]any) string {
	url, _ := a["url"].(string)
	return "dalfox url " + url + " --silence"
}

func renderChecksec(a map[string]any) string {
	file, _ := a["file"].(string)
	return "checksec --file=" + file
}

func renderRadare2(a map[string]any) string {
	file, _ := a["file"].(string)
	cmd, _ := a["command"].(string)
	if cmd == "" {
		cmd = "aaa; afl"
	}
	return fmt.Sprintf("r2 -q -c %q %s", cmd, file)
}
