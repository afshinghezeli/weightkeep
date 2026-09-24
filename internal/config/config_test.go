package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture is a fake user environment rooted in a temp dir. Every path it
// hands out is absolute on the host OS, so the same tests run on Windows.
type fixture struct {
	t    *testing.T
	root string
	home string
	vars map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, home: filepath.Join(root, "home"), vars: map[string]string{}}
	if runtime.GOOS == "windows" {
		// Keep Windows defaults inside the fixture as well.
		f.vars["LOCALAPPDATA"] = f.path("local")
		f.vars["APPDATA"] = f.path("roaming")
	}
	return f
}

// path returns an absolute path inside the fixture.
func (f *fixture) path(elem ...string) string {
	return filepath.Join(append([]string{f.root}, elem...)...)
}

func (f *fixture) set(k, v string) *fixture { f.vars[k] = v; return f }

func (f *fixture) env() Env {
	return Env{HomeDir: f.home, Getenv: func(k string) string { return f.vars[k] }}
}

func (f *fixture) writeConfig(contents string) {
	f.t.Helper()
	writeFile(f.t, defaultConfigPath(f.env()), contents)
}

func (f *fixture) load() *Config {
	f.t.Helper()
	c, err := Load(f.env())
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// tomlPath quotes a path as a TOML literal string (no escape processing,
// so Windows backslashes survive).
func tomlPath(p string) string { return "'" + p + "'" }

func unixOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG defaults do not apply on Windows")
	}
}

func TestHomeDefaults(t *testing.T) {
	t.Run("XDG default", func(t *testing.T) {
		unixOnly(t)
		f := newFixture(t)
		if got, want := f.load().Home, filepath.Join(f.home, ".local", "share", "weightkeep"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})
	t.Run("XDG_DATA_HOME", func(t *testing.T) {
		unixOnly(t)
		f := newFixture(t)
		f.set("XDG_DATA_HOME", f.path("xdg"))
		if got, want := f.load().Home, f.path("xdg", "weightkeep"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})
	t.Run("relative XDG_DATA_HOME is ignored, as the XDG spec says", func(t *testing.T) {
		unixOnly(t)
		f := newFixture(t)
		f.set("XDG_DATA_HOME", "relative/dir")
		if got, want := f.load().Home, filepath.Join(f.home, ".local", "share", "weightkeep"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})
	t.Run("windows LOCALAPPDATA", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("windows only")
		}
		f := newFixture(t)
		if got, want := f.load().Home, f.path("local", "weightkeep"); got != want {
			t.Errorf("Home = %q, want %q", got, want)
		}
	})
}

func TestHomePrecedence(t *testing.T) {
	f := newFixture(t)
	f.set("XDG_DATA_HOME", f.path("xdg"))
	f.writeConfig("home = " + tomlPath(f.path("from-file")))
	if got, want := f.load().Home, f.path("from-file"); got != want {
		t.Errorf("config file should beat XDG: Home = %q, want %q", got, want)
	}

	f.set("WEIGHTKEEP_HOME", f.path("from-env"))
	if got, want := f.load().Home, f.path("from-env"); got != want {
		t.Errorf("WEIGHTKEEP_HOME should beat the config file: Home = %q, want %q", got, want)
	}
}

func TestHomeTildeInConfig(t *testing.T) {
	f := newFixture(t)
	f.writeConfig(`home = "~/models"`)
	if got, want := f.load().Home, filepath.Join(f.home, "models"); got != want {
		t.Errorf("Home = %q, want %q", got, want)
	}
}

func TestConfigFilePath(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		unixOnly(t)
		f := newFixture(t)
		if got, want := f.load().ConfigFile, filepath.Join(f.home, ".config", "weightkeep", "config.toml"); got != want {
			t.Errorf("ConfigFile = %q, want %q", got, want)
		}
	})
	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		unixOnly(t)
		f := newFixture(t)
		f.set("XDG_CONFIG_HOME", f.path("cfg"))
		if got, want := f.load().ConfigFile, f.path("cfg", "weightkeep", "config.toml"); got != want {
			t.Errorf("ConfigFile = %q, want %q", got, want)
		}
	})
	t.Run("windows APPDATA", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("windows only")
		}
		f := newFixture(t)
		if got, want := f.load().ConfigFile, f.path("roaming", "weightkeep", "config.toml"); got != want {
			t.Errorf("ConfigFile = %q, want %q", got, want)
		}
	})
	t.Run("WEIGHTKEEP_CONFIG", func(t *testing.T) {
		f := newFixture(t)
		p := f.path("etc", "wk.toml")
		writeFile(t, p, `upstream = "https://mirror.example"`)
		f.set("WEIGHTKEEP_CONFIG", p)
		c := f.load()
		if c.ConfigFile != p {
			t.Errorf("ConfigFile = %q, want %q", c.ConfigFile, p)
		}
		if c.Upstream != "https://mirror.example" {
			t.Errorf("file at WEIGHTKEEP_CONFIG was not read: Upstream = %q", c.Upstream)
		}
	})
	t.Run("WEIGHTKEEP_CONFIG must exist", func(t *testing.T) {
		f := newFixture(t)
		f.set("WEIGHTKEEP_CONFIG", f.path("missing.toml"))
		if _, err := Load(f.env()); err == nil {
			t.Fatal("expected an error for a missing WEIGHTKEEP_CONFIG file")
		}
	})
}

func TestUpstream(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		file    string
		want    string
		wantErr string
	}{
		{name: "default", want: "https://huggingface.co"},
		{name: "HF_ENDPOINT is ignored", vars: map[string]string{"HF_ENDPOINT": "http://127.0.0.1:8700"}, want: "https://huggingface.co"},
		{name: "config file", file: `upstream = "https://hf-mirror.example/"`, want: "https://hf-mirror.example"},
		{name: "env beats file", vars: map[string]string{"WEIGHTKEEP_UPSTREAM": "http://10.0.0.5:8080"}, file: `upstream = "https://hf-mirror.example"`, want: "http://10.0.0.5:8080"},
		{name: "keeps a path prefix", vars: map[string]string{"WEIGHTKEEP_UPSTREAM": "https://proxy.example/hf/"}, want: "https://proxy.example/hf"},
		{name: "rejects non-http", vars: map[string]string{"WEIGHTKEEP_UPSTREAM": "ftp://x"}, wantErr: "upstream"},
		{name: "rejects relative", vars: map[string]string{"WEIGHTKEEP_UPSTREAM": "huggingface.co"}, wantErr: "upstream"},
		{name: "rejects query", vars: map[string]string{"WEIGHTKEEP_UPSTREAM": "https://x.example/?a=b"}, wantErr: "upstream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			for k, v := range tt.vars {
				f.set(k, v)
			}
			if tt.file != "" {
				f.writeConfig(tt.file)
			}
			c, err := Load(f.env())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Upstream != tt.want {
				t.Errorf("Upstream = %q, want %q", c.Upstream, tt.want)
			}
		})
	}
}

func TestServeAddr(t *testing.T) {
	f := newFixture(t)
	if got := f.load().ServeAddr; got != "127.0.0.1:8700" {
		t.Errorf("default ServeAddr = %q", got)
	}
	f.writeConfig("[serve]\naddr = \"127.0.0.1:1234\"\n")
	if got := f.load().ServeAddr; got != "127.0.0.1:1234" {
		t.Errorf("ServeAddr from file = %q", got)
	}
	f.set("WEIGHTKEEP_ADDR", "0.0.0.0:9000")
	if got := f.load().ServeAddr; got != "0.0.0.0:9000" {
		t.Errorf("ServeAddr = %q, want env value", got)
	}
}

func TestHFPaths(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := newFixture(t)
		c := f.load()
		hf := filepath.Join(f.home, ".cache", "huggingface")
		check(t, "HFHome", c.HFHome, hf)
		check(t, "HFHubCache", c.HFHubCache, filepath.Join(hf, "hub"))
		check(t, "HFTokenPath", c.HFTokenPath, filepath.Join(hf, "token"))
	})
	t.Run("XDG_CACHE_HOME moves HF_HOME", func(t *testing.T) {
		f := newFixture(t)
		f.set("XDG_CACHE_HOME", f.path("xc"))
		c := f.load()
		check(t, "HFHome", c.HFHome, f.path("xc", "huggingface"))
		check(t, "HFHubCache", c.HFHubCache, f.path("xc", "huggingface", "hub"))
	})
	t.Run("HF_HOME beats XDG_CACHE_HOME", func(t *testing.T) {
		f := newFixture(t)
		f.set("XDG_CACHE_HOME", f.path("xc")).set("HF_HOME", f.path("hf"))
		c := f.load()
		check(t, "HFHome", c.HFHome, f.path("hf"))
		check(t, "HFHubCache", c.HFHubCache, f.path("hf", "hub"))
		check(t, "HFTokenPath", c.HFTokenPath, f.path("hf", "token"))
	})
	t.Run("HF_HUB_CACHE and HF_TOKEN_PATH", func(t *testing.T) {
		f := newFixture(t)
		f.set("HF_HOME", f.path("hf")).set("HF_HUB_CACHE", f.path("cache")).set("HF_TOKEN_PATH", f.path("secret", "t"))
		c := f.load()
		check(t, "HFHubCache", c.HFHubCache, f.path("cache"))
		check(t, "HFTokenPath", c.HFTokenPath, f.path("secret", "t"))
	})
	t.Run("legacy HUGGINGFACE_HUB_CACHE", func(t *testing.T) {
		f := newFixture(t)
		f.set("HUGGINGFACE_HUB_CACHE", f.path("legacy"))
		check(t, "HFHubCache", f.load().HFHubCache, f.path("legacy"))
	})
}

func TestToken(t *testing.T) {
	tests := []struct {
		name       string
		vars       map[string]string
		fileToken  string // written to the default token path
		wantValue  string
		wantSource string // "file" means the default token path
	}{
		{name: "none"},
		{name: "HF_TOKEN", vars: map[string]string{"HF_TOKEN": "hf_env"}, fileToken: "hf_file", wantValue: "hf_env", wantSource: "HF_TOKEN"},
		{name: "HF_TOKEN is trimmed", vars: map[string]string{"HF_TOKEN": "  hf_env\n"}, wantValue: "hf_env", wantSource: "HF_TOKEN"},
		{name: "legacy env var", vars: map[string]string{"HUGGING_FACE_HUB_TOKEN": "hf_old"}, wantValue: "hf_old", wantSource: "HUGGING_FACE_HUB_TOKEN"},
		{name: "HF_TOKEN beats legacy", vars: map[string]string{"HF_TOKEN": "hf_new", "HUGGING_FACE_HUB_TOKEN": "hf_old"}, wantValue: "hf_new", wantSource: "HF_TOKEN"},
		{name: "blank HF_TOKEN falls through to file", vars: map[string]string{"HF_TOKEN": "   "}, fileToken: "hf_file\n", wantValue: "hf_file", wantSource: "file"},
		{name: "token file", fileToken: "hf_file\n", wantValue: "hf_file", wantSource: "file"},
		{name: "empty token file", fileToken: "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			for k, v := range tt.vars {
				f.set(k, v)
			}
			tokenPath := filepath.Join(f.home, ".cache", "huggingface", "token")
			if tt.fileToken != "" {
				writeFile(t, tokenPath, tt.fileToken)
			}
			c := f.load()
			check(t, "Token", c.Token.Value(), tt.wantValue)
			wantSource := tt.wantSource
			if wantSource == "file" {
				wantSource = tokenPath
			}
			check(t, "TokenSource", c.TokenSource, wantSource)
		})
	}
}

func TestTokenNeverPrinted(t *testing.T) {
	const secret = "hf_supersecretvalue123"
	tok := NewToken(secret)
	c := Config{Token: tok, Upstream: "https://huggingface.co"}

	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	logger.Info("loaded", "token", tok, "config", c)

	outputs := map[string]string{
		"%s":         fmt.Sprintf("%s", tok), //nolint:staticcheck // S1025: exercising fmt's %s path on purpose
		"%v":         fmt.Sprintf("%v", tok),
		"%+v":        fmt.Sprintf("%+v", tok),
		"%#v":        fmt.Sprintf("%#v", tok),
		"config %v":  fmt.Sprintf("%v", c),
		"config %+v": fmt.Sprintf("%+v", c),
		"config %#v": fmt.Sprintf("%#v", c),
		"slog":       logged.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, "supersecret") {
			t.Errorf("%s leaks the token: %s", name, out)
		}
	}
	check(t, "redacted form", fmt.Sprintf("%v", tok), "hf_***")
	check(t, "empty token", fmt.Sprintf("%v", Token{}), "")
}

func TestConfigFileErrors(t *testing.T) {
	tests := []struct {
		name, file, wantErr string
	}{
		{"unknown key", `uptsream = "https://x"`, "uptsream"},
		{"bad toml", `home = `, "config"},
		{"relative home", `home = "models"`, "home"},
		{"token in config is refused", `token = "hf_x"`, "token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.writeConfig(tt.file)
			_, err := Load(f.env())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func check(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMirrors(t *testing.T) {
	f := newFixture(t)
	f.writeConfig("mirrors = [\"http://nas.local:8700/\", \"https://hf-mirror.com\"]\n")
	c := f.load()
	if len(c.Mirrors) != 2 || c.Mirrors[0] != "http://nas.local:8700" {
		t.Errorf("Mirrors from file = %v", c.Mirrors)
	}
	f.set("WEIGHTKEEP_MIRRORS", "http://a.example, http://b.example")
	if c := f.load(); len(c.Mirrors) != 2 || c.Mirrors[1] != "http://b.example" {
		t.Errorf("Mirrors from env = %v", c.Mirrors)
	}
	f.set("WEIGHTKEEP_MIRRORS", "ftp://nope")
	if _, err := Load(f.env()); err == nil {
		t.Error("bad mirror accepted")
	}
}

func TestRegistryConfig(t *testing.T) {
	f := newFixture(t)
	f.writeConfig("[registry]\nurl = \"https://registry.example/\"\nroot = \"~/wk-root.json\"\n")
	c := f.load()
	if c.RegistryURL != "https://registry.example" || c.RegistryRoot != filepath.Join(f.home, "wk-root.json") {
		t.Errorf("registry = %q %q", c.RegistryURL, c.RegistryRoot)
	}
}
