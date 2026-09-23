// Command weightkeep keeps verified copies of open-weight models and serves
// them through a local Hugging Face Hub-compatible endpoint.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/afshinghezeli/weightkeep/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, cli.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, os.Args[1:])
	stop()
	os.Exit(code)
}
