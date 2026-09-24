// Package policy decides what weightkeep may share. Keeping a private copy
// is never restricted; sharing (seeding, the registry) depends on the
// model's licence, whether the repo is gated, and its base models.
//
// The rules are ADR 0007 as amended by ADR 0009. Everything here is data
// plus a pure function, so each licence has a table-driven test.
package policy

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

// Tier orders how freely a model may be shared. Higher is stricter.
type Tier int

const (
	A  Tier = iota // permissive: shared by default
	B1             // conditions: opt-in per model
	B2             // non-commercial: opt-in, non-commercial operators only
	C              // never shared
)

func (t Tier) String() string {
	return [...]string{"A", "B1", "B2", "C"}[t]
}

// Summary is one line on what the tier allows.
func (t Tier) Summary() string {
	return [...]string{
		"shared by default",
		"shared only after opting in for this model; licence conditions apply",
		"shared only after opting in, and only for non-commercial use",
		"never shared",
	}[t]
}

// Input is what the decision is based on.
type Input struct {
	LicenseIDs  []string // cardData.license
	LicenseName string   // cardData.license_name, for license: other
	Gated       string   // "", "auto", "manual", "true"
	Private     bool
	Denylisted  bool
	// LicenseFile is the text of the repo's own licence file, nil if it has none.
	LicenseFile     *string
	LicenseFilePath string
	// Bases are the tiers of cardData.base_model entries, as far as known.
	Bases []Base
}

// Base is a base model and the tier its own licence puts it in.
type Base struct {
	ID    string
	Tier  Tier
	Known bool // false if it couldn't be checked (offline, removed)
}

// Decision is the outcome, with reasons a person can read.
type Decision struct {
	Tier    Tier
	License string // what the decision is about, e.g. "Apache License 2.0"
	SPDX    string
	// SuppliedText is set when the repo has no licence file and weightkeep
	// will attach this SPDX text to anything it shares (ADR 0009).
	SuppliedText string
	Reasons      []string
	Notes        []string
}

func (d *Decision) because(t Tier, format string, args ...any) {
	if t > d.Tier {
		d.Tier = t
	}
	d.Reasons = append(d.Reasons, fmt.Sprintf(format, args...))
}

// Evaluate applies the rules. It never returns a tier less strict than the
// evidence supports: anything unknown is C.
func Evaluate(in Input) Decision {
	d := Decision{Tier: A}
	if in.Denylisted {
		d.because(C, "on the denylist")
	}
	if in.Private {
		d.because(C, "private repository")
	}
	if in.Gated != "" && in.Gated != "false" {
		d.because(C, "gated repository: access is an agreement with the author, so it is never shared")
	}

	lic, ok := lookup(in.LicenseIDs, in.LicenseName)
	switch {
	case len(in.LicenseIDs) == 0:
		d.because(C, "no licence declared in the model card")
	case !ok:
		name := strings.Join(in.LicenseIDs, ", ")
		if in.LicenseName != "" {
			name += " (" + in.LicenseName + ")"
		}
		d.License = name
		d.because(C, "licence %q is not one weightkeep knows how to share", name)
	default:
		d.License, d.SPDX = lic.name, lic.spdx
		if lic.tier > A {
			d.because(lic.tier, "%s: %s", lic.name, lic.tier.Summary())
		}
		if lic.note != "" {
			d.Notes = append(d.Notes, lic.note)
		}
		switch {
		case in.LicenseFile != nil && !matches(*in.LicenseFile, lic.prints):
			d.because(C, "%s does not look like the %s the model card declares", in.LicenseFilePath, lic.name)
		case in.LicenseFile == nil && lic.tier == A && lic.bundled:
			d.SuppliedText = lic.spdx
		case in.LicenseFile == nil:
			d.because(C, "%s requires its text to be passed on, and the repo has no licence file", lic.name)
		}
	}

	for _, b := range in.Bases {
		switch {
		case !b.Known:
			d.because(C, "base model %s could not be checked", b.ID)
		case b.Tier > d.Tier:
			d.because(b.Tier, "base model %s is tier %s", b.ID, b.Tier)
		}
	}
	if d.Tier == C {
		d.SuppliedText = "" // nothing will be shared, so nothing to attach
	} else if d.SuppliedText != "" {
		d.Notes = append(d.Notes, "no licence file in the repo; weightkeep attaches the "+d.SuppliedText+" text when sharing")
	}
	return d
}

// TierOf is the tier a declared licence alone implies, for base models
// where only the model card is looked at.
func TierOf(ids []string, name, gated string, private bool) Tier {
	if private || (gated != "" && gated != "false") {
		return C
	}
	lic, ok := lookup(ids, name)
	if !ok || len(ids) == 0 {
		return C
	}
	return lic.tier
}

// lookup finds the strictest known licence among ids. "other" is resolved
// through license_name. Any unknown id makes the whole lookup fail.
func lookup(ids []string, name string) (licence, bool) {
	var best licence
	found := false
	for _, id := range ids {
		key := strings.ToLower(strings.TrimSpace(id))
		if key == "other" {
			key = strings.ToLower(strings.TrimSpace(name))
		}
		l, ok := byID[key]
		if !ok {
			return licence{}, false
		}
		if !found || l.tier > best.tier {
			best, found = l, true
		}
	}
	return best, found
}

var byID = func() map[string]licence {
	m := make(map[string]licence, len(licences))
	for _, l := range licences {
		m[l.hfID] = l
	}
	return m
}()

var spaces = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	return spaces.ReplaceAllString(strings.ToLower(s), " ")
}

func matches(text string, prints []string) bool {
	t := normalize(text)
	for _, p := range prints {
		if !strings.Contains(t, p) {
			return false
		}
	}
	return true
}

//go:embed texts/*.txt
var texts embed.FS

// Text returns the bundled text of an SPDX licence.
func Text(spdx string) (string, error) {
	b, err := texts.ReadFile("texts/" + spdx + ".txt")
	if err != nil {
		return "", fmt.Errorf("no bundled text for %s", spdx)
	}
	return string(b), nil
}

// licenceFileRE matches the files treated as the repo's licence.
var licenceFileRE = regexp.MustCompile(`(?i)^(licen[cs]e|copying)(\.(md|txt|rst))?$`)

// IsLicenceFile reports whether a repo path is its top-level licence file.
func IsLicenceFile(path string) bool {
	return !strings.Contains(path, "/") && licenceFileRE.MatchString(path)
}
