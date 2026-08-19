package version

// Info describes the build that produced the running executable.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

var Version = "0.0.0-dev"
var Commit = "unknown"
var BuildDate = "unknown"

// Current returns the linker-provided build metadata.
func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}
