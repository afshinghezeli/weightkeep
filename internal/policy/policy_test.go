package policy

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

// Every bundled text must exist and satisfy its own fingerprints; otherwise
// a repo that ships the genuine text would be refused.
func TestBundledTextsMatchTheirFingerprints(t *testing.T) {
	for _, l := range licences {
		if !l.bundled {
			continue
		}
		text, err := Text(l.spdx)
		if err != nil {
			t.Errorf("%s: %v", l.hfID, err)
			continue
		}
		if !matches(text, l.prints) {
			t.Errorf("%s: bundled %s text does not match fingerprints %q", l.hfID, l.spdx, l.prints)
		}
	}
}

func TestTableHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range licences {
		if seen[l.hfID] {
			t.Errorf("duplicate entry %q", l.hfID)
		}
		seen[l.hfID] = true
		if len(l.prints) == 0 {
			t.Errorf("%s has no fingerprints", l.hfID)
		}
	}
}

func TestTierPerLicence(t *testing.T) {
	want := map[string]Tier{
		"apache-2.0": A, "mit": A, "bsd-3-clause": A, "cc-by-4.0": A, "cc0-1.0": A, "mpl-2.0": A,
		"llama3.1": B1, "llama4": B1, "gemma": B1, "creativeml-openrail-m": B1, "cc-by-nd-4.0": B1, "gpl-3.0": B1,
		"cc-by-nc-4.0": B2, "cc-by-nc-sa-4.0": B2,
	}
	for id, tier := range want {
		if got := TierOf([]string{id}, "", "", false); got != tier {
			t.Errorf("%s: tier %s, want %s", id, got, tier)
		}
	}
	for _, c := range []struct {
		id, name string
		want     Tier
	}{
		{"other", "qwen", B1},
		{"other", "deepseek", B1},
		{"other", "mrl", B2},
		{"other", "some-custom-thing", C},
		{"other", "", C},
		{"unknown", "", C},
		{"APACHE-2.0", "", A}, // the Hub is case-insensitive
	} {
		if got := TierOf([]string{c.id}, c.name, "", false); got != c.want {
			t.Errorf("%s/%s: tier %s, want %s", c.id, c.name, got, c.want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	apache, _ := Text("Apache-2.0")
	mit, _ := Text("MIT")
	tests := []struct {
		name     string
		in       Input
		want     Tier
		supplied string
		reason   string
	}{
		{"permissive with its own file", Input{LicenseIDs: []string{"apache-2.0"}, LicenseFile: ptr(apache), LicenseFilePath: "LICENSE"}, A, "", ""},
		{"permissive without a file gets the bundled text", Input{LicenseIDs: []string{"mit"}}, A, "MIT", ""},
		{"file contradicts the card", Input{LicenseIDs: []string{"mit"}, LicenseFile: ptr(apache), LicenseFilePath: "LICENSE"}, C, "", "does not look like"},
		{"gated beats licence", Input{LicenseIDs: []string{"apache-2.0"}, Gated: "auto", LicenseFile: ptr(apache)}, C, "", "gated"},
		{"private", Input{LicenseIDs: []string{"mit"}, Private: true}, C, "", "private"},
		{"denylisted", Input{LicenseIDs: []string{"mit"}, Denylisted: true}, C, "", "denylist"},
		{"no licence", Input{}, C, "", "no licence declared"},
		{"unknown licence", Input{LicenseIDs: []string{"my-own-terms"}}, C, "", "not one weightkeep knows"},
		{"B1 needs its own file", Input{LicenseIDs: []string{"llama3.1"}}, C, "", "no licence file"},
		{"B1 with its file", Input{LicenseIDs: []string{"llama3.1"}, LicenseFile: ptr("LLAMA 3.1 COMMUNITY LICENSE AGREEMENT ..."), LicenseFilePath: "LICENSE"}, B1, "", "opting in"},
		{"strictest of several ids", Input{LicenseIDs: []string{"mit", "cc-by-nc-4.0"}, LicenseFile: ptr("Attribution-NonCommercial 4.0 International ... Permission is hereby granted, free of charge")}, B2, "", "non-commercial"},
		{"stricter base model", Input{LicenseIDs: []string{"apache-2.0"}, LicenseFile: ptr(apache), Bases: []Base{{ID: "meta/base", Tier: B1, Known: true}}}, B1, "", "base model meta/base"},
		{"unknown base model", Input{LicenseIDs: []string{"mit"}, LicenseFile: ptr(mit), Bases: []Base{{ID: "gone/base"}}}, C, "", "could not be checked"},
		{"looser base model changes nothing", Input{LicenseIDs: []string{"mit"}, LicenseFile: ptr(mit), Bases: []Base{{ID: "x/y", Tier: A, Known: true}}}, A, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Evaluate(tt.in)
			if d.Tier != tt.want {
				t.Errorf("tier %s, want %s (reasons %q)", d.Tier, tt.want, d.Reasons)
			}
			if d.SuppliedText != tt.supplied {
				t.Errorf("supplied text %q, want %q", d.SuppliedText, tt.supplied)
			}
			if tt.reason != "" && !strings.Contains(strings.Join(d.Reasons, "; "), tt.reason) {
				t.Errorf("reasons %q don't mention %q", d.Reasons, tt.reason)
			}
		})
	}
}

func TestFingerprintsIgnoreFormatting(t *testing.T) {
	// Real LICENSE files rewrap lines and change case.
	text := "  APACHE LICENSE\n        Version   2.0, January 2004\n"
	if !matches(text, byID["apache-2.0"].prints) {
		t.Error("reflowed Apache header not recognised")
	}
}

func TestIsLicenceFile(t *testing.T) {
	for _, p := range []string{"LICENSE", "LICENCE", "license.md", "License.txt", "COPYING"} {
		if !IsLicenceFile(p) {
			t.Errorf("%q should be a licence file", p)
		}
	}
	for _, p := range []string{"docs/LICENSE", "LICENSE-THIRD-PARTY", "license.json", "README.md"} {
		if IsLicenceFile(p) {
			t.Errorf("%q should not be a licence file", p)
		}
	}
}
