package dockerproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Opt in with a real local Docker Unix socket. The sole execution target is
// a new disposable, networkless container; the live Yummi stack is untouched.
func TestRealDockerBrokerCLIStreamingAndTTY(t *testing.T) {
	socket := os.Getenv("YUMMI_DOCKER_PROXY_TEST_SOCKET")
	if socket == "" {
		t.Skip("set YUMMI_DOCKER_PROXY_TEST_SOCKET to test a real Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name := fmt.Sprintf("yummi-broker-test-%d", time.Now().UnixNano())
	direct := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = append(os.Environ(), "DOCKER_HOST=unix://"+socket)
		return cmd
	}
	out, err := direct("run", "-d", "--rm", "--network=none", "--cap-drop=ALL", "--user", "1000:1000", "--name", name, "--entrypoint", "/bin/sleep", "yummi/sandbox:local", "infinity").CombinedOutput()
	if err != nil {
		t.Fatalf("create disposable sandbox: %v %s", err, out)
	}
	t.Cleanup(func() {
		cmd := exec.Command("docker", "rm", "-f", name)
		cmd.Env = append(os.Environ(), "DOCKER_HOST=unix://"+socket)
		_ = cmd.Run()
	})
	broker, err := New(Config{Socket: socket, Container: name, Peers: []string{"127.0.0.1/32", "::1/128"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	proxied := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = append(os.Environ(), "DOCKER_HOST=tcp://"+strings.TrimPrefix(server.URL, "http://"))
		return cmd
	}
	// This uses Docker's real API negotiation, exec-create/start upgrade,
	// stdin streaming, exit inspection, and custom environment fields.
	cmd := proxied("exec", "-i", "--user", "1000:1000", "--env", "BROKER_PROBE=permitted", name, "/bin/sh", "-c", `read line; printf '%s:%s:' "$line" "$BROKER_PROBE"; id -u; sleep 0.1; printf 'second chunk'`)
	cmd.Stdin = strings.NewReader("real-input\n")
	out, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "real-input:permitted:1000") || !strings.Contains(string(out), "second chunk") {
		t.Fatalf("real CLI exec streaming: %v %s", err, out)
	}
	// Repeated health-like CLI execs must enter the short completion grace,
	// rather than consuming a full day's registry capacity.
	for i := 0; i < 16; i++ {
		if out, err := proxied("exec", "--user", "1000:1000", name, "/bin/true").CombinedOutput(); err != nil {
			t.Fatalf("health exec %d: %v %s", i, err, out)
		}
	}
	broker.mu.Lock()
	for id, item := range broker.issued {
		if !item.completed || time.Until(item.expires) > 2*time.Minute {
			broker.mu.Unlock()
			t.Fatalf("completed CLI exec %s retained as active for a day", id)
		}
	}
	broker.mu.Unlock()
	// Real TCP short reads and slow request bodies fail before any daemon exec.
	for _, slow := range []bool{false, true} {
		connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.SetDeadline(time.Now().Add(7 * time.Second))
		_, _ = fmt.Fprintf(connection, "POST /containers/%s/exec HTTP/1.1\r\nHost: docker\r\nContent-Length: 200\r\nConnection: close\r\n\r\n{", name)
		if !slow {
			_ = connection.(*net.TCPConn).CloseWrite()
		}
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil {
			connection.Close()
			t.Fatalf("short/slow body response: %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		connection.Close()
		if response.StatusCode != 403 {
			t.Fatalf("short/slow request reached daemon: %d", response.StatusCode)
		}
	}
	// Keep stdin open until the process has emitted its first observation.
	cmd = proxied("exec", "-i", "--user", "1000:1000", name, "/bin/sh", "-c", `printf 'ready\n'; read line; printf 'received:%s' "$line"`)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("initial streaming output: %q %v", line, err)
	}
	_, _ = io.WriteString(input, "after-ready\n")
	_ = input.Close()
	rest, err := io.ReadAll(reader)
	wait := cmd.Wait()
	if err != nil || wait != nil || string(rest) != "received:after-ready" {
		t.Fatalf("bidirectional streaming: %s %v %v", rest, err, wait)
	}
	// PTY exercises the same Docker CLI -it handshake as the operator terminal.
	cmd = proxied("exec", "-it", "--user", "1000:1000", name, "/bin/sh", "-c", "test -t 0 && printf 'real-tty:' && id -u")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	terminalOutput, readErr := io.ReadAll(terminal)
	_ = terminal.Close()
	waitErr := cmd.Wait()
	if waitErr != nil || !bytes.Contains(terminalOutput, []byte("real-tty:1000")) {
		t.Fatalf("real TTY exec: wait=%v read=%v out=%s", waitErr, readErr, terminalOutput)
	}
	// Exercise issued-ID start, resize and inspect with real daemon state.
	call := func(method, path, body string) (int, []byte) {
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	status, data := call("POST", "/containers/"+name+"/exec", `{"User":"1000:1000","Tty":true,"Cmd":["/bin/sleep","10"]}`)
	var issued struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(data, &issued)
	if status != 201 || issued.ID == "" {
		t.Fatalf("create resize target: %d %s", status, data)
	}
	status, data = call("POST", "/exec/"+issued.ID+"/start", `{"Detach":true,"Tty":true}`)
	if status != 200 {
		t.Fatalf("detached start: %d %s", status, data)
	}
	status, data = call("POST", "/exec/"+issued.ID+"/resize?h=24&w=80", "")
	if status != 200 {
		t.Fatalf("resize issued exec: %d %s", status, data)
	}
	status, data = call("GET", "/exec/"+issued.ID+"/json", "")
	var inspected struct {
		Running       bool
		ProcessConfig struct {
			User string `json:"user"`
		}
	}
	_ = json.Unmarshal(data, &inspected)
	if status != 200 || !inspected.Running || inspected.ProcessConfig.User != "1000:1000" {
		t.Fatalf("inspect issued exec: %d %s", status, data)
	}
	// A real daemon would accept these actions; the broker rejects them before
	// reaching it. Only harmless disposable-target operations are attempted.
	requests := []struct{ method, path, body string }{
		{"POST", "/containers/" + name + "/stop", ""},
		{"POST", "/containers/" + name + "/exec", `{"User":"root","Cmd":["id"]}`},
		{"POST", "/containers/" + name + "/exec", `{"User":"1000:1000","Privileged":true,"Cmd":["id"]}`},
		{"POST", "/containers/create", `{"Image":"yummi/sandbox:local"}`},
		{"POST", "/exec/" + strings.Repeat("a", 64) + "/start", `{"Detach":false,"Tty":false}`},
		{"GET", "/containers/yummi-gateway/json", ""},
	}
	for _, tc := range requests {
		req, _ := http.NewRequestWithContext(ctx, tc.method, server.URL+tc.path, strings.NewReader(tc.body))
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("denial %s %s: %d %s", tc.method, tc.path, response.StatusCode, body)
		}
	}
	if out, err := direct("inspect", "--format", "{{.State.Running}}", name).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("denied stop reached daemon: %v %s", err, out)
	}
}
