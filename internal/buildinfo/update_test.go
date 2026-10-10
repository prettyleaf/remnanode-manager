package buildinfo

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.9", 1},
		{"v1.10.0", "1.9.9", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"dev", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestChannel(t *testing.T) {
	if got := channel(Info{Version: "1.2.3"}); got != ChannelRelease {
		t.Fatalf("release: %s", got)
	}
	if got := channel(Info{Version: "dev-abc", Branch: "dev", Commit: "abc"}); got != ChannelBranch {
		t.Fatalf("branch: %s", got)
	}
	if got := channel(Info{Version: "dev"}); got != ChannelNone {
		t.Fatalf("none: %s", got)
	}
}

func TestCheckRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/prettyleaf/remnanode-manager/releases/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"0.3.0","html_url":"https://github.com/prettyleaf/remnanode-manager/releases/tag/0.3.0"}`))
	}))
	defer srv.Close()

	oldVersion := Version
	Version = "0.2.1"
	defer func() { Version = oldVersion }()

	u := NewUpdater(true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.apiBase = srv.URL
	u.Check(context.Background())
	st := u.Status()
	if !st.Available || st.Latest != "0.3.0" || st.Error != "" {
		t.Fatalf("unexpected status: %+v", st)
	}
}
