// docker-proxy is the fixed-container, non-root Docker execution broker.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/dockerproxy"
)

func setting(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
func main() {
	broker, err := dockerproxy.New(dockerproxy.Config{
		Socket:    setting("YUMMI_DOCKER_SOCKET", "/var/run/docker.sock"),
		Container: setting("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"),
		Peers:     strings.Split(setting("YUMMI_DOCKER_PROXY_PEERS", "172.21.0.3/32,127.0.0.1/32,::1/128"), ","),
	})
	if err != nil {
		log.Fatal(err)
	}
	socketPath := setting("YUMMI_DOCKER_SOCKET", "/var/run/docker.sock")
	info, err := os.Stat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		log.Fatal("YUMMI_DOCKER_SOCKET must identify an existing Unix socket")
	}
	addr := setting("YUMMI_DOCKER_PROXY_ADDR", ":2375")
	if addr == "" {
		log.Fatal("YUMMI_DOCKER_PROXY_ADDR must not be empty")
	}
	server := &http.Server{Addr: addr, Handler: broker, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	log.Fatal(server.ListenAndServe())
}
