package core

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/anyproto/anytype-cli/core/config"
)

// Set via ldflags during build
var (
	Version   = ""
	Commit    = ""
	BuildTime = ""
	GitState  = ""
)

func GetVersionVerbose() string {
	commit := Commit
	if commit == "" {
		commit = "unknown"
	}
	buildTime := BuildTime
	if buildTime == "" {
		buildTime = "unknown"
	}
	return fmt.Sprintf("anytype-cli %s\nCommit: %s\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\nHeart: %s\nURL: %s",
		GetVersion(), commit, buildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH, GetHeartVersion(), GetReleaseURL())
}

func GetVersionBrief() string {
	return fmt.Sprintf("anytype-cli %s (%s)\n%s", GetVersion(), BuildTime, GetReleaseURL())
}

func GetVersion() string {
	version := Version
	if version == "" {
		version = "0.0.1-dev"
	}
	if GitState == "dirty" {
		version += "-dirty"
	}
	return version
}

func GetReleaseURL() string {
	if Version == "" || Commit == "" || strings.Contains(Version, "-") {
		if Commit != "" {
			return config.GitHubCommitURL + Commit
		}
		return "https://github.com/anyproto/anytype-cli"
	}
	return config.GitHubReleaseURL + Version
}

func GetHeartVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	for _, dep := range info.Deps {
		if dep.Path == "github.com/anyproto/anytype-heart" {
			return dep.Version
		}
	}
	return "unknown"
}
