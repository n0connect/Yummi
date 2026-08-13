package main

import (
	"context"
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

// registerTools wires every cataloged tool onto server. Split out from
// main() so tests can build a server without also starting a stdio loop.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "nmap_scan",
		Description: "Scan a target with nmap for open ports and services.",
	}, nmapHandler)
}
