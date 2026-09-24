package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/proxy"
)

func newServeCommand(d deps) *cobra.Command {
	var (
		addr     string
		offline  bool
		logLevel string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve kept models through a Hugging Face Hub-compatible endpoint",
		Long: `serve answers the Hugging Face Hub API on a local address. Point clients at it
and they load models from the store:

  export HF_ENDPOINT=http://127.0.0.1:8700     # transformers, vLLM, hf CLI
  export MODEL_ENDPOINT=http://127.0.0.1:8700  # llama.cpp -hf

Files that aren't kept yet are fetched from the upstream Hub on first
request, verified, kept, and streamed to the client as they arrive. If the
Hub is unreachable, kept revisions are still served. --offline never
contacts the Hub.

For huggingface_hub, also set HF_HUB_DISABLE_XET=1 if the client ever talked
to huggingface.co directly before; see docs/clients.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := d.openApp(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			if addr == "" {
				addr = a.cfg.ServeAddr
			}
			var level slog.Level
			if err := level.UnmarshalText([]byte(logLevel)); err != nil {
				return fmt.Errorf("--log-level: %w", err)
			}
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level}))

			ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
				// Other machines can reach this server: keep denylisted
				// revisions from them.
				if err := a.useDenylist(cmd, !offline, false); err != nil {
					return err
				}
			}
			srv := &http.Server{
				Handler:           proxy.New(a.keeper, proxy.Options{Offline: offline, Log: log}),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       2 * time.Minute,
				// No WriteTimeout: model files take as long as they take.
			}
			url := "http://" + ln.Addr().String()
			mode := "upstream " + a.cfg.Upstream
			if offline {
				mode = "offline"
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "serving %s on %s (%s)\n  export HF_ENDPOINT=%s\n", a.cfg.Home, url, mode, url)
			if !isLoopback(ln.Addr()) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: listening beyond localhost; anyone who can reach %s can read every kept model, including gated ones\n", url)
			}

			errc := make(chan error, 1)
			go func() { errc <- srv.Serve(ln) }()
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "listen address (default 127.0.0.1:8700, or serve.addr in the config)")
	cmd.Flags().BoolVar(&offline, "offline", false, "never contact the upstream Hub")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "debug, info, warn or error")
	return cmd
}

func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}
