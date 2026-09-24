package torrent

import (
	"fmt"
	"sort"
	"time"

	"github.com/anacrolix/torrent/bencode"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

// infoKey is the info dict key that carries a revision's manifest.
const infoKey = "weightkeep"

// embedded is the manifest as stored inside the info dict. It holds only
// what describes the revision's content (no fetch time or upstream), so
// every node seeding the same revision builds the same info hash and joins
// the same swarm. Being inside the info dict, it is authenticated by the
// info hash: a magnet link alone is enough to check every file's SHA-256.
type embedded struct {
	Version int             `bencode:"version"`
	Type    string          `bencode:"type"`
	Repo    string          `bencode:"repo"`
	Commit  string          `bencode:"commit"`
	License embeddedLicence `bencode:"license"`
	Files   []embeddedFile  `bencode:"files"`
}

type embeddedLicence struct {
	IDs   []string `bencode:"ids,omitempty"`
	Name  string   `bencode:"name,omitempty"`
	Link  string   `bencode:"link,omitempty"`
	Bases []string `bencode:"base_models,omitempty"`
}

type embeddedFile struct {
	Path    string `bencode:"path"`
	SHA256  string `bencode:"sha256"`
	GitSHA1 string `bencode:"git_sha1"`
	LFS     int    `bencode:"lfs"`
}

// ManifestInfoExtra returns the InfoExtra that embeds m in a torrent.
func ManifestInfoExtra(m *manifest.Manifest) map[string]any {
	e := embedded{
		Version: manifest.FormatVersion, Type: m.Repo.Type, Repo: m.Repo.ID, Commit: m.Commit,
		License: embeddedLicence{IDs: m.License.IDs, Name: m.License.Name, Link: m.License.Link, Bases: m.License.BaseModels},
	}
	files := append([]manifest.File(nil), m.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		lfs := 0
		if f.LFS {
			lfs = 1
		}
		e.Files = append(e.Files, embeddedFile{Path: f.Path, SHA256: f.SHA256, GitSHA1: f.GitSHA1, LFS: lfs})
	}
	return map[string]any{infoKey: e}
}

// EmbeddedManifest reads the manifest from a torrent's info bytes. Sizes
// come from the torrent's file list. fetchedAt and upstream describe how
// this node got it.
func EmbeddedManifest(infoBytes []byte, fetchedAt time.Time, upstream string) (*manifest.Manifest, error) {
	var info struct {
		Files []struct {
			Length int64    `bencode:"length"`
			Path   []string `bencode:"path"`
			Attr   string   `bencode:"attr,omitempty"`
		} `bencode:"files"`
		Weightkeep *embedded `bencode:"weightkeep"`
	}
	if err := bencode.Unmarshal(infoBytes, &info); err != nil {
		return nil, fmt.Errorf("read torrent info: %w", err)
	}
	e := info.Weightkeep
	if e == nil {
		return nil, fmt.Errorf("torrent carries no weightkeep manifest; it wasn't made by `weightkeep seed`")
	}
	sizes := map[string]int64{}
	for _, f := range info.Files {
		if f.Attr != "p" {
			sizes[joinPath(f.Path)] = f.Length
		}
	}
	m := &manifest.Manifest{
		Version: e.Version, Repo: manifest.Repo{Type: e.Type, ID: e.Repo}, Commit: e.Commit,
		FetchedAt: fetchedAt, Upstream: upstream,
		License: manifest.License{IDs: e.License.IDs, Name: e.License.Name, Link: e.License.Link, BaseModels: e.License.Bases},
	}
	for _, f := range e.Files {
		size, ok := sizes[f.Path]
		if !ok {
			return nil, fmt.Errorf("manifest lists %s, which the torrent doesn't contain", f.Path)
		}
		delete(sizes, f.Path)
		m.Files = append(m.Files, manifest.File{Path: f.Path, Size: size, SHA256: f.SHA256, GitSHA1: f.GitSHA1, LFS: f.LFS == 1})
	}
	if len(sizes) > 0 {
		return nil, fmt.Errorf("torrent has %d file(s) its manifest doesn't list", len(sizes))
	}
	// Round-trip through canonical form: validates ids, paths and hashes.
	data, err := m.Canonical()
	if err != nil {
		return nil, err
	}
	return manifest.Parse(data)
}
