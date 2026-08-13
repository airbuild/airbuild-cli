package main

import "github.com/airbuild/cli/cmd"

// version is set at build time via -ldflags "-X main.version=..."
// Defaults to "dev" when built without ldflags (e.g. go run, go build).
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
