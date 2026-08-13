package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "yummi-sandbox", Version: "v1"}, nil)
	registerTools(server)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Println("yummi-sandbox-mcp exited:", err)
		os.Exit(1)
	}
}
