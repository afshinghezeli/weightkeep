package proxy

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/ids"
)

// routeKind is what a request asks for.
type routeKind int

const (
	routeUnknown   routeKind = iota
	routeResolve             // /{prefix}{repo}/resolve/{rev}/{path}
	routeInfo                // /api/{type}s/{repo}[/revision/{rev}]
	routeTree                // /api/{type}s/{repo}/tree/{rev}[/{path}]
	routeRefs                // /api/{type}s/{repo}/refs
	routePathsInfo           // /api/{type}s/{repo}/paths-info/{rev}
	routeAPIOther            // any other /api/ route: passed through
)

type route struct {
	kind routeKind
	repo hub.Repo
	rev  string
	path string // repo file path, or tree subpath
}

// parseRoute parses an escaped request path. Revisions arrive URL-encoded
// ("refs%2Fpr%2F1"), which is why this works on the escaped form.
func parseRoute(escaped string) (route, error) {
	if rest, ok := strings.CutPrefix(escaped, "/api/"); ok {
		return parseAPI(rest)
	}
	if i := strings.Index(escaped, "/resolve/"); i > 0 {
		return parseResolve(escaped[1:i], escaped[i+len("/resolve/"):])
	}
	return route{}, nil
}

func parseAPI(rest string) (route, error) {
	segment, rest, _ := strings.Cut(rest, "/")
	var t hub.RepoType
	switch segment {
	case "models":
		t = hub.Model
	case "datasets":
		t = hub.Dataset
	case "spaces":
		t = hub.Space
	default:
		return route{kind: routeAPIOther}, nil
	}
	for _, m := range []struct {
		marker string
		kind   routeKind
	}{
		{"/revision/", routeInfo},
		{"/tree/", routeTree},
		{"/paths-info/", routePathsInfo},
	} {
		if i := strings.Index(rest, m.marker); i > 0 {
			repo, err := repoFrom(t, rest[:i])
			if err != nil {
				return route{}, err
			}
			tail := rest[i+len(m.marker):]
			revEsc, pathEsc, _ := strings.Cut(tail, "/")
			rev, err := url.PathUnescape(revEsc)
			if err != nil || rev == "" {
				return route{}, fmt.Errorf("bad revision %q", revEsc)
			}
			r := route{kind: m.kind, repo: repo, rev: rev}
			if m.kind == routeInfo && pathEsc != "" {
				// "/revision/refs%2Fpr%2F1" is one segment; anything after
				// it is not an endpoint we know.
				return route{kind: routeAPIOther}, nil
			}
			if pathEsc != "" {
				if r.path, err = url.PathUnescape(pathEsc); err != nil {
					return route{}, err
				}
				r.path = strings.TrimSuffix(r.path, "/")
				if err := ids.ValidatePath(r.path); err != nil {
					return route{}, err
				}
			}
			return r, nil
		}
	}
	if id, ok := strings.CutSuffix(rest, "/refs"); ok {
		repo, err := repoFrom(t, id)
		return route{kind: routeRefs, repo: repo}, err
	}
	// /api/models/{repo} with nothing after it means the default branch.
	if repo, err := repoFrom(t, rest); err == nil {
		return route{kind: routeInfo, repo: repo, rev: "main"}, nil
	}
	return route{kind: routeAPIOther}, nil
}

func parseResolve(repoPart, tail string) (route, error) {
	t := hub.Model
	for _, candidate := range []hub.RepoType{hub.Dataset, hub.Space} {
		if rest, ok := strings.CutPrefix(repoPart, candidate.URLPrefix()); ok {
			t, repoPart = candidate, rest
		}
	}
	repo, err := repoFrom(t, repoPart)
	if err != nil {
		return route{}, err
	}
	revEsc, pathEsc, ok := strings.Cut(tail, "/")
	if !ok || pathEsc == "" {
		return route{}, fmt.Errorf("resolve URL without a file path")
	}
	rev, err := url.PathUnescape(revEsc)
	if err != nil || rev == "" {
		return route{}, fmt.Errorf("bad revision %q", revEsc)
	}
	// A hand-written client may send refs/pr/1 unencoded.
	if rev == "refs" {
		parts := strings.SplitN(pathEsc, "/", 3)
		if len(parts) == 3 && (parts[0] == "pr" || parts[0] == "convert") {
			rev, pathEsc = "refs/"+parts[0]+"/"+parts[1], parts[2]
		}
	}
	path, err := url.PathUnescape(pathEsc)
	if err != nil {
		return route{}, err
	}
	if err := ids.ValidatePath(path); err != nil {
		return route{}, err
	}
	return route{kind: routeResolve, repo: repo, rev: rev, path: path}, nil
}

func repoFrom(t hub.RepoType, escapedID string) (hub.Repo, error) {
	id, err := url.PathUnescape(escapedID)
	if err != nil {
		return hub.Repo{}, err
	}
	if err := ids.ValidateRepoID(id); err != nil {
		return hub.Repo{}, err
	}
	return hub.Repo{Type: t, ID: id}, nil
}
