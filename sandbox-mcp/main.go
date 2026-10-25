package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := requireSandboxRuntime(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "yummi-sandbox", Version: "v1"}, nil)
	defer func() {
		globalBrowser.mu.Lock()
		driver := globalBrowser.driver
		globalBrowser.driver = nil
		globalBrowser.mu.Unlock()
		if driver != nil {
			_ = driver.Close()
		}
	}()
	defer func() {
		globalBurp.mu.Lock()
		session := globalBurp.client
		globalBurp.client = nil
		globalBurp.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
	}()
	defer func() {
		globalRea.mu.Lock()
		session := globalRea.client
		globalRea.client = nil
		globalRea.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
	}()
	registerTools(server)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Println("yummi-sandbox-mcp exited:", err)
		os.Exit(1)
	}
}
