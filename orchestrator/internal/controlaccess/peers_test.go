package controlaccess

import "testing"

func TestExactControlPeers(t *testing.T) {
	p, err := ParsePeers("127.0.0.1, ::1/128, 172.21.0.4/32")
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range []string{"127.0.0.1:1", "[::1]:80", "[::ffff:127.0.0.1]:80", "172.21.0.4:12000"} {
		if !p.Allows(peer) {
			t.Errorf("trusted socket peer refused: %s", peer)
		}
	}
	for _, peer := range []string{"172.20.0.10:12000", "172.21.0.3:12000", "localhost:80", "127.0.0.1", "[::1%lo]:80"} {
		if p.Allows(peer) {
			t.Errorf("untrusted socket peer accepted: %s", peer)
		}
	}
	for _, raw := range []string{"", "127.0.0.1,", "0.0.0.0/0", "127.0.0.0/8", "::/0", "0.0.0.0", "::", "localhost", "::1%lo"} {
		if _, err := ParsePeers(raw); err == nil {
			t.Errorf("unsafe policy accepted: %q", raw)
		}
	}
}
