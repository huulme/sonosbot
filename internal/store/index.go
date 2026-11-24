package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type IndexedArtist struct {
	Name      string
	Playcount int
}

type IndexedAlbum struct {
	Artist, Name, Image string
	Playcount           int
}

type IndexedTrack struct {
	Artist, Name string
	Playcount    int
}

func (s *Store) ClearUserIndex(ctx context.Context, userID int64) error {
	for _, t := range []string{"user_artists", "user_albums", "user_tracks"} {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id = ?`, userID); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceIndex atomically replaces a user's indexed top artists, albums and tracks.
func (s *Store) ReplaceIndex(ctx context.Context, userID int64, artists []IndexedArtist, albums []IndexedAlbum, tracks []IndexedTrack) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"user_artists", "user_albums", "user_tracks"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id = ?`, userID); err != nil {
			return err
		}
	}
	insA, err := tx.PrepareContext(ctx, `INSERT INTO user_artists (user_id, name, name_key, playcount) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, name_key) DO UPDATE SET playcount = playcount + excluded.playcount`)
	if err != nil {
		return err
	}
	for _, a := range artists {
		if _, err := insA.ExecContext(ctx, userID, a.Name, Key(a.Name), a.Playcount); err != nil {
			return err
		}
	}
	insB, err := tx.PrepareContext(ctx, `INSERT INTO user_albums (user_id, artist, name, item_key, artist_key, playcount, image) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_key) DO UPDATE SET playcount = playcount + excluded.playcount`)
	if err != nil {
		return err
	}
	for _, a := range albums {
		if _, err := insB.ExecContext(ctx, userID, a.Artist, a.Name, Key(a.Artist, a.Name), Key(a.Artist), a.Playcount, a.Image); err != nil {
			return err
		}
	}
	insT, err := tx.PrepareContext(ctx, `INSERT INTO user_tracks (user_id, artist, name, item_key, artist_key, playcount) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_key) DO UPDATE SET playcount = playcount + excluded.playcount`)
	if err != nil {
		return err
	}
	for _, t := range tracks {
		if _, err := insT.ExecContext(ctx, userID, t.Artist, t.Name, Key(t.Artist, t.Name), Key(t.Artist), t.Playcount); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET last_indexed = ? WHERE discord_id = ?`, time.Now().Unix(), userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetArtistPlays updates a single user's play count for an artist (live refresh).
func (s *Store) SetArtistPlays(ctx context.Context, userID int64, artist string, plays int) error {
	if plays <= 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM user_artists WHERE user_id = ? AND name_key = ?`, userID, Key(artist))
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_artists (user_id, name, name_key, playcount) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, name_key) DO UPDATE SET playcount = excluded.playcount, name = excluded.name`, userID, artist, Key(artist), plays)
	return err
}

func (s *Store) SetAlbumPlays(ctx context.Context, userID int64, artist, album, image string, plays int) error {
	if plays <= 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM user_albums WHERE user_id = ? AND item_key = ?`, userID, Key(artist, album))
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_albums (user_id, artist, name, item_key, artist_key, playcount, image) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_key) DO UPDATE SET playcount = excluded.playcount`, userID, artist, album, Key(artist, album), Key(artist), plays, image)
	return err
}

func (s *Store) SetTrackPlays(ctx context.Context, userID int64, artist, track string, plays int) error {
	if plays <= 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM user_tracks WHERE user_id = ? AND item_key = ?`, userID, Key(artist, track))
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_tracks (user_id, artist, name, item_key, artist_key, playcount) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_key) DO UPDATE SET playcount = excluded.playcount`, userID, artist, track, Key(artist, track), Key(artist), plays)
	return err
}

// WhoKnowsRow is one listener in a WhoKnows result.
type WhoKnowsRow struct {
	UserID      int64
	Username    string
	DisplayName string
	Playcount   int
}

// Scope selects which users a WhoKnows query covers.
type Scope struct {
	GuildID   int64   // >0: members of this guild (excluding blocked)
	UserIDs   []int64 // non-empty: only these discord users (friends)
	Usernames []string
	Global    bool
}

func (s *Store) whoKnows(ctx context.Context, table, keyCol, key string, scope Scope, limit int) ([]WhoKnowsRow, error) {
	var rows *sql.Rows
	var err error
	base := `SELECT u.discord_id, u.username, '', t.playcount FROM ` + table + ` t JOIN users u ON u.discord_id = t.user_id `
	switch {
	case scope.GuildID > 0:
		q := `SELECT u.discord_id, u.username, g.display_name, t.playcount FROM ` + table + ` t
			JOIN users u ON u.discord_id = t.user_id
			JOIN guild_users g ON g.user_id = t.user_id AND g.guild_id = ?
			WHERE t.` + keyCol + ` = ? AND g.blocked = 0 ORDER BY t.playcount DESC LIMIT ?`
		rows, err = s.DB.QueryContext(ctx, q, scope.GuildID, key, limit)
	case scope.Global:
		q := base + `WHERE t.` + keyCol + ` = ? ORDER BY t.playcount DESC LIMIT ?`
		rows, err = s.DB.QueryContext(ctx, q, key, limit)
	default:
		if len(scope.UserIDs) == 0 {
			return nil, nil
		}
		args := []any{key}
		ph := ""
		for i, id := range scope.UserIDs {
			if i > 0 {
				ph += ","
			}
			ph += "?"
			args = append(args, id)
		}
		args = append(args, limit)
		q := base + `WHERE t.` + keyCol + ` = ? AND t.user_id IN (` + ph + `) ORDER BY t.playcount DESC LIMIT ?`
		rows, err = s.DB.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WhoKnowsRow
	for rows.Next() {
		var r WhoKnowsRow
		if err := rows.Scan(&r.UserID, &r.Username, &r.DisplayName, &r.Playcount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) WhoKnowsArtist(ctx context.Context, artist string, scope Scope, limit int) ([]WhoKnowsRow, error) {
	return s.whoKnows(ctx, "user_artists", "name_key", Key(artist), scope, limit)
}

func (s *Store) WhoKnowsAlbum(ctx context.Context, artist, album string, scope Scope, limit int) ([]WhoKnowsRow, error) {
	return s.whoKnows(ctx, "user_albums", "item_key", Key(artist, album), scope, limit)
}

func (s *Store) WhoKnowsTrack(ctx context.Context, artist, track string, scope Scope, limit int) ([]WhoKnowsRow, error) {
	return s.whoKnows(ctx, "user_tracks", "item_key", Key(artist, track), scope, limit)
}

// UserArtists returns the indexed artists of a user ordered by plays.
func (s *Store) UserArtists(ctx context.Context, userID int64, limit int) ([]IndexedArtist, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name, playcount FROM user_artists WHERE user_id = ? ORDER BY playcount DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IndexedArtist
	for rows.Next() {
		var a IndexedArtist
		rows.Scan(&a.Name, &a.Playcount)
		out = append(out, a)
	}
	return out, rows.Err()
}

// UserArtistTracks returns a user's indexed tracks for an artist.
func (s *Store) UserArtistTracks(ctx context.Context, userID int64, artist string) ([]IndexedTrack, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT artist, name, playcount FROM user_tracks WHERE user_id = ? AND artist_key = ? ORDER BY playcount DESC`, userID, Key(artist))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IndexedTrack
	for rows.Next() {
		var t IndexedTrack
		rows.Scan(&t.Artist, &t.Name, &t.Playcount)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UserArtistAlbums returns a user's indexed albums for an artist.
func (s *Store) UserArtistAlbums(ctx context.Context, userID int64, artist string) ([]IndexedAlbum, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT artist, name, image, playcount FROM user_albums WHERE user_id = ? AND artist_key = ? ORDER BY playcount DESC`, userID, Key(artist))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IndexedAlbum
	for rows.Next() {
		var a IndexedAlbum
		rows.Scan(&a.Artist, &a.Name, &a.Image, &a.Playcount)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ServerTop aggregates indexed plays across a guild's members.
type ServerTopRow struct {
	Artist, Name string
	Playcount    int
	Listeners    int
}

func (s *Store) ServerTop(ctx context.Context, guildID int64, kind string, limit int) ([]ServerTopRow, error) {
	var q string
	switch kind {
	case "artists":
		q = `SELECT '', MAX(t.name), SUM(t.playcount) p, COUNT(*) FROM user_artists t
			JOIN guild_users g ON g.user_id = t.user_id AND g.guild_id = ? WHERE g.blocked = 0
			GROUP BY t.name_key ORDER BY p DESC LIMIT ?`
	case "albums":
		q = `SELECT MAX(t.artist), MAX(t.name), SUM(t.playcount) p, COUNT(*) FROM user_albums t
			JOIN guild_users g ON g.user_id = t.user_id AND g.guild_id = ? WHERE g.blocked = 0
			GROUP BY t.item_key ORDER BY p DESC LIMIT ?`
	case "tracks":
		q = `SELECT MAX(t.artist), MAX(t.name), SUM(t.playcount) p, COUNT(*) FROM user_tracks t
			JOIN guild_users g ON g.user_id = t.user_id AND g.guild_id = ? WHERE g.blocked = 0
			GROUP BY t.item_key ORDER BY p DESC LIMIT ?`
	default:
		return nil, errors.New("invalid kind")
	}
	rows, err := s.DB.QueryContext(ctx, q, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerTopRow
	for rows.Next() {
		var r ServerTopRow
		if err := rows.Scan(&r.Artist, &r.Name, &r.Playcount, &r.Listeners); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------- crowns ----------------

type Crown struct {
	ID        int64
	GuildID   int64
	Artist    string
	UserID    int64
	Username  string
	Playcount int
	ClaimedAt time.Time
	UpdatedAt time.Time
	Active    bool
	StolenBy  int64
}

const crownCols = `c.id, c.guild_id, c.artist, c.user_id, COALESCE(u.username, ''), c.playcount, c.claimed_at, c.updated_at, c.active, c.stolen_by`

func scanCrowns(rows *sql.Rows) ([]Crown, error) {
	defer rows.Close()
	var out []Crown
	for rows.Next() {
		var c Crown
		var ca, ua int64
		if err := rows.Scan(&c.ID, &c.GuildID, &c.Artist, &c.UserID, &c.Username, &c.Playcount, &ca, &ua, &c.Active, &c.StolenBy); err != nil {
			return nil, err
		}
		c.ClaimedAt, c.UpdatedAt = time.Unix(ca, 0), time.Unix(ua, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ActiveCrown(ctx context.Context, guildID int64, artist string) (*Crown, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+crownCols+` FROM crowns c LEFT JOIN users u ON u.discord_id = c.user_id
		WHERE c.guild_id = ? AND c.artist_key = ? AND c.active = 1 LIMIT 1`, guildID, Key(artist))
	if err != nil {
		return nil, err
	}
	cs, err := scanCrowns(rows)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

func (s *Store) CrownHistory(ctx context.Context, guildID int64, artist string, limit int) ([]Crown, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+crownCols+` FROM crowns c LEFT JOIN users u ON u.discord_id = c.user_id
		WHERE c.guild_id = ? AND c.artist_key = ? ORDER BY c.claimed_at DESC LIMIT ?`, guildID, Key(artist), limit)
	if err != nil {
		return nil, err
	}
	return scanCrowns(rows)
}

// UserCrowns lists a user's active crowns; order is "playcount" or "recent".
func (s *Store) UserCrowns(ctx context.Context, guildID, userID int64, order string) ([]Crown, error) {
	ob := "c.playcount DESC"
	if order == "recent" {
		ob = "c.claimed_at DESC"
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+crownCols+` FROM crowns c LEFT JOIN users u ON u.discord_id = c.user_id
		WHERE c.guild_id = ? AND c.user_id = ? AND c.active = 1 ORDER BY `+ob, guildID, userID)
	if err != nil {
		return nil, err
	}
	return scanCrowns(rows)
}

// StolenCrowns lists crowns taken from the user.
func (s *Store) StolenCrowns(ctx context.Context, guildID, userID int64) ([]Crown, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+crownCols+` FROM crowns c LEFT JOIN users u ON u.discord_id = c.stolen_by
		WHERE c.guild_id = ? AND c.user_id = ? AND c.active = 0 AND c.stolen_by != 0 ORDER BY c.updated_at DESC LIMIT 100`, guildID, userID)
	if err != nil {
		return nil, err
	}
	return scanCrowns(rows)
}

type CrownLeader struct {
	UserID   int64
	Username string
	Count    int
}

func (s *Store) CrownLeaderboard(ctx context.Context, guildID int64, limit int) ([]CrownLeader, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.user_id, u.username, COUNT(*) n FROM crowns c JOIN users u ON u.discord_id = c.user_id
		WHERE c.guild_id = ? AND c.active = 1 GROUP BY c.user_id ORDER BY n DESC LIMIT ?`, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CrownLeader
	for rows.Next() {
		var l CrownLeader
		rows.Scan(&l.UserID, &l.Username, &l.Count)
		out = append(out, l)
	}
	return out, rows.Err()
}

// CrownResult describes what happened when a crown was evaluated.
type CrownResult struct {
	Holder     *Crown
	Claimed    bool  // a new holder took the crown
	PrevHolder int64 // previous holder, if stolen
}

// EvaluateCrown makes sure the top listener holds the crown if they pass threshold.
func (s *Store) EvaluateCrown(ctx context.Context, guildID int64, artist string, top WhoKnowsRow, threshold int) (*CrownResult, error) {
	cur, err := s.ActiveCrown(ctx, guildID, artist)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res := &CrownResult{Holder: cur}
	if cur != nil && cur.UserID == top.UserID {
		if cur.Playcount != top.Playcount {
			s.DB.ExecContext(ctx, `UPDATE crowns SET playcount = ?, updated_at = ? WHERE id = ?`, top.Playcount, now, cur.ID)
			cur.Playcount = top.Playcount
		}
		return res, nil
	}
	if top.Playcount < threshold {
		return res, nil
	}
	if cur != nil {
		// Only steal when strictly ahead of the current holder.
		var holderPlays int
		err := s.DB.QueryRowContext(ctx, `SELECT playcount FROM user_artists WHERE user_id = ? AND name_key = ?`, cur.UserID, Key(artist)).Scan(&holderPlays)
		if err == nil && holderPlays >= top.Playcount {
			return res, nil
		}
		s.DB.ExecContext(ctx, `UPDATE crowns SET active = 0, stolen_by = ?, updated_at = ? WHERE id = ?`, top.UserID, now, cur.ID)
		res.PrevHolder = cur.UserID
	}
	r, err := s.DB.ExecContext(ctx, `INSERT INTO crowns (guild_id, artist_key, artist, user_id, playcount, claimed_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		guildID, Key(artist), artist, top.UserID, top.Playcount, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := r.LastInsertId()
	res.Claimed = true
	res.Holder = &Crown{ID: id, GuildID: guildID, Artist: artist, UserID: top.UserID, Username: top.Username, Playcount: top.Playcount,
		ClaimedAt: time.Unix(now, 0), UpdatedAt: time.Unix(now, 0), Active: true}
	return res, nil
}

func (s *Store) KillCrown(ctx context.Context, guildID int64, artist string) (int64, error) {
	r, err := s.DB.ExecContext(ctx, `DELETE FROM crowns WHERE guild_id = ? AND artist_key = ?`, guildID, Key(artist))
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Store) KillAllCrowns(ctx context.Context, guildID int64) (int64, error) {
	r, err := s.DB.ExecContext(ctx, `DELETE FROM crowns WHERE guild_id = ?`, guildID)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Store) RemoveUserCrowns(ctx context.Context, guildID, userID int64) (int64, error) {
	r, err := s.DB.ExecContext(ctx, `DELETE FROM crowns WHERE guild_id = ? AND user_id = ?`, guildID, userID)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
