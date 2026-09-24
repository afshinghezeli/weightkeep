// Package config resolves where weightkeep keeps its data, which Hub it
// talks to, and which Hugging Face token to use.
//
// Precedence, highest first: environment variables, the config file,
// built-in defaults. Hugging Face paths and the token are found the same way
// huggingface_hub finds them, so weightkeep sees what the hf CLI sees.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultUpstream  = "https://huggingface.co"
	DefaultServeAddr = "127.0.0.1:8700"
)

// Config is the resolved configuration.
type Config struct {
	// Home is the data directory: blob store, manifests, database.
	Home string
	// ConfigFile is the config file that was read, or would be read if it existed.
	ConfigFile string
	// Upstream is the Hub weightkeep fetches from, without a trailing slash.
	Upstream string
	// Mirrors are tried in order when the upstream can't be reached or no
	// longer has a repo: other weightkeep nodes, or public Hub mirrors.
	Mirrors []string
	// ServeAddr is the default listen address for `weightkeep serve`.
	ServeAddr string

	// HFHome, HFHubCache and HFTokenPath follow huggingface_hub's rules.
	HFHome      string
	HFHubCache  string
	HFTokenPath string

	// Token is the Hugging Face token, if any. TokenSource names where it
	// came from: an environment variable name or a file path.
	Token       Token
	TokenSource string
}

// Env is the part of the process environment Load looks at.
type Env struct {
	HomeDir string
	Getenv  func(string) string
}

// OSEnv returns the real environment.
func OSEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("find home directory: %w", err)
	}
	return Env{HomeDir: home, Getenv: os.Getenv}, nil
}

// fileConfig is the on-disk format. Unknown keys are an error.
type fileConfig struct {
	Home     string   `toml:"home"`
	Upstream string   `toml:"upstream"`
	Mirrors  []string `toml:"mirrors"`
	Serve    struct {
		Addr string `toml:"addr"`
	} `toml:"serve"`
}

// Load resolves the configuration.
func Load(env Env) (*Config, error) {
	c := &Config{}

	fc, path, err := readConfigFile(env)
	if err != nil {
		return nil, err
	}
	c.ConfigFile = path

	if c.Home, err = resolveHome(env, fc); err != nil {
		return nil, err
	}

	// HF_ENDPOINT is deliberately not read: users point it at `weightkeep
	// serve`, and using it as our upstream would make the proxy call itself.
	upstream := firstNonEmpty(env.Getenv("WEIGHTKEEP_UPSTREAM"), fc.Upstream, DefaultUpstream)
	if c.Upstream, err = normalizeUpstream(upstream); err != nil {
		return nil, err
	}

	mirrors := fc.Mirrors
	if v := env.Getenv("WEIGHTKEEP_MIRRORS"); v != "" {
		mirrors = strings.Split(v, ",")
	}
	for _, m := range mirrors {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		u, err := normalizeUpstream(m)
		if err != nil {
			return nil, fmt.Errorf("mirror: %w", err)
		}
		c.Mirrors = append(c.Mirrors, u)
	}

	c.ServeAddr = firstNonEmpty(env.Getenv("WEIGHTKEEP_ADDR"), fc.Serve.Addr, DefaultServeAddr)

	resolveHF(env, c)
	if err := resolveToken(env, c); err != nil {
		return nil, err
	}
	return c, nil
}

func readConfigFile(env Env) (fileConfig, string, error) {
	var fc fileConfig
	path, explicit := env.Getenv("WEIGHTKEEP_CONFIG"), true
	if path == "" {
		path, explicit = defaultConfigPath(env), false
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return fc, path, nil
	}
	if err != nil {
		return fc, path, fmt.Errorf("read config: %w", err)
	}

	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		return fc, path, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0].String()
		if key == "token" {
			return fc, path, fmt.Errorf("config %s: token: put tokens in HF_TOKEN or the Hugging Face token file, not in the config", path)
		}
		return fc, path, fmt.Errorf("config %s: unknown key %q", path, key)
	}

	if fc.Home != "" {
		fc.Home = expandTilde(fc.Home, env.HomeDir)
		if !filepath.IsAbs(fc.Home) {
			return fc, path, fmt.Errorf("config %s: home must be an absolute path, got %q", path, fc.Home)
		}
	}
	return fc, path, nil
}

func defaultConfigPath(env Env) string {
	var dir string
	if runtime.GOOS == "windows" {
		dir = absOr(env.Getenv("APPDATA"), filepath.Join(env.HomeDir, "AppData", "Roaming"))
	} else {
		dir = absOr(env.Getenv("XDG_CONFIG_HOME"), filepath.Join(env.HomeDir, ".config"))
	}
	return filepath.Join(dir, "weightkeep", "config.toml")
}

func resolveHome(env Env, fc fileConfig) (string, error) {
	if h := env.Getenv("WEIGHTKEEP_HOME"); h != "" {
		h = expandTilde(h, env.HomeDir)
		if !filepath.IsAbs(h) {
			return "", fmt.Errorf("WEIGHTKEEP_HOME must be an absolute path, got %q", h)
		}
		return filepath.Clean(h), nil
	}
	if fc.Home != "" {
		return filepath.Clean(fc.Home), nil
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(absOr(env.Getenv("LOCALAPPDATA"), filepath.Join(env.HomeDir, "AppData", "Local")), "weightkeep"), nil
	}
	// XDG on macOS too: this is a CLI tool, and ~/.local/share is where
	// people look for it.
	return filepath.Join(absOr(env.Getenv("XDG_DATA_HOME"), filepath.Join(env.HomeDir, ".local", "share")), "weightkeep"), nil
}

func normalizeUpstream(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("upstream %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("upstream %q: must be an http or https URL", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("upstream %q: must not contain a query, fragment or credentials", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// resolveHF mirrors huggingface_hub/constants.py.
func resolveHF(env Env, c *Config) {
	c.HFHome = expandTilde(env.Getenv("HF_HOME"), env.HomeDir)
	if c.HFHome == "" {
		cache := expandTilde(env.Getenv("XDG_CACHE_HOME"), env.HomeDir)
		if cache == "" {
			cache = filepath.Join(env.HomeDir, ".cache")
		}
		c.HFHome = filepath.Join(cache, "huggingface")
	}
	c.HFHubCache = expandTilde(firstNonEmpty(
		env.Getenv("HF_HUB_CACHE"),
		env.Getenv("HUGGINGFACE_HUB_CACHE"),
		filepath.Join(c.HFHome, "hub"),
	), env.HomeDir)
	c.HFTokenPath = expandTilde(firstNonEmpty(env.Getenv("HF_TOKEN_PATH"), filepath.Join(c.HFHome, "token")), env.HomeDir)
}

// resolveToken follows huggingface_hub's get_token: HF_TOKEN, then the
// legacy HUGGING_FACE_HUB_TOKEN, then the token file.
func resolveToken(env Env, c *Config) error {
	for _, name := range []string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"} {
		if t := NewToken(env.Getenv(name)); !t.IsZero() {
			c.Token, c.TokenSource = t, name
			return nil
		}
	}
	data, err := os.ReadFile(c.HFTokenPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read token file: %w", err)
	}
	if t := NewToken(string(data)); !t.IsZero() {
		c.Token, c.TokenSource = t, c.HFTokenPath
	}
	return nil
}

func expandTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}

// absOr returns p if it is an absolute path, else fallback. The XDG spec
// says relative values must be ignored.
func absOr(p, fallback string) string {
	if p != "" && filepath.IsAbs(p) {
		return p
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
