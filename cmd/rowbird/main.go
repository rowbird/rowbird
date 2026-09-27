// Command rowbird is the Rowbird server and administration CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // IANA time zones for report and user time zones, also in minimal containers

	"github.com/rowbird/rowbird/internal/cli"
	"github.com/rowbird/rowbird/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, SPA: web.Handler()})
	stop()
	os.Exit(code)
}
