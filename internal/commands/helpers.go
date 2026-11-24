// Package commands implements the bot's commands.
package commands

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

// All registers every command on the bot.
func All(b *bot.Bot) {
	b.Register(accountCommands()...)
	b.Register(playCommands()...)
	b.Register(artistCommands()...)
	b.Register(albumCommands()...)
	b.Register(trackCommands()...)
	b.Register(genreCommands()...)
	b.Register(friendCommands()...)
	b.Register(crownCommands()...)
	b.Register(serverCommands()...)
	b.Register(gameCommands(b)...)
}

// Common option definitions.
var (
	optUser    = bot.Option{Name: "user", Description: "Another user: mention, Discord ID or Sonosphere username (sp:name)", Kind: bot.OptUser}
	optPeriod  = bot.Option{Name: "time-period", Description: "Time period", Kind: bot.OptPeriod}
	optArtist  = bot.Option{Name: "artist", Description: "Artist name (defaults to what you're listening to)", Kind: bot.OptQuery}
	optAlbum   = bot.Option{Name: "album", Description: "Artist - Album (defaults to what you're listening to)", Kind: bot.OptQuery}
	optTrack   = bot.Option{Name: "track", Description: "Artist - Track (defaults to what you're listening to)", Kind: bot.OptQuery}
	optSize    = bot.Option{Name: "size", Description: "Chart size, e.g. 3x3 (max 10x10)", Kind: bot.OptSize}
	optPrivate = bot.Option{Name: "private", Description: "Only show the response to you", Kind: bot.OptFlag}
)

// lastTrack returns the user's current or most recent track.
func lastTrack(c *bot.Ctx, username string) (*sonosphere.RecentTrack, error) {
	r, err := c.B.API.RecentTracks(c, username, sonosphere.RecentOpts{Limit: 1, NoCache: true})
	if err != nil {
		return nil, err
	}
	if len(r.Track) == 0 {
		return nil, bot.Errorf("**%s** hasn't scrobbled anything yet.", bot.Esc(username))
	}
	return &r.Track[0], nil
}

// splitPair splits "Artist - Title" (also accepts " | " and " — ").
func splitPair(q string) (string, string, bool) {
	for _, sep := range []string{" | ", " - ", " – ", " — "} {
		if a, t, ok := strings.Cut(q, sep); ok && strings.TrimSpace(a) != "" && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(a), strings.TrimSpace(t), true
		}
	}
	return "", "", false
}

// contextUsername is the Sonosphere name used for "current track" lookups.
func contextUsername(c *bot.Ctx) (string, error) {
	u, err := c.Self()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// resolveArtist returns the searched artist or the artist of the invoker's current track.
func resolveArtist(c *bot.Ctx, opt string, fallbackUser string) (string, error) {
	if q := c.Str(opt); q != "" {
		return q, nil
	}
	if fallbackUser == "" {
		u, err := contextUsername(c)
		if err != nil {
			return "", err
		}
		fallbackUser = u
	}
	t, err := lastTrack(c, fallbackUser)
	if err != nil {
		return "", err
	}
	return t.Artist.String(), nil
}

// resolveAlbum returns artist and album from "Artist - Album", a search, or the current track.
func resolveAlbum(c *bot.Ctx, opt string, fallbackUser string) (string, string, error) {
	if q := c.Str(opt); q != "" {
		if a, b, ok := splitPair(q); ok {
			return a, b, nil
		}
		res, err := c.B.API.SearchAlbum(c, q, 1)
		if err != nil {
			return "", "", err
		}
		if len(res) == 0 {
			return "", "", bot.Errorf("Couldn't find an album matching **%s**. Try `Artist - Album`.", bot.Esc(q))
		}
		return res[0].Artist.String(), res[0].Name, nil
	}
	if fallbackUser == "" {
		u, err := contextUsername(c)
		if err != nil {
			return "", "", err
		}
		fallbackUser = u
	}
	t, err := lastTrack(c, fallbackUser)
	if err != nil {
		return "", "", err
	}
	if t.Album.String() == "" {
		return "", "", bot.Errorf("Your current track has no album. Try searching with `Artist - Album`.")
	}
	return t.Artist.String(), t.Album.String(), nil
}

// resolveTrack returns artist and track from "Artist - Track", a search, or the current track.
func resolveTrack(c *bot.Ctx, opt string, fallbackUser string) (string, string, error) {
	if q := c.Str(opt); q != "" {
		if a, b, ok := splitPair(q); ok {
			return a, b, nil
		}
		res, err := c.B.API.SearchTrack(c, q, 1)
		if err != nil {
			return "", "", err
		}
		if len(res) == 0 {
			return "", "", bot.Errorf("Couldn't find a track matching **%s**. Try `Artist - Track`.", bot.Esc(q))
		}
		return res[0].Artist.String(), res[0].Name, nil
	}
	if fallbackUser == "" {
		u, err := contextUsername(c)
		if err != nil {
			return "", "", err
		}
		fallbackUser = u
	}
	t, err := lastTrack(c, fallbackUser)
	if err != nil {
		return "", "", err
	}
	return t.Artist.String(), t.Name, nil
}

// fallbackFor picks the user whose current track is used when no query is given:
// the explicit target if one was given, otherwise the invoker.
func fallbackFor(c *bot.Ctx) string {
	if c.Has("user") {
		if t, err := c.Target(); err == nil {
			return t.Username
		}
	}
	return ""
}

func tagList(tags []sonosphere.Tag, n int, b *bot.Bot) string {
	var parts []string
	for i, t := range tags {
		if i >= n {
			break
		}
		parts = append(parts, bot.Link(strings.ToLower(t.Name), b.TagURL(t.Name)))
	}
	return strings.Join(parts, " · ")
}

// stripHTML removes tags and the trailing "Read more" link from bios.
func stripHTML(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if i := strings.Index(out, "Read more on"); i > 0 {
		out = strings.TrimSpace(out[:i])
	}
	return out
}

// ---------------- WhoKnows ----------------

type wkKind int

const (
	wkArtist wkKind = iota
	wkAlbum
	wkTrack
)

type wkMode int

const (
	modeServer wkMode = iota
	modeGlobal
	modeFriends
)

// whoKnows runs a WhoKnows query and renders the paginated result.
func whoKnows(c *bot.Ctx, kind wkKind, mode wkMode) error {
	self, err := c.Self()
	if err != nil {
		return err
	}
	var artist, item, title, url, thumb string
	var selfPlays int
	switch kind {
	case wkArtist:
		a, err := resolveArtist(c, "artist", "")
		if err != nil {
			return err
		}
		info, err := c.B.API.ArtistInfo(c, a, self.Username)
		if err != nil {
			return err
		}
		artist, title, url, thumb, selfPlays = info.Name, info.Name, c.B.ArtistURL(info.Name), info.Image.Best(), info.UserPlays()
		_ = c.B.Store.SetArtistPlays(c, self.DiscordID, artist, selfPlays)
	case wkAlbum:
		a, al, err := resolveAlbum(c, "album", "")
		if err != nil {
			return err
		}
		info, err := c.B.API.AlbumInfo(c, a, al, self.Username)
		if err != nil {
			return err
		}
		artist, item, selfPlays = info.Artist.String(), info.Name, info.UserPlays()
		title, url, thumb = item+" by "+artist, c.B.AlbumURL(artist, item), info.Image.Best()
		_ = c.B.Store.SetAlbumPlays(c, self.DiscordID, artist, item, thumb, selfPlays)
	case wkTrack:
		a, t, err := resolveTrack(c, "track", "")
		if err != nil {
			return err
		}
		info, err := c.B.API.TrackInfo(c, a, t, self.Username)
		if err != nil {
			return err
		}
		artist, item, selfPlays = info.Artist.String(), info.Name, info.UserPlays()
		title, url = item+" by "+artist, c.B.TrackURL(artist, item)
		if info.Album != nil {
			thumb = info.Album.Image.Best()
		}
		_ = c.B.Store.SetTrackPlays(c, self.DiscordID, artist, item, selfPlays)
	}

	var scope store.Scope
	var where string
	switch mode {
	case modeServer:
		scope.GuildID = int64(c.GuildID)
		where = "in this server"
		if g, ok := c.B.Client.Caches.Guild(c.GuildID); ok {
			where = "in " + g.Name
		}
	case modeGlobal:
		scope.Global = true
		where = "globally"
	case modeFriends:
		// Friends may not use the bot, so their plays are fetched live.
		names, err := c.B.Store.Friends(c, self.DiscordID)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return bot.Errorf("You haven't added any friends yet. Use %s to add some.", c.Cmdf("addfriends"))
		}
		return friendsWhoKnow(c, kind, self, names, artist, item, title, url, thumb, selfPlays)
	}

	var rows []store.WhoKnowsRow
	switch kind {
	case wkArtist:
		rows, err = c.B.Store.WhoKnowsArtist(c, artist, scope, 500)
	case wkAlbum:
		rows, err = c.B.Store.WhoKnowsAlbum(c, artist, item, scope, 500)
	case wkTrack:
		rows, err = c.B.Store.WhoKnowsTrack(c, artist, item, scope, 500)
	}
	if err != nil {
		return err
	}

	// Crowns (server artist WhoKnows only).
	var crownNote string
	var crownHolder int64
	if kind == wkArtist && mode == modeServer && len(rows) > 0 {
		g, err := c.B.Store.Guild(c, int64(c.GuildID))
		if err == nil && g.CrownsEnabled {
			top := firstEligible(c, rows)
			if top != nil {
				res, err := c.B.Store.EvaluateCrown(c, int64(c.GuildID), artist, *top, g.CrownThreshold)
				if err == nil && res.Holder != nil {
					crownHolder = res.Holder.UserID
					if res.Claimed && res.PrevHolder != 0 {
						crownNote = fmt.Sprintf("👑 Crown stolen by **%s**!", bot.Esc(nameOf(rows, res.Holder.UserID)))
					} else if res.Claimed {
						crownNote = fmt.Sprintf("👑 Crown claimed by **%s**!", bot.Esc(nameOf(rows, res.Holder.UserID)))
					}
				}
			}
		}
	}

	total, listeners := 0, len(rows)
	var lines []string
	selfListed := false
	for i, r := range rows {
		total += r.Playcount
		name := r.DisplayName
		if name == "" {
			name = r.Username
		}
		if mode == modeGlobal {
			name = r.Username
		}
		line := fmt.Sprintf("%s %s - **%s**", bot.Medal(i), bot.Link(bot.Trunc(name, 32), c.B.API.UserURL(r.Username)), bot.Plural(r.Playcount, "play"))
		if crownHolder != 0 && r.UserID == crownHolder {
			line = "👑 " + line
		}
		if r.UserID == self.DiscordID {
			line += " ← you"
			selfListed = true
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		lines = []string{"Nobody " + where + " has listened to this yet."}
		if mode == modeServer {
			lines[0] += fmt.Sprintf("\n\n-# Only members who used the bot here are counted. Admins can run %s.", c.Cmdf("refreshmembers"))
		}
	}

	label := map[wkKind]string{wkArtist: "", wkAlbum: "album ", wkTrack: "track "}[kind]
	tmpl := discord.NewEmbed().WithTitle(bot.Trunc(fmt.Sprintf("Who knows %s%s %s", label, title, where), 256)).WithURL(url).WithColor(bot.Accent)
	if thumb != "" {
		tmpl = tmpl.WithThumbnail(thumb)
	}
	footer := fmt.Sprintf("%s · %s · avg %s plays", bot.Plural(listeners, "listener"), bot.Plural(total, "play"), bot.N(total/max(listeners, 1)))
	if !selfListed && selfPlays > 0 {
		footer = fmt.Sprintf("You: %s · ", bot.Plural(selfPlays, "play")) + footer
	}
	tmpl = tmpl.WithFooterText(footer)
	if crownNote != "" {
		tmpl = tmpl.WithDescription(crownNote)
	}
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

func firstEligible(c *bot.Ctx, rows []store.WhoKnowsRow) *store.WhoKnowsRow {
	members, err := c.B.Store.GuildMembers(c, int64(c.GuildID))
	if err != nil {
		return nil
	}
	blocked := map[int64]bool{}
	for _, m := range members {
		if m.CrownBlocked {
			blocked[m.UserID] = true
		}
	}
	for i := range rows {
		if !blocked[rows[i].UserID] {
			return &rows[i]
		}
	}
	return nil
}

func nameOf(rows []store.WhoKnowsRow, id int64) string {
	for _, r := range rows {
		if r.UserID == id {
			if r.DisplayName != "" {
				return r.DisplayName
			}
			return r.Username
		}
	}
	return "someone"
}

// friendsWhoKnow fetches live play counts for each friend.
func friendsWhoKnow(c *bot.Ctx, kind wkKind, self *store.User, friends []string, artist, item, title, url, thumb string, selfPlays int) error {
	type row struct {
		name  string
		plays int
	}
	results := make([]row, len(friends))
	done := make(chan struct{}, len(friends))
	for i, f := range friends {
		go func(i int, f string) {
			defer func() { done <- struct{}{} }()
			plays := 0
			switch kind {
			case wkArtist:
				if info, err := c.B.API.ArtistInfo(c, artist, f); err == nil {
					plays = info.UserPlays()
				}
			case wkAlbum:
				if info, err := c.B.API.AlbumInfo(c, artist, item, f); err == nil {
					plays = info.UserPlays()
				}
			case wkTrack:
				if info, err := c.B.API.TrackInfo(c, artist, item, f); err == nil {
					plays = info.UserPlays()
				}
			}
			results[i] = row{f, plays}
		}(i, f)
	}
	for range friends {
		<-done
	}
	results = append(results, row{self.Username, selfPlays})
	// sort by plays desc
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].plays > results[j-1].plays; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	var lines []string
	total, listeners := 0, 0
	for _, r := range results {
		if r.plays == 0 {
			continue
		}
		line := fmt.Sprintf("%s %s - **%s**", bot.Medal(listeners), bot.Link(r.name, c.B.API.UserURL(r.name)), bot.Plural(r.plays, "play"))
		if strings.EqualFold(r.name, self.Username) {
			line += " ← you"
		}
		lines = append(lines, line)
		total += r.plays
		listeners++
	}
	if len(lines) == 0 {
		lines = []string{"None of your friends have listened to this yet."}
	}
	label := map[wkKind]string{wkArtist: "", wkAlbum: "album ", wkTrack: "track "}[kind]
	tmpl := discord.NewEmbed().WithTitle(bot.Trunc(fmt.Sprintf("Who knows %s%s among your friends", label, title), 256)).WithURL(url).
		WithFooterText(fmt.Sprintf("%s · %s", bot.Plural(listeners, "listener"), bot.Plural(total, "play")))
	if thumb != "" {
		tmpl = tmpl.WithThumbnail(thumb)
	}
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

// topLines renders "1. Name - 12 plays" lines.
func numbered(i int, text string, plays int) string {
	return fmt.Sprintf("%d. %s - *%s*", i+1, text, bot.Plural(plays, "play"))
}
