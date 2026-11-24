// Package store persists bot state in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	discord_id    INTEGER PRIMARY KEY,
	username      TEXT NOT NULL,
	session_key   TEXT NOT NULL DEFAULT '',
	fm_mode       TEXT NOT NULL DEFAULT 'embedmini',
	timezone      TEXT NOT NULL DEFAULT 'UTC',
	reactions     TEXT NOT NULL DEFAULT '',
	last_indexed  INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS users_username ON users(username COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS guilds (
	guild_id         INTEGER PRIMARY KEY,
	prefix           TEXT NOT NULL DEFAULT '',
	crowns_enabled   INTEGER NOT NULL DEFAULT 1,
	crown_threshold  INTEGER NOT NULL DEFAULT 30,
	reactions        TEXT NOT NULL DEFAULT '',
	disabled_cmds    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS guild_users (
	guild_id       INTEGER NOT NULL,
	user_id        INTEGER NOT NULL,
	display_name   TEXT NOT NULL DEFAULT '',
	blocked        INTEGER NOT NULL DEFAULT 0,
	crown_blocked  INTEGER NOT NULL DEFAULT 0,
	last_seen      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (guild_id, user_id)
);
CREATE INDEX IF NOT EXISTS guild_users_user ON guild_users(user_id);

CREATE TABLE IF NOT EXISTS user_artists (
	user_id    INTEGER NOT NULL,
	name       TEXT NOT NULL,
	name_key   TEXT NOT NULL,
	playcount  INTEGER NOT NULL,
	PRIMARY KEY (user_id, name_key)
);
CREATE INDEX IF NOT EXISTS user_artists_key ON user_artists(name_key);

CREATE TABLE IF NOT EXISTS user_albums (
	user_id    INTEGER NOT NULL,
	artist     TEXT NOT NULL,
	name       TEXT NOT NULL,
	item_key   TEXT NOT NULL,
	artist_key TEXT NOT NULL,
	playcount  INTEGER NOT NULL,
	image      TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (user_id, item_key)
);
CREATE INDEX IF NOT EXISTS user_albums_key ON user_albums(item_key);
CREATE INDEX IF NOT EXISTS user_albums_artist ON user_albums(user_id, artist_key);

CREATE TABLE IF NOT EXISTS user_tracks (
	user_id    INTEGER NOT NULL,
	artist     TEXT NOT NULL,
	name       TEXT NOT NULL,
	item_key   TEXT NOT NULL,
	artist_key TEXT NOT NULL,
	playcount  INTEGER NOT NULL,
	PRIMARY KEY (user_id, item_key)
);
CREATE INDEX IF NOT EXISTS user_tracks_key ON user_tracks(item_key);
CREATE INDEX IF NOT EXISTS user_tracks_artist ON user_tracks(user_id, artist_key);

CREATE TABLE IF NOT EXISTS friends (
	user_id   INTEGER NOT NULL,
	username  TEXT NOT NULL COLLATE NOCASE,
	PRIMARY KEY (user_id, username)
);

CREATE TABLE IF NOT EXISTS crowns (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id    INTEGER NOT NULL,
	artist_key  TEXT NOT NULL,
	artist      TEXT NOT NULL,
	user_id     INTEGER NOT NULL,
	playcount   INTEGER NOT NULL,
	claimed_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL,
	active      INTEGER NOT NULL DEFAULT 1,
	stolen_by   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS crowns_active ON crowns(guild_id, artist_key, active);
CREATE INDEX IF NOT EXISTS crowns_user ON crowns(guild_id, user_id, active);

CREATE TABLE IF NOT EXISTS shortcuts (
	user_id  INTEGER NOT NULL,
	input    TEXT NOT NULL,
	output   TEXT NOT NULL,
	PRIMARY KEY (user_id, input)
);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: a single writer avoids SQLITE_BUSY storms
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Key normalises a name for case-insensitive matching.
func Key(parts ...string) string {
	for i, p := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(p))
	}
	return strings.Join(parts, "\x1f")
}

// ---------------- users ----------------

type User struct {
	DiscordID   int64
	Username    string
	SessionKey  string
	FmMode      string
	Timezone    string
	Reactions   string
	LastIndexed time.Time
}

var ErrNotFound = errors.New("not found")

const userCols = `discord_id, username, session_key, fm_mode, timezone, reactions, last_indexed`

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	var u User
	var li int64
	if err := r.Scan(&u.DiscordID, &u.Username, &u.SessionKey, &u.FmMode, &u.Timezone, &u.Reactions, &li); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.LastIndexed = time.Unix(li, 0)
	return &u, nil
}

func (s *Store) User(ctx context.Context, discordID int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE discord_id = ?`, discordID))
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE LIMIT 1`, username))
}

func (s *Store) UpsertUser(ctx context.Context, discordID int64, username, sessionKey string) (changed bool, err error) {
	old, err := s.User(ctx, discordID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	changed = old == nil || !strings.EqualFold(old.Username, username)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO users (discord_id, username, session_key, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(discord_id) DO UPDATE SET username = excluded.username, session_key = excluded.session_key`,
		discordID, username, sessionKey, time.Now().Unix())
	if err == nil && changed && old != nil {
		// Account switched: drop the old index and crowns.
		err = s.ClearUserIndex(ctx, discordID)
		if err == nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE crowns SET active = 0 WHERE user_id = ? AND active = 1`, discordID)
		}
		if err == nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE users SET last_indexed = 0 WHERE discord_id = ?`, discordID)
		}
	}
	return changed, err
}

func (s *Store) SetUserField(ctx context.Context, discordID int64, field, value string) error {
	switch field {
	case "fm_mode", "timezone", "reactions":
	default:
		return errors.New("invalid field")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET `+field+` = ? WHERE discord_id = ?`, value, discordID)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, discordID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM users WHERE discord_id = ?`,
		`DELETE FROM guild_users WHERE user_id = ?`,
		`DELETE FROM user_artists WHERE user_id = ?`,
		`DELETE FROM user_albums WHERE user_id = ?`,
		`DELETE FROM user_tracks WHERE user_id = ?`,
		`DELETE FROM friends WHERE user_id = ?`,
		`DELETE FROM crowns WHERE user_id = ?`,
		`DELETE FROM shortcuts WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, discordID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// StaleUsers returns users whose index is older than maxAge, oldest first.
func (s *Store) StaleUsers(ctx context.Context, maxAge time.Duration, limit int) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE last_indexed < ? ORDER BY last_indexed LIMIT ?`,
		time.Now().Add(-maxAge).Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// ---------------- guilds ----------------

type Guild struct {
	ID             int64
	Prefix         string
	CrownsEnabled  bool
	CrownThreshold int
	Reactions      string
	DisabledCmds   []string
}

func (s *Store) Guild(ctx context.Context, guildID int64) (*Guild, error) {
	g := &Guild{ID: guildID, CrownsEnabled: true, CrownThreshold: 30}
	var disabled string
	err := s.DB.QueryRowContext(ctx, `SELECT prefix, crowns_enabled, crown_threshold, reactions, disabled_cmds FROM guilds WHERE guild_id = ?`, guildID).
		Scan(&g.Prefix, &g.CrownsEnabled, &g.CrownThreshold, &g.Reactions, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil
	}
	if disabled != "" {
		g.DisabledCmds = strings.Split(disabled, ",")
	}
	return g, err
}

func (s *Store) SetGuildField(ctx context.Context, guildID int64, field string, value any) error {
	switch field {
	case "prefix", "crowns_enabled", "crown_threshold", "reactions", "disabled_cmds":
	default:
		return errors.New("invalid field")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO guilds (guild_id) VALUES (?) ON CONFLICT(guild_id) DO NOTHING`, guildID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE guilds SET `+field+` = ? WHERE guild_id = ?`, value, guildID)
	return err
}

// ---------------- guild membership ----------------

func (s *Store) TouchGuildUser(ctx context.Context, guildID, userID int64, displayName string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO guild_users (guild_id, user_id, display_name, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT(guild_id, user_id) DO UPDATE SET display_name = excluded.display_name, last_seen = excluded.last_seen`,
		guildID, userID, displayName, time.Now().Unix())
	return err
}

func (s *Store) RemoveGuildUser(ctx context.Context, guildID, userID int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM guild_users WHERE guild_id = ? AND user_id = ?`, guildID, userID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE crowns SET active = 0 WHERE guild_id = ? AND user_id = ? AND active = 1`, guildID, userID)
	return err
}

// ReplaceGuildMembers sets the registered members of a guild to exactly the given set.
func (s *Store) ReplaceGuildMembers(ctx context.Context, guildID int64, members map[int64]string) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM guild_users WHERE guild_id = ?`, guildID)
	if err != nil {
		return 0, err
	}
	var existing []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		existing = append(existing, id)
	}
	rows.Close()
	for _, id := range existing {
		if _, ok := members[id]; !ok {
			tx.ExecContext(ctx, `DELETE FROM guild_users WHERE guild_id = ? AND user_id = ?`, guildID, id)
			tx.ExecContext(ctx, `UPDATE crowns SET active = 0 WHERE guild_id = ? AND user_id = ? AND active = 1`, guildID, id)
		}
	}
	n := 0
	now := time.Now().Unix()
	for id, name := range members {
		var exists int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE discord_id = ?`, id).Scan(&exists) != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO guild_users (guild_id, user_id, display_name, last_seen) VALUES (?, ?, ?, ?)
			ON CONFLICT(guild_id, user_id) DO UPDATE SET display_name = excluded.display_name`, guildID, id, name, now); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

func (s *Store) SetGuildUserFlag(ctx context.Context, guildID, userID int64, field string, v bool) error {
	if field != "blocked" && field != "crown_blocked" {
		return errors.New("invalid field")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO guild_users (guild_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, guildID, userID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE guild_users SET `+field+` = ? WHERE guild_id = ? AND user_id = ?`, v, guildID, userID)
	return err
}

type GuildMember struct {
	UserID       int64
	Username     string
	DisplayName  string
	Blocked      bool
	CrownBlocked bool
}

func (s *Store) GuildMembers(ctx context.Context, guildID int64) ([]GuildMember, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.user_id, u.username, g.display_name, g.blocked, g.crown_blocked
		FROM guild_users g JOIN users u ON u.discord_id = g.user_id WHERE g.guild_id = ? ORDER BY u.username COLLATE NOCASE`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuildMember
	for rows.Next() {
		var m GuildMember
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Blocked, &m.CrownBlocked); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------------- friends ----------------

func (s *Store) Friends(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT username FROM friends WHERE user_id = ? ORDER BY username`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) AddFriend(ctx context.Context, userID int64, username string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO friends (user_id, username) VALUES (?, ?) ON CONFLICT DO NOTHING`, userID, username)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) RemoveFriend(ctx context.Context, userID int64, username string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM friends WHERE user_id = ? AND username = ?`, userID, username)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) ClearFriends(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM friends WHERE user_id = ?`, userID)
	return err
}

// ---------------- shortcuts ----------------

func (s *Store) Shortcuts(ctx context.Context, userID int64) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT input, output FROM shortcuts WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var i, o string
		rows.Scan(&i, &o)
		out[i] = o
	}
	return out, rows.Err()
}

func (s *Store) SetShortcut(ctx context.Context, userID int64, input, output string) error {
	if output == "" {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM shortcuts WHERE user_id = ? AND input = ?`, userID, input)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO shortcuts (user_id, input, output) VALUES (?, ?, ?)
		ON CONFLICT(user_id, input) DO UPDATE SET output = excluded.output`, userID, input, output)
	return err
}
