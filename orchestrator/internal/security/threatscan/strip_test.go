package threatscan

import "testing"

func TestStripANSIColorsAndCursor(t *testing.T) {
	// SGR color codes + cursor move, as nmap/gobuster emit.
	in := "\x1b[1;32mPORT\x1b[0m   STATE\x1b[2K\x1b[1G open"
	got := StripANSI(in)
	want := "PORT   STATE open"
	if got != want {
		t.Fatalf("StripANSI()=%q want %q", got, want)
	}
}

func TestStripANSIOscTitle(t *testing.T) {
	// OSC window-title set, BEL-terminated.
	in := "\x1b]0;malicious title\x07real output"
	if got := StripANSI(in); got != "real output" {
		t.Fatalf("OSC not stripped: %q", got)
	}
	// OSC ST-terminated (ESC \).
	in2 := "\x1b]2;t\x1b\\after"
	if got := StripANSI(in2); got != "after" {
		t.Fatalf("ST-terminated OSC not stripped: %q", got)
	}
}

func TestStripANSICleanFastPath(t *testing.T) {
	clean := "22/tcp open ssh\n80/tcp open http"
	if got := StripANSI(clean); got != clean {
		t.Fatalf("clean text must pass through unchanged, got %q", got)
	}
	if StripANSI("") != "" {
		t.Fatal("empty input must return empty")
	}
}

func TestStripANSILoneC1(t *testing.T) {
	// A UTF-8-encoded 8-bit C1 control (U+009B = CSI) should be removed. Note:
	// a *raw* invalid 0x9B byte cannot be safely touched (it decodes to U+FFFD
	// and stripping arbitrary 0x80-0x9F bytes would corrupt valid multibyte
	// UTF-8) — modern tools emit the 7-bit ESC forms anyway.
	in := "a" + string(rune(0x9b)) + "31mb"
	if got := StripANSI(in); got != "ab" {
		t.Fatalf("8-bit CSI not stripped: %q", got)
	}
}

// StripANSI must not disturb legitimate content that merely contains brackets or
// percent signs (no ESC byte → fast path, untouched).
func TestStripANSIPreservesBenignBrackets(t *testing.T) {
	in := "found [admin] at /admin?x=1 (100%)"
	if got := StripANSI(in); got != in {
		t.Fatalf("benign brackets altered: %q", got)
	}
}
