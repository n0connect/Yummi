package eino

import "testing"

func TestRunTrace_RecordAndSnapshot(t *testing.T) {
	tr := &runTrace{}
	tr.record("nmap -sV 10.0.0.5", "Nmap done: 1 host up")
	tr.record("nmap -p- 10.0.0.6", "Nmap done: 2 hosts up")
	tr.record("gobuster dir -u http://x -w common.txt", "found /admin")

	obs, tools := tr.snapshot()
	if len(obs) != 3 {
		t.Fatalf("observations = %v, want 3", obs)
	}
	// Distinct tool names only, keyed by first token of the recorded command.
	if len(tools) != 2 || tools[0] != "nmap" || tools[1] != "gobuster" {
		t.Fatalf("tools = %v, want [nmap gobuster]", tools)
	}
}

func TestRunTrace_NilSafe(t *testing.T) {
	var tr *runTrace
	tr.record("nmap 10.0.0.5", "out") // must not panic
	obs, tools := tr.snapshot()
	if obs != nil || tools != nil {
		t.Fatalf("nil trace snapshot should be nil, got obs=%v tools=%v", obs, tools)
	}
}

func TestFinishCapture_SetAndGet(t *testing.T) {
	fc := &finishCapture{}
	if _, _, called := fc.get(); called {
		t.Fatal("fresh capture should report not called")
	}
	fc.set("done: found nothing", []string{"port 22 open"})
	summary, findings, called := fc.get()
	if !called || summary != "done: found nothing" || len(findings) != 1 || findings[0] != "port 22 open" {
		t.Fatalf("unexpected capture state: summary=%q findings=%v called=%v", summary, findings, called)
	}
}

func TestAskCapture_SetAndGet(t *testing.T) {
	ac := &askCapture{}
	if _, called := ac.get(); called {
		t.Fatal("fresh capture should report not called")
	}
	ac.set("which credentials should I try?")
	question, called := ac.get()
	if !called || question != "which credentials should I try?" {
		t.Fatalf("unexpected capture state: question=%q called=%v", question, called)
	}
}
