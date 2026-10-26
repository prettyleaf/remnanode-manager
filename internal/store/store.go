package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/prettyleaf/remnanode-manager/internal/mihomo"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB

	mu       sync.RWMutex
	settings settings.Settings
}

const schema = `
CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admins (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash   TEXT PRIMARY KEY,
	admin_id     INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	ip           TEXT NOT NULL DEFAULT '',
	user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS node_state (
	uuid         TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	status       TEXT NOT NULL,
	down_since   INTEGER,
	up_since     INTEGER,
	last_message TEXT NOT NULL DEFAULT '',
	notified     INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS managed_hosts (
	uuid        TEXT PRIMARY KEY,
	remark      TEXT NOT NULL,
	nodes       TEXT NOT NULL,
	state       TEXT NOT NULL,
	reason      TEXT NOT NULL DEFAULT '',
	disabled_at INTEGER NOT NULL,
	mihomo      TEXT NOT NULL DEFAULT '[]',
	updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	ts        INTEGER NOT NULL,
	level     TEXT NOT NULL,
	type      TEXT NOT NULL,
	message   TEXT NOT NULL,
	node_uuid TEXT NOT NULL DEFAULT '',
	host_uuid TEXT NOT NULL DEFAULT '',
	meta      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions(expires_at);
`

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.Join(dataDir, "manager.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s := &Store{db: db}
	if err := s.loadSettings(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- settings ----

func (s *Store) loadSettings() error {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = 'settings'`).Scan(&raw)
	cfg := settings.Default()
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("decode settings: %w", err)
		}
	}
	cfg.FillDefaults()
	s.mu.Lock()
	s.settings = cfg
	s.mu.Unlock()
	return nil
}

func (s *Store) Settings() settings.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.settings)
}

func (s *Store) SaveSettings(ctx context.Context, cfg settings.Settings) error {
	cfg.Normalize()
	cfg.FillDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO kv(key, value) VALUES('settings', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(raw)); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = cfg
	s.mu.Unlock()
	return nil
}

func clone(cfg settings.Settings) settings.Settings {
	raw, _ := json.Marshal(cfg)
	var out settings.Settings
	_ = json.Unmarshal(raw, &out)
	out.FillDefaults()
	return out
}

// ---- admins & sessions ----

type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
}

func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Store) AdminByUsername(ctx context.Context, username string) (*Admin, error) {
	return s.scanAdmin(s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM admins WHERE username = ?`, username))
}

func (s *Store) AdminByID(ctx context.Context, id int64) (*Admin, error) {
	return s.scanAdmin(s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM admins WHERE id = ?`, id))
}

func (s *Store) scanAdmin(row *sql.Row) (*Admin, error) {
	var a Admin
	if err := row.Scan(&a.ID, &a.Username, &a.PasswordHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) CreateAdmin(ctx context.Context, username, hash string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO admins(username, password_hash, created_at, updated_at) VALUES(?, ?, ?, ?)`,
		username, hash, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateFirstAdmin inserts an admin only when none exist yet, atomically.
func (s *Store) CreateFirstAdmin(ctx context.Context, username, hash string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO admins(username, password_hash, created_at, updated_at)
		SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM admins)`, username, hash, now, now)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, errors.New("admin already exists")
	}
	return res.LastInsertId()
}

func (s *Store) UpdateAdmin(ctx context.Context, id int64, username, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admins SET username = ?, password_hash = ?, updated_at = ? WHERE id = ?`,
		username, hash, time.Now().Unix(), id)
	return err
}

type Session struct {
	AdminID   int64
	ExpiresAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, adminID int64, ttl time.Duration, ip, ua string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, admin_id, created_at, expires_at, last_seen_at, ip, user_agent)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, tokenHash, adminID, now.Unix(), now.Add(ttl).Unix(), now.Unix(), ip, truncate(ua, 255))
	return err
}

func (s *Store) Session(ctx context.Context, tokenHash string) (*Session, error) {
	var sess Session
	var exp int64
	err := s.db.QueryRowContext(ctx, `SELECT admin_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&sess.AdminID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt = time.Unix(exp, 0)
	if time.Now().After(sess.ExpiresAt) {
		_ = s.DeleteSession(ctx, tokenHash)
		return nil, ErrNotFound
	}
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
		now.Unix(), now.Add(ttl).Unix(), tokenHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteAdminSessions(ctx context.Context, adminID int64, except string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE admin_id = ? AND token_hash != ?`, adminID, except)
	return err
}

// ---- node state ----

type NodeState struct {
	UUID        string     `json:"uuid"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	DownSince   *time.Time `json:"downSince"`
	UpSince     *time.Time `json:"upSince"`
	LastMessage string     `json:"lastMessage"`
	Notified    bool       `json:"-"`
}

func (s *Store) NodeStates(ctx context.Context) (map[string]*NodeState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uuid, name, status, down_since, up_since, last_message, notified FROM node_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*NodeState{}
	for rows.Next() {
		var n NodeState
		var down, up sql.NullInt64
		if err := rows.Scan(&n.UUID, &n.Name, &n.Status, &down, &up, &n.LastMessage, &n.Notified); err != nil {
			return nil, err
		}
		n.DownSince = fromNull(down)
		n.UpSince = fromNull(up)
		out[n.UUID] = &n
	}
	return out, rows.Err()
}

func (s *Store) SaveNodeStates(ctx context.Context, states map[string]*NodeState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_state`); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, n := range states {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_state(uuid, name, status, down_since, up_since, last_message, notified, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, n.UUID, n.Name, n.Status, toNull(n.DownSince), toNull(n.UpSince), n.LastMessage, n.Notified, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- managed hosts ----

const (
	HostStateDisabled   = "disabled"
	HostStateDryRun     = "dry_run"
	HostStateOverridden = "overridden"
)

// Why a host was disabled. "nodes down" predates the constant and is kept
// so existing records keep their meaning.
const (
	ReasonNodesDown = "nodes down"
	ReasonTraffic   = "traffic"
)

type TemplatePatch struct {
	TemplateUUID string       `json:"templateUuid"`
	TemplateName string       `json:"templateName"`
	Patch        mihomo.Patch `json:"patch"`
}

type ManagedHost struct {
	UUID       string          `json:"uuid"`
	Remark     string          `json:"remark"`
	Nodes      []string        `json:"nodes"`
	State      string          `json:"state"`
	Reason     string          `json:"reason"`
	DisabledAt time.Time       `json:"disabledAt"`
	Mihomo     []TemplatePatch `json:"mihomo"`
}

func (s *Store) ManagedHosts(ctx context.Context) (map[string]*ManagedHost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT uuid, remark, nodes, state, reason, disabled_at, mihomo FROM managed_hosts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ManagedHost{}
	for rows.Next() {
		var h ManagedHost
		var nodes, patches string
		var disabledAt int64
		if err := rows.Scan(&h.UUID, &h.Remark, &nodes, &h.State, &h.Reason, &disabledAt, &patches); err != nil {
			return nil, err
		}
		h.DisabledAt = time.Unix(disabledAt, 0)
		_ = json.Unmarshal([]byte(nodes), &h.Nodes)
		_ = json.Unmarshal([]byte(patches), &h.Mihomo)
		if h.Nodes == nil {
			h.Nodes = []string{}
		}
		if h.Mihomo == nil {
			h.Mihomo = []TemplatePatch{}
		}
		out[h.UUID] = &h
	}
	return out, rows.Err()
}

func (s *Store) ManagedHost(ctx context.Context, uuid string) (*ManagedHost, error) {
	all, err := s.ManagedHosts(ctx)
	if err != nil {
		return nil, err
	}
	h, ok := all[uuid]
	if !ok {
		return nil, ErrNotFound
	}
	return h, nil
}

func (s *Store) SaveManagedHost(ctx context.Context, h *ManagedHost) error {
	nodes, _ := json.Marshal(h.Nodes)
	patches, _ := json.Marshal(h.Mihomo)
	_, err := s.db.ExecContext(ctx, `INSERT INTO managed_hosts(uuid, remark, nodes, state, reason, disabled_at, mihomo, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET remark = excluded.remark, nodes = excluded.nodes, state = excluded.state,
			reason = excluded.reason, disabled_at = excluded.disabled_at, mihomo = excluded.mihomo, updated_at = excluded.updated_at`,
		h.UUID, h.Remark, string(nodes), h.State, h.Reason, h.DisabledAt.Unix(), string(patches), time.Now().Unix())
	return err
}

func (s *Store) DeleteManagedHost(ctx context.Context, uuid string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM managed_hosts WHERE uuid = ?`, uuid)
	return err
}

// ---- events ----

type Event struct {
	ID       int64          `json:"id"`
	Time     time.Time      `json:"time"`
	Level    string         `json:"level"`
	Type     string         `json:"type"`
	Message  string         `json:"message"`
	NodeUUID string         `json:"nodeUuid,omitempty"`
	HostUUID string         `json:"hostUuid,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

func (s *Store) AddEvent(ctx context.Context, e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	meta, _ := json.Marshal(e.Meta)
	if e.Meta == nil {
		meta = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(ts, level, type, message, node_uuid, host_uuid, meta) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		e.Time.UnixMilli(), e.Level, e.Type, e.Message, e.NodeUUID, e.HostUUID, string(meta))
	return err
}

type EventFilter struct {
	Type   string
	Level  string
	Search string
	Before int64
	Limit  int
}

func (s *Store) Events(ctx context.Context, f EventFilter) ([]Event, error) {
	q := `SELECT id, ts, level, type, message, node_uuid, host_uuid, meta FROM events WHERE 1=1`
	var args []any
	if f.Type != "" {
		q += ` AND type = ?`
		args = append(args, f.Type)
	}
	if f.Level != "" {
		q += ` AND level = ?`
		args = append(args, f.Level)
	}
	if f.Search != "" {
		q += ` AND message LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ts int64
		var meta string
		if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Type, &e.Message, &e.NodeUUID, &e.HostUUID, &meta); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts)
		_ = json.Unmarshal([]byte(meta), &e.Meta)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Prune(ctx context.Context, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func toNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func fromNull(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

func (s *Store) DeleteAdmins(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM admins`)
	return err
}
