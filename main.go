package main

import (
	"fmt"
	"runtime"

	"github.com/semaphoreio/sem-ai/cmd"
	"github.com/semaphoreio/sem-ai/pkg/client"
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
	client.UserAgent = fmt.Sprintf("sem-ai/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
	cmd.Execute()
}
