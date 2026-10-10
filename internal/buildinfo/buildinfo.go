// Package buildinfo carries build metadata injected at link time:
//
//	go build -ldflags "-X github.com/prettyleaf/remnanode-manager/internal/buildinfo.Version=1.2.3 ..."
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"time"
)

var (
	Version    = "dev"
	Commit     = ""
	Branch     = ""
	BuildTime  = ""
	Repository = "prettyleaf/remnanode-manager"
)

var startedAt = time.Now()

type Info struct {
	Version    string    `json:"version"`
	Commit     string    `json:"commit"`
	Branch     string    `json:"branch"`
	BuildTime  string    `json:"buildTime"`
	Repository string    `json:"repository"`
	GoVersion  string    `json:"goVersion"`
	Platform   string    `json:"platform"`
	StartedAt  time.Time `json:"startedAt"`
}

// Get returns the build metadata, falling back to the VCS stamp Go embeds
// for local `go build` runs without ldflags.
func Get() Info {
	info := Info{
		Version:    Version,
		Commit:     Commit,
		Branch:     Branch,
		BuildTime:  BuildTime,
		Repository: Repository,
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:  startedAt,
	}
	if info.Commit == "" || info.BuildTime == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if info.Commit == "" {
						info.Commit = s.Value
					}
				case "vcs.time":
					if info.BuildTime == "" {
						info.BuildTime = s.Value
					}
				}
			}
		}
	}
	return info
}
