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
	"nmap_scan":           {TargetField: "target", Render: renderNmap},
	"whatweb_scan":        {TargetField: "url", Render: renderWhatweb},
	"curl_probe":          {TargetField: "url", Render: renderCurl},
	"gobuster_dir":        {TargetField: "url", Render: renderGobuster},
	"ffuf_fuzz":           {TargetField: "url", Render: renderFfuf},
	"nikto_scan":          {TargetField: "host", Render: renderNikto},
	"amass_enum":          {TargetField: "domain", Render: renderAmass},
	"subfinder_enum":      {TargetField: "domain", Render: renderSubfinder},
	"theharvester_scan":   {TargetField: "domain", Render: renderTheharvester},
	"dnsenum_scan":        {TargetField: "domain", Render: renderDnsenum},
	"enum4linux_scan":     {TargetField: "target", Render: renderEnum4linux},
	"onesixtyone_scan":    {TargetField: "target", Render: renderOnesixtyone},
	"wafw00f_scan":        {TargetField: "url", Render: renderWafw00f},
	"wpscan_scan":         {TargetField: "url", Render: renderWpscan},
	"nuclei_scan":         {TargetField: "target", Render: renderNuclei},
	"sqlmap_scan":         {TargetField: "url", Render: renderSqlmap},
	"hydra_bruteforce":    {TargetField: "target", Render: renderHydra},
	"medusa_bruteforce":   {TargetField: "target", Render: renderMedusa},
	"dirb_scan":           {TargetField: "url", Render: renderDirb},
	"snmpwalk_enum":       {TargetField: "target", Render: renderSnmpwalk},
	"httpx_probe":         {TargetField: "target", Render: renderHttpx},
	"dnsx_resolve":        {TargetField: "domain", Render: renderDnsx},
	"alterx_mutate":       {TargetField: "domain", Render: renderAlterx},
	"gau_fetch":           {TargetField: "domain", Render: renderGau},
	"katana_crawl":        {TargetField: "url", Render: renderKatana},
	"dalfox_scan":         {TargetField: "url", Render: renderDalfox},
	"checksec_audit":      {TargetField: "", Render: renderChecksec},
	"radare2_disasm":      {TargetField: "", Render: renderRadare2},
	"consolidate_hosts":   {TargetField: "", Render: renderConsolidate},
	"passive_recon_batch": {TargetField: "domain", Render: renderPassiveReconBatch},
	"arjun_scan":          {TargetField: "url", Render: renderArjun},
	"naabu_scan":          {TargetField: "target", Render: renderNaabu},
	"tlsx_scan":           {TargetField: "target", Render: renderTlsx},
	"cloud_enum_scan":     {TargetField: "keyword", Render: renderCloudEnum},
	"jwt_tool_inspect":    {TargetField: "", Render: renderJwtTool},
	"subzy_scan":          {TargetField: "target", Render: renderSubzy},
	"jsluice_scan":        {TargetField: "url", Render: renderJsluice},
	"graphql_cop_scan":    {TargetField: "url", Render: renderGraphqlCop},
	"corsy_scan":          {TargetField: "url", Render: renderCorsy},
	"screenshot_triage":   {TargetField: "target", Render: renderScreenshotTriage},
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

// NeedsTarget reports whether tool's schema names a network-target field at
// all — false for a tool with TargetField "" (checksec_audit, radare2_disasm,
// consolidate_hosts: pure local file analysis, no network contact). The
// executor's MCP dispatch path (processMCP) uses this to decide whether a
// gateway scope check applies at all, rather than treating "no target field
// in this schema" the same as "the target field is present but empty" —
// those used to collapse to the same (\"\", false) from Target() alone,
// which made every no-target tool fail every real dispatch with "no target
// found in arguments" (or, if that check were skipped, a follow-on gateway
// scope check against a literal empty-string target, which also fails
// fail-closed) — confirmed nothing ever wired around it. Discovered while
// designing consolidate_hosts, whose file-only shape hits the exact same
// gap.
func NeedsTarget(tool string) bool {
	spec, ok := catalog[tool]
	return ok && spec.TargetField != ""
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
	return renderSaveNote(a, "nmap "+strings.Join(parts, " "))
}

// renderIntField reads a numeric field from a map[string]any tolerant of
// both float64 (the type every JSON number decodes to under `any`, the real
// MCP round-trip's shape) and a native Go int (a literal passed directly in
// tests or other Go-side callers).
func renderIntField(a map[string]any, key string) (int, bool) {
	switch v := a[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func renderWhatweb(a map[string]any) string {
	url, _ := a["url"].(string)
	s := "whatweb"
	if agg, ok := renderIntField(a, "aggression"); ok && agg > 0 {
		s += fmt.Sprintf(" -a %d", agg)
	}
	return renderSaveNote(a, s+" "+url)
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
	if m, _ := a["method"].(string); m != "" {
		parts = append(parts, "-X", m)
	}
	for _, h := range renderHeaderList(a) {
		parts = append(parts, "-H", h)
	}
	if d, _ := a["data"].(string); d != "" {
		parts = append(parts, "-d", d)
	}
	url, _ := a["url"].(string)
	parts = append(parts, url)
	return renderSaveNote(a, "curl -s "+strings.Join(parts, " "))
}

// renderHeaderList reads args["headers"] as a []string regardless of whether
// it arrived as a native []string (Go-side tests, direct calls) or the
// []any a JSON-decoded map produces (the real MCP round-trip) — mirrors the
// same any-vs-typed tolerance every other renderX function already has for
// map[string]any fields.
func renderHeaderList(a map[string]any) []string {
	return renderStringList(a["headers"])
}

// renderStringList reads a map[string]any field tolerant of both a native
// []string (Go-side tests, direct calls) and the []any a JSON-decoded map
// produces (the real MCP round-trip) — shared by any renderX function that
// takes a list argument.
func renderStringList(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func renderGobuster(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	s := fmt.Sprintf("gobuster dir -u %s -w %s", url, wl)
	if ext, _ := a["extensions"].(string); ext != "" {
		s += " -x " + ext
	}
	return renderSaveNote(a, s)
}

func renderFfuf(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	s := fmt.Sprintf("ffuf -u %s -w %s -s", url, wl)
	if mc, _ := a["match_codes"].(string); mc != "" {
		s += " -mc " + mc
	}
	if m, _ := a["method"].(string); m != "" {
		s += " -X " + m
	}
	for _, h := range renderHeaderList(a) {
		s += " -H " + h
	}
	if d, _ := a["data"].(string); d != "" {
		s += " -d " + d
	}
	return renderSaveNote(a, s)
}

func renderNikto(a map[string]any) string {
	host, _ := a["host"].(string)
	return renderSaveNote(a, "nikto -h "+host)
}

// renderSaveNote appends a human-readable note to a rendered command line
// when args carries an output_file — mirrors runCapturedToFile's own
// behavior on the sandbox-mcp side (see sandbox-mcp/capture.go) so the
// approval prompt and audit trail show the full picture, not just the
// underlying tool invocation.
func renderSaveNote(a map[string]any, s string) string {
	if f, _ := a["output_file"].(string); f != "" {
		s += "  [saves output to " + f + "]"
	}
	return s
}

func renderAmass(a map[string]any) string {
	domain, _ := a["domain"].(string)
	s := "amass enum -d " + domain
	if b, _ := a["passive"].(bool); b {
		s += " -passive"
	}
	return renderSaveNote(a, s)
}

func renderSubfinder(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return renderSaveNote(a, "subfinder -d "+domain+" -silent")
}

func renderTheharvester(a map[string]any) string {
	domain, _ := a["domain"].(string)
	source, _ := a["source"].(string)
	if source == "" {
		source = "all"
	}
	return renderSaveNote(a, fmt.Sprintf("theHarvester -d %s -b %s", domain, source))
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
	return renderSaveNote(a, "wpscan --url "+url)
}

func renderNuclei(a map[string]any) string {
	target, _ := a["target"].(string)
	var s string
	if strings.HasPrefix(target, "/") {
		s = "nuclei -l " + target + " -c 15 -rate-limit 50 -bulk-size 50 -timeout 5 -retries 1"
	} else {
		s = "nuclei -u " + target
	}
	if tags, _ := a["tags"].(string); tags != "" {
		s += " -tags " + tags
	}
	if severity, _ := a["severity"].(string); severity != "" {
		s += " -severity " + severity
	}
	return renderSaveNote(a, s+" -silent")
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
	s := "httpx -u " + target + " -silent -sc -server -ip -cl -location -fr"
	if b, _ := a["title"].(bool); b {
		s += " -title"
	}
	if b, _ := a["tech"].(bool); b {
		s += " -td"
	}
	return renderSaveNote(a, s)
}

func renderDnsx(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return renderSaveNote(a, "dnsx -l "+domain+" -silent -a -resp -cname -ns -mx -retry 2")
}

func renderAlterx(a map[string]any) string {
	domain, _ := a["domain"].(string)
	s := "alterx -l " + domain + " -silent"
	if b, _ := a["enrich"].(bool); b {
		s += " -en"
	}
	return renderSaveNote(a, s)
}

func renderGau(a map[string]any) string {
	domain, _ := a["domain"].(string)
	return renderSaveNote(a, "gau --subs --threads 4 --timeout 10 "+domain)
}

func renderKatana(a map[string]any) string {
	url, _ := a["url"].(string)
	depth, ok := renderIntField(a, "depth")
	if !ok || depth <= 0 {
		depth = 2
	}
	return renderSaveNote(a, fmt.Sprintf("katana -u %s -d %d -jc -silent", url, depth))
}

func renderDalfox(a map[string]any) string {
	url, _ := a["url"].(string)
	return renderSaveNote(a, "dalfox url "+url+" --silence")
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

func renderConsolidate(a map[string]any) string {
	domain, _ := a["domain"].(string)
	output, _ := a["output_file"].(string)
	inputs := renderStringList(a["input_files"])
	s := fmt.Sprintf("consolidate_hosts domain=%s inputs=[%s] -> %s", domain, strings.Join(inputs, ", "), output)
	if b, _ := a["high_confidence_only"].(bool); b {
		s += " (high-confidence only)"
	}
	return s
}

func renderPassiveReconBatch(a map[string]any) string {
	domain, _ := a["domain"].(string)
	outputDir, _ := a["output_dir"].(string)
	sources := renderStringList(a["sources"])
	if len(sources) == 0 {
		sources = passiveReconAllSources
	}
	return fmt.Sprintf("passive_recon_batch domain=%s sources=[%s] -> %s/", domain, strings.Join(sources, ", "), outputDir)
}

// passiveReconAllSources mirrors sandbox-mcp's own default source list
// (passive_recon.go's passiveReconSources) purely for display purposes when
// the caller omitted "sources" — the two are independent copies since this
// package never imports sandbox-mcp's main package.
var passiveReconAllSources = []string{"subfinder", "amass", "theharvester", "gau"}

func renderArjun(a map[string]any) string {
	url, _ := a["url"].(string)
	cmd := "arjun -u " + url
	if m, _ := a["method"].(string); m != "" {
		cmd += " -m " + m
	}
	if w, _ := a["wordlist"].(string); w != "" {
		cmd += " -w " + w
	}
	return renderSaveNote(a, cmd)
}

func renderNaabu(a map[string]any) string {
	target, _ := a["target"].(string)
	cmd := "naabu -host " + target + " -silent"
	if p, _ := a["ports"].(string); p != "" {
		cmd += " -p " + p
	}
	return renderSaveNote(a, cmd)
}

func renderTlsx(a map[string]any) string {
	target, _ := a["target"].(string)
	cmd := "tlsx -u " + target + " -silent"
	if b, _ := a["san"].(bool); b {
		cmd += " -san"
	}
	if b, _ := a["jarm"].(bool); b {
		cmd += " -jarm"
	}
	return renderSaveNote(a, cmd)
}

func renderCloudEnum(a map[string]any) string {
	kw, _ := a["keyword"].(string)
	cmd := "cloud_enum -k " + kw
	if b, _ := a["quick_scan"].(bool); b {
		cmd += " -q"
	}
	return renderSaveNote(a, cmd)
}

func renderJwtTool(a map[string]any) string {
	tok, _ := a["token"].(string)
	cmd := "jwt_tool " + tok
	if m, _ := a["mode"].(string); m != "" {
		cmd += " -M " + m
	}
	return renderSaveNote(a, cmd)
}

func renderSubzy(a map[string]any) string {
	target, _ := a["target"].(string)
	return renderSaveNote(a, "subzy run --target "+target)
}

func renderJsluice(a map[string]any) string {
	url, _ := a["url"].(string)
	mode, _ := a["mode"].(string)
	if mode != "secrets" {
		mode = "urls"
	}
	return renderSaveNote(a, "jsluice "+mode+" "+url)
}

func renderGraphqlCop(a map[string]any) string {
	url, _ := a["url"].(string)
	return renderSaveNote(a, "graphql-cop -t "+url)
}

func renderCorsy(a map[string]any) string {
	url, _ := a["url"].(string)
	return renderSaveNote(a, "corsy -u "+url)
}

func renderScreenshotTriage(a map[string]any) string {
	target, _ := a["target"].(string)
	outDir, _ := a["output_dir"].(string)
	s := "httpx -l " + target + " -ss -system-chrome -sc -title -td -server"
	if outDir != "" {
		s += "  [screenshots saved to " + outDir + "]"
	}
	return s
}
