// Package main is the entry point for the Ultra CLI.
//
//	@title			Ultra API
//	@version		1.0
//	@description	Ultra is a terminal-based AI coding assistant. This API is served over a Unix socket (or Windows named pipe) and provides programmatic access to workspaces, sessions, agents, LSP, MCP, and more.
//	@contact.name	Charm
//	@contact.url	https://charm.sh
//	@license.name	MIT
//	@license.url	https://github.com/asx8678/ultra/blob/main/LICENSE
//	@BasePath		/v1
package main

import (
	"github.com/asx8678/ultra/internal/cmd"
	_ "github.com/asx8678/ultra/internal/dns"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	startProfiler()
	cmd.Execute()
}
