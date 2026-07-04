package version

import (
	"fmt"
	"runtime"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func String() string {
	return fmt.Sprintf("cloud-clipboard %s (commit %s, built %s, %s/%s)", Version, Commit, BuildTime, runtime.GOOS, runtime.GOARCH)
}

func Short() string {
	return Version
}