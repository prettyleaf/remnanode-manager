package buildinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ChannelRelease = "release" // tagged builds compare against the latest GitHub release
	ChannelBranch  = "branch"  // dev builds compare their commit with the branch head
	ChannelNone    = "none"

	checkInterval = 6 * time.Hour
)

type UpdateStatus struct {
	Enabled   bool       `json:"enabled"`
	Channel   string     `json:"channel"`
	Available bool       `json:"available"`
	Latest    string     `json:"latest,omitempty"`
	URL       string     `json:"url,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Updater periodically asks GitHub whether a newer build exists.
type Updater struct {
	enabled bool
	apiBase string
	client  *http.Client
	log     *slog.Logger

	mu     sync.RWMutex
	status UpdateStatus
}

func NewUpdater(enabled bool, log *slog.Logger) *Updater {
	u := &Updater{
		enabled: enabled,
		apiBase: "https://api.github.com",
		client:  &http.Client{Timeout: 15 * time.Second},
		log:     log,
	}
	u.status = UpdateStatus{Enabled: enabled, Channel: channel(Get())}
	return u
}

func (u *Updater) Status() UpdateStatus {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status
}

func (u *Updater) Run(ctx context.Context) {
	if !u.enabled || u.Status().Channel == ChannelNone {
		return
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		u.Check(ctx)
		timer.Reset(checkInterval)
	}
}

func (u *Updater) Check(ctx context.Context) {
	info := Get()
	st := UpdateStatus{Enabled: u.enabled, Channel: channel(info)}
	now := time.Now()
	st.CheckedAt = &now

	var err error
	switch st.Channel {
	case ChannelRelease:
		err = u.checkRelease(ctx, info, &st)
	case ChannelBranch:
		err = u.checkBranch(ctx, info, &st)
	default:
		return
	}
	if err != nil {
		st.Error = err.Error()
		u.log.Debug("update check failed", "err", err)
	}
	u.mu.Lock()
	u.status = st
	u.mu.Unlock()
}

func (u *Updater) checkRelease(ctx context.Context, info Info, st *UpdateStatus) error {
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := u.get(ctx, fmt.Sprintf("/repos/%s/releases/latest", info.Repository), &rel); err != nil {
		return err
	}
	st.Latest = rel.TagName
	st.URL = rel.HTMLURL
	st.Available = compareVersions(rel.TagName, info.Version) > 0
	return nil
}

func (u *Updater) checkBranch(ctx context.Context, info Info, st *UpdateStatus) error {
	var head struct {
		SHA string `json:"sha"`
	}
	if err := u.get(ctx, fmt.Sprintf("/repos/%s/commits/%s", info.Repository, url.PathEscape(info.Branch)), &head); err != nil {
		return err
	}
	st.Latest = head.SHA
	st.Available = head.SHA != "" && !strings.EqualFold(head.SHA, info.Commit)
	if st.Available {
		st.URL = fmt.Sprintf("https://github.com/%s/compare/%s...%s", info.Repository, info.Commit, head.SHA)
	}
	return nil
}

func (u *Updater) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "remnanode-manager/"+Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github: %s", resp.Status)
	}
	return json.Unmarshal(body, out)
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func channel(info Info) string {
	switch {
	case semverRe.MatchString(info.Version):
		return ChannelRelease
	case info.Branch != "" && info.Commit != "":
		return ChannelBranch
	}
	return ChannelNone
}

// compareVersions compares MAJOR.MINOR.PATCH strings with an optional "v".
// Unparseable versions compare as equal so they never claim an update.
func compareVersions(a, b string) int {
	ma, mb := semverRe.FindStringSubmatch(a), semverRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return 0
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}
