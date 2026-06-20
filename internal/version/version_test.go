package version

import "testing"

func TestCurrent(t *testing.T) {
	info := Current()
	if info.Semver == "" {
		t.Fatal("semver should not be empty")
	}
}
