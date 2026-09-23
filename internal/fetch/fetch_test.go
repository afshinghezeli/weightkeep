package fetch

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

var (
	cfgFile   = fakehub.File{Path: "config.json", Content: []byte(`{"n": 1}`)}
	bigFile   = fakehub.File{Path: "model.safetensors", Content: bytes.Repeat([]byte("0123456789"), 50_000), LFS: true}
	emptyFile = fakehub.File{Path: "empty/__init__.py", Content: []byte{}}
	repo      = hub.Repo{Type: hub.Model, ID: "acme/tiny"}
)

type env struct {
	hub     *fakehub.Hub
	fetcher *Fetcher
	commit  string
	targets map[string]Target
	events  *eventLog
}

type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) count(path string, s State) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if e.Path == path && e.State == s {
			n++
		}
	}
	return n
}

func setup(t *testing.T, files ...fakehub.File) *env {
	t.Helper()
	if len(files) == 0 {
		files = []fakehub.File{cfgFile, bigFile, emptyFile}
	}
	h := fakehub.New(&fakehub.Repo{ID: repo.ID, Files: files})
	t.Cleanup(h.Close)
	client, err := hub.New(h.URL, hub.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	info, err := client.RepoInfo(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.Tree(ctx, repo, info.SHA)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{hub: h, commit: info.SHA, targets: map[string]Target{}, events: &eventLog{}}
	for _, entry := range tree {
		if entry.IsFile() {
			e.targets[entry.Path] = TargetFromTree(repo, info.SHA, entry)
		}
	}
	e.fetcher = &Fetcher{Store: st, Hub: client, Backoff: time.Millisecond, Events: e.events.add}
	return e
}

func (e *env) content(t *testing.T, b store.Blob) []byte {
	t.Helper()
	data, err := os.ReadFile(e.fetcher.Store.Path(b.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFetchAll(t *testing.T) {
	e := setup(t)
	var targets []Target
	for _, tg := range e.targets {
		targets = append(targets, tg)
	}
	for _, r := range e.fetcher.Files(context.Background(), targets) {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.Target.Path, r.Err)
		}
	}
	for _, f := range []fakehub.File{cfgFile, bigFile, emptyFile} {
		b, ok := e.fetcher.Have(context.Background(), e.targets[f.Path])
		if !ok {
			t.Fatalf("%s not in store", f.Path)
		}
		if b.SHA256 != f.SHA256() || !bytes.Equal(e.content(t, b), f.Content) {
			t.Errorf("%s: stored content differs", f.Path)
		}
	}

	// Second run: everything skipped, nothing downloaded.
	e.hub.ResetRequests()
	for _, r := range e.fetcher.Files(context.Background(), targets) {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	if reqs := e.hub.Requests(); len(reqs) != 0 {
		t.Errorf("second fetch made %d requests: %+v", len(reqs), reqs)
	}
	if n := e.events.count(bigFile.Path, Skipped); n != 1 {
		t.Errorf("Skipped events for %s = %d, want 1", bigFile.Path, n)
	}
}

func TestResumeAfterDroppedConnection(t *testing.T) {
	e := setup(t)
	e.hub.CutNextDownload(123_456)
	b, err := e.fetcher.File(context.Background(), e.targets[bigFile.Path])
	if err != nil {
		t.Fatal(err)
	}
	if b.SHA256 != bigFile.SHA256() {
		t.Fatalf("wrong blob %s", b.SHA256)
	}
	var ranges []string
	for _, r := range e.hub.Requests() {
		if r.Host == "cdn" {
			ranges = append(ranges, r.Range)
		}
	}
	if len(ranges) != 2 || ranges[0] != "" || ranges[1] != "bytes=123456-" {
		t.Errorf("CDN ranges = %q, want a full request then a resume from 123456", ranges)
	}
	if e.events.count(bigFile.Path, Retrying) != 1 {
		t.Error("expected one Retrying event")
	}
}

func TestResumeAcrossRuns(t *testing.T) {
	e := setup(t)
	tg := e.targets[bigFile.Path]
	// A previous run left half the file behind.
	p, err := e.fetcher.Store.Partial(context.Background(), tg.key(), tg.expect())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(bigFile.Content[:200_000]); err != nil {
		t.Fatal(err)
	}
	p.Close()

	if _, err := e.fetcher.File(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	for _, r := range e.hub.Requests() {
		if r.Host == "cdn" && r.Range != "bytes=200000-" {
			t.Errorf("CDN request range %q, want bytes=200000-", r.Range)
		}
	}
}

func TestRetriesServerErrorsThenSucceeds(t *testing.T) {
	e := setup(t)
	e.hub.Fail("/resolve/", http.StatusServiceUnavailable, 2)
	if _, err := e.fetcher.File(context.Background(), e.targets[bigFile.Path]); err != nil {
		t.Fatalf("503, 503, then success: %v", err)
	}
}

func TestGivesUpOnPersistentFailure(t *testing.T) {
	e := setup(t)
	e.fetcher.Retries = 2
	e.hub.Fail("/resolve/", http.StatusBadGateway, 100)
	_, err := e.fetcher.File(context.Background(), e.targets[bigFile.Path])
	if !errors.Is(err, hub.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestNoRetryOnNotFound(t *testing.T) {
	e := setup(t)
	tg := e.targets[bigFile.Path]
	e.hub.Remove(repo.ID)
	_, err := e.fetcher.File(context.Background(), tg)
	if !errors.Is(err, hub.ErrRepoNotFound) {
		t.Fatalf("err = %v, want ErrRepoNotFound", err)
	}
	if n := len(e.hub.Requests()); n > 3 { // RepoInfo + Tree during setup + one resolve
		t.Errorf("made %d requests; 404 must not be retried", n)
	}
}

func TestRejectsContentThatDoesNotMatchTree(t *testing.T) {
	e := setup(t)
	tg := e.targets[bigFile.Path]
	// The Hub now serves different bytes than the tree listing described.
	tampered := bigFile
	tampered.Content = bytes.Repeat([]byte("x"), len(bigFile.Content))
	e.hub.Add(&fakehub.Repo{ID: repo.ID, Commit: e.commit, Files: []fakehub.File{cfgFile, tampered, emptyFile}})

	_, err := e.fetcher.File(context.Background(), tg)
	if !errors.Is(err, store.ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
	if e.fetcher.Store.Has(tampered.SHA256()) || e.fetcher.Store.Has(bigFile.SHA256()) {
		t.Error("mismatched content was stored")
	}
}

func TestWaitsForAnotherWriter(t *testing.T) {
	e := setup(t)
	tg := e.targets[cfgFile.Path]
	// Another process is downloading the same file.
	other, err := e.fetcher.Store.Partial(context.Background(), tg.key(), tg.expect())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.fetcher.File(context.Background(), tg)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := other.Write(cfgFile.Content); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch didn't notice the other writer finished")
	}
	for _, r := range e.hub.Requests() {
		if strings.Contains(r.Path, "/resolve/") {
			t.Errorf("downloaded although another writer produced the blob: %+v", r)
		}
	}
}

func TestCancel(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.fetcher.File(ctx, e.targets[bigFile.Path])
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
