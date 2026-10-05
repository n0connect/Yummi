// Package version exposes build metadata for the yummi binary.
package version

// Build-time variables populated with -ldflags.
var (
	Semver    = "v1.0.1b"
	GitSHA    = "dev"
	BuildDate = "unknown"
)

// Info contains runtime-visible build metadata.
type Info struct {
	Semver    string `json:"semver"`
	GitSHA    string `json:"git_sha"`
	BuildDate string `json:"build_date"`
}

// Current returns the active build metadata.
func Current() Info {
	return Info{
		Semver:    Semver,
		GitSHA:    GitSHA,
		BuildDate: BuildDate,
	}
}
