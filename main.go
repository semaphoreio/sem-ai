package main

import (
	"fmt"
	"runtime"

	"github.com/semaphoreio/agent-cli/cmd"
	"github.com/semaphoreio/agent-cli/pkg/client"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.Version = version
	cmd.Commit = commit
	cmd.Date = date
	client.UserAgent = fmt.Sprintf("sem-agent/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
	cmd.Execute()
}
