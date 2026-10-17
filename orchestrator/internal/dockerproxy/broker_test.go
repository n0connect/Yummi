package dockerproxy

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExecCreateRejectsPrivilegeAndAmbiguousFields(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"User":"root","Cmd":["id"]}`, `{"User":"1000","Cmd":["id"]}`,
		`{"User":"1000:1000","Privileged":true,"Cmd":["id"]}`,
		`{"User":"1000:1000","Detach":true,"Cmd":["id"]}`,
		`{"User":"1000:1000","Detach":"false","Cmd":["id"]}`,
		`{"User":"1000:1000","Cmd":["id"],"HostConfig":{"Privileged":true}}`,
		`{"User":"1000:1000","User":"root","Cmd":["id"]}`,
		`{"User":"1000:1000","user":"root","Cmd":["id"]}`,
		`{"User":"1000:1000","Cmd":[]}`, `{"User":"1000:1000","Cmd":["id"]} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			if _, err := validateCreate([]byte(body)); err == nil {
				t.Fatal("unsafe create accepted")
			}
		})
	}
	valid := `{"Detach":false,"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"ConsoleSize":[24,80],"DetachKeys":"","Env":["TERM=xterm"],"Cmd":["/bin/sh","-c","id"],"Tty":true,"WorkingDir":"/tmp","User":"1000:1000","Privileged":false}`
	if _, err := validateCreate([]byte(valid)); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerDeniesOtherContainersAndDaemonMutations(t *testing.T) {
	b, err := New(Config{Socket: "/var/run/docker.sock", Container: "yummi-sandbox", Peers: []string{"172.21.0.3/32"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body, peer string }{
		{"POST", "/v1.45/containers/create", `{}`, "172.21.0.3:3000"},
		{"POST", "/containers/yummi-sandbox/start", `{}`, "172.21.0.3:3000"},
		{"POST", "/containers/yummi-gateway/exec", `{"User":"1000:1000","Cmd":["id"]}`, "172.21.0.3:3000"},
		{"POST", "/exec/unissued/start", `{"Detach":false,"Tty":false}`, "172.21.0.3:3000"},
		{"GET", "/containers/json", "", "172.21.0.3:3000"},
		{"GET", "/images/json", "", "172.21.0.3:3000"},
		{"GET", "/_ping", "", "172.20.0.10:3000"},
		{"GET", "/_ping", "slow-body", "172.21.0.3:3000"},
		{"GET", "/v1.45/containers/yummi-sandbox%2f..%2fyummi-gateway/json", "", "172.21.0.3:3000"},
		{"GET", "/containers/yummi-sandbox/json?unexpected=true", "", "172.21.0.3:3000"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "172.21.0.3")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s %s peer %s: status=%d body=%s", tc.method, tc.path, tc.peer, w.Code, w.Body.String())
		}
	}
}

func TestBrokerRejectsUnsafeStartupConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{Socket: "", Container: "yummi-sandbox", Peers: []string{"172.21.0.3/32"}},
		{Socket: "tcp://docker:2375", Container: "yummi-sandbox", Peers: []string{"172.21.0.3/32"}},
		{Socket: "/var/run/docker.sock", Container: "", Peers: []string{"172.21.0.3/32"}},
		{Socket: "/var/run/docker.sock", Container: "../gateway", Peers: []string{"172.21.0.3/32"}},
		{Socket: "/var/run/docker.sock", Container: "yummi-sandbox", Peers: nil},
		{Socket: "/var/run/docker.sock", Container: "yummi-sandbox", Peers: []string{"0.0.0.0/0"}},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("unsafe config accepted: %+v", cfg)
		}
	}
}

func TestStrictJSONRejectsDuplicateFieldsAndOversizedCommands(t *testing.T) {
	if _, err := validateCreate([]byte(`{"User":"1000:1000","Cmd":["id"],"Privileged":false,"Privileged":true}`)); err == nil {
		t.Fatal("duplicate field accepted")
	}
	b := append([]byte(`{"User":"1000:1000","Cmd":["`), bytes.Repeat([]byte("x"), maxBody+1)...)
	b = append(b, []byte(`"]}`)...)
	if _, err := validateCreate(b); err == nil {
		t.Fatal("oversized create accepted")
	}
}

func TestRegistryReclaimsCompletedExecsButKeepsActiveStreams(t *testing.T) {
	b, err := New(Config{Socket: "/var/run/docker.sock", Container: "yummi-sandbox", Peers: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	b.issued[id] = issuedExec{expires: time.Now().Add(24 * time.Hour)}
	b.markCompleted(id)
	completed, ok := b.lookup(id)
	if !ok || !completed.completed || time.Until(completed.expires) > 2*time.Minute {
		t.Fatal("completed exec retained for a full day")
	}
	// Registry expiry uses the real clock; only fixture timestamps are past-dated.
	completed.expires = time.Now().Add(-time.Second)
	b.issued[id] = completed
	if _, ok := b.lookup(id); ok {
		t.Fatal("expired completed exec not reclaimed")
	}
	b.issued[id] = issuedExec{expires: time.Now().Add(-time.Second), active: 1, tty: true}
	if _, ok := b.lookup(id); !ok {
		t.Fatal("long-lived active TTY lost resize/inspect authorization")
	}
	b.unpin(id)
	if _, ok := b.lookup(id); !ok {
		t.Fatal("finished stream lost CLI exit-inspect grace")
	}
}
