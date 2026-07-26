package api

import (
	"net"
	"testing"
)

// Regression: Docker socket hardening (docker-compose.yml's docker-proxy
// service) routes the orchestrator's Docker access through DOCKER_HOST=
// tcp://docker-proxy:2375 instead of a mounted /var/run/docker.sock — the
// orchestrator container has no such socket file anymore. checkDockerDaemon
// still only ever dialed a literal unix socket path, so in the hardened
// deployment it always reported the daemon "down" even though Docker access
// works fine through the proxy. When DOCKER_HOST is set to a tcp:// address,
// the check must dial that instead of the (now-nonexistent) socket path.
func TestCheckDockerDaemonUsesDockerHostWhenSet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	t.Setenv("DOCKER_HOST", "tcp://"+ln.Addr().String())

	mod := checkDockerDaemon("/nonexistent/stale-socket-path.sock") // proves the check used DOCKER_HOST, not this path
	if mod.Status != "healthy" {
		t.Fatalf("expected healthy via DOCKER_HOST proxy, got status=%q details=%q", mod.Status, mod.Details)
	}
}

func TestCheckDockerDaemonReportsDownWhenDockerHostUnreachable(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1") // nothing listens on port 1

	mod := checkDockerDaemon("/nonexistent/stale-socket-path.sock")
	if mod.Status != "down" {
		t.Fatalf("expected down for unreachable DOCKER_HOST, got status=%q details=%q", mod.Status, mod.Details)
	}
}

// Existing behavior (no DOCKER_HOST) must be unchanged: fall back to the
// literal socket path.
func TestCheckDockerDaemonFallsBackToSocketPathWithoutDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	mod := checkDockerDaemon("")
	if mod.Status != "not_configured" {
		t.Fatalf("expected not_configured for empty socket path, got status=%q", mod.Status)
	}
}
