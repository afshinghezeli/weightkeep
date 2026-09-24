package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/afshinghezeli/weightkeep/internal/config"
	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/version"
)

// app is everything a command that touches the store needs.
type app struct {
	cfg    *config.Config
	store  *store.Store
	hub    *hub.Client
	keeper *keep.Keeper
}

func (d deps) openApp(ctx context.Context) (*app, error) {
	cfg, err := d.loadConfig()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.Home)
	if err != nil {
		return nil, fmt.Errorf("open store at %s: %w", cfg.Home, err)
	}
	client, err := hub.New(cfg.Upstream, hub.Options{
		Token:     cfg.Token,
		UserAgent: "weightkeep/" + version.Get().Version,
	})
	if err != nil {
		st.Close()
		return nil, err
	}
	var mirrors []*hub.Client
	for _, m := range cfg.Mirrors {
		mc, err := hub.New(m, hub.Options{UserAgent: "weightkeep/" + version.Get().Version})
		if err != nil {
			st.Close()
			return nil, err
		}
		mirrors = append(mirrors, mc)
	}
	f := &fetch.Fetcher{Store: st, Hub: client}
	return &app{
		cfg:    cfg,
		store:  st,
		hub:    client,
		keeper: &keep.Keeper{Store: st, Hub: client, Fetcher: f, Mirrors: mirrors},
	}, nil
}

func (a *app) Close() error { return a.store.Close() }

// explain adds what to do next to errors users can act on.
func explain(err error, repo string) error {
	switch {
	case errors.Is(err, hub.ErrGated):
		return fmt.Errorf("%w\n%s is gated: accept its terms on the Hub, then set HF_TOKEN (or run `hf auth login`). weightkeep keeps a private copy but never shares gated repos", err, repo)
	case errors.Is(err, hub.ErrRepoNotFound):
		return fmt.Errorf("%w\ncheck the spelling of %s; private repos also need HF_TOKEN", err, repo)
	case errors.Is(err, hub.ErrUnauthorized):
		return fmt.Errorf("%w\nthe token in use was rejected; check `weightkeep env` for where it comes from", err)
	case errors.Is(err, hub.ErrDisabled):
		return fmt.Errorf("%w\nthe Hub no longer serves %s", err, repo)
	case hub.Retryable(err):
		return fmt.Errorf("%w\nthe Hub can't be reached. Kept revisions are still served by `weightkeep serve`; to get this one from another weightkeep node, use `weightkeep pull --torrent <magnet> --peer HOST:PORT`", err)
	}
	return err
}
