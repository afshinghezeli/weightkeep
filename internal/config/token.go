package config

import (
	"log/slog"
	"strings"
)

// Token is a Hugging Face access token. It formats as "hf_***" everywhere
// (fmt verbs, slog, JSON, text marshalling) so it can't end up in logs or
// bug reports by accident. Use Value to get the secret itself.
type Token struct {
	secret string
}

// NewToken wraps s, trimming surrounding whitespace.
func NewToken(s string) Token {
	return Token{secret: strings.TrimSpace(s)}
}

// Value returns the secret. Only the Hub client should call it.
func (t Token) Value() string { return t.secret }

// IsZero reports whether no token is set.
func (t Token) IsZero() bool { return t.secret == "" }

func (t Token) redacted() string {
	switch {
	case t.secret == "":
		return ""
	case strings.HasPrefix(t.secret, "hf_"):
		return "hf_***"
	default:
		return "***"
	}
}

func (t Token) String() string               { return t.redacted() }
func (t Token) GoString() string             { return `config.Token("` + t.redacted() + `")` }
func (t Token) LogValue() slog.Value         { return slog.StringValue(t.redacted()) }
func (t Token) MarshalText() ([]byte, error) { return []byte(t.redacted()), nil }
