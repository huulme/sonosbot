package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

func trackCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "track", Aliases: []string{"tr", "ti", "trackinfo"}, Category: "Tracks",
			Description: "Shows information about a track and your listening",
			Options:     []bot.Option{optTrack, optUser},
			Usage:       []string{"track", "track the beatles - yesterday"},
			Run:         trackCmd,
		},
		{
			Name: "trackplays", Aliases: []string{"tp"}, Category: "Tracks",
			Description: "Shows how many times you've listened to a track",
			Options:     []bot.Option{optTrack, optUser},
			Usage:       []string{"trackplays", "tp tame impala - let it happen"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				a, tr, err := resolveTrack(c, "track", t.Username)
				if err != nil {
					return err
				}
				info, err := c.B.API.TrackInfo(c, a, tr, t.Username)
				if err != nil {
					return err
				}
				if t.User != nil {
					_ = c.B.Store.SetTrackPlays(c, t.User.DiscordID, info.Artist.String(), info.Name, info.UserPlays())
				}
				return c.Say("**%s** has **%s** for **%s** by **%s**.", bot.Esc(t.DisplayName), bot.Plural(info.UserPlays(), "play"),
					bot.Link(info.Name, c.B.TrackURL(info.Artist.String(), info.Name)), bot.Esc(info.Artist.String()))
			},
		},
		{
			Name: "toptracks", Aliases: []string{"tt", "tracks"}, Category: "Tracks",
			Description: "Shows your top tracks for a time period",
			Options:     []bot.Option{optPeriod, optUser},
			Usage:       []string{"toptracks", "tt monthly"},
			Run:         topTracksCmd,
		},
		{
			Name: "whoknowstrack", Aliases: []string{"wktr", "wt", "wktrack"}, Category: "Tracks", GuildOnly: true,
			Description: "Shows who in this server listens to a track",
			Options:     []bot.Option{optTrack},
			Usage:       []string{"wt", "wt kanye west - runaway"},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkTrack, modeServer) },
		},
		{
			Name: "friendwhoknowstrack", Aliases: []string{"fwkt", "fwt", "fwktrack"}, Category: "Friends",
			Description: "Shows who of your friends listens to a track",
			Options:     []bot.Option{optTrack},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkTrack, modeFriends) },
		},
		{
			Name: "globalwhoknowstrack", Aliases: []string{"gwkt", "gwt", "gwktrack"}, Category: "Tracks",
			Description: "Shows who of all bot users listens to a track",
			Options:     []bot.Option{optTrack},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkTrack, modeGlobal) },
		},
		{
			Name: "servertracks", Aliases: []string{"st", "stt"}, Category: "Tracks", GuildOnly: true,
			Description: "Shows the top tracks of this server",
			Run:         func(c *bot.Ctx) error { return serverTop(c, "tracks") },
		},
		{
			Name: "love", Aliases: []string{"l", "heart"}, Category: "Tracks",
			Description: "Loves a track on Sonosphere (defaults to your current track)",
			Options:     []bot.Option{optTrack},
			Usage:       []string{"love", "love daft punk - one more time"},
			Run:         func(c *bot.Ctx) error { return loveCmd(c, true) },
		},
		{
			Name: "unlove", Aliases: []string{"ul", "unheart"}, Category: "Tracks",
			Description: "Removes a track from your loved tracks",
			Options:     []bot.Option{optTrack},
			Usage:       []string{"unlove", "unlove daft punk - one more time"},
			Run:         func(c *bot.Ctx) error { return loveCmd(c, false) },
		},
		{
			Name: "loved", Aliases: []string{"lt", "lovedtracks"}, Category: "Tracks",
			Description: "Shows your loved tracks",
			Options:     []bot.Option{optUser},
			Usage:       []string{"loved", "loved @user"},
			Run:         lovedCmd,
		},
		{
			Name: "scrobble", Aliases: []string{"sb"}, Category: "Tracks",
			Description: "Scrobbles a track to your Sonosphere account",
			Options:     []bot.Option{{Name: "track", Description: "Artist - Track (or a search)", Kind: bot.OptQuery, Required: true}},
			Usage:       []string{"scrobble the beatles - hey jude", "sb hey jude"},
			Run:         scrobbleCmd,
		},
	}
}

func trackCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	a, tr, err := resolveTrack(c, "track", t.Username)
	if err != nil {
		return err
	}
	info, err := c.B.API.TrackInfo(c, a, tr, t.Username)
	if err != nil {
		return err
	}
	artist := info.Artist.String()
	if t.User != nil {
		_ = c.B.Store.SetTrackPlays(c, t.User.DiscordID, artist, info.Name, info.UserPlays())
	}
	e := discord.NewEmbed().WithColor(bot.Accent).WithTitle(bot.Trunc(info.Name+" by "+artist, 256)).WithURL(c.B.TrackURL(artist, info.Name))
	if info.Album != nil {
		if img := info.Album.Image.Best(); img != "" {
			e = e.WithThumbnail(img)
		}
	}
	e = e.AddField("Statistics", fmt.Sprintf("**%s** listeners\n**%s** global plays", bot.N(info.Listeners.Int()), bot.N(info.Playcount.Int())), true)
	you := fmt.Sprintf("**%s**", bot.Plural(info.UserPlays(), "play"))
	if info.UserLoved {
		you += "\n❤️ Loved"
	}
	if d := info.Duration.Int(); d > 0 && info.UserPlays() > 0 {
		you += "\n" + bot.LongDuration(time.Duration(d*info.UserPlays())*time.Millisecond) + " listened"
	}
	e = e.AddField(t.DisplayName, you, true)
	var details []string
	if info.Album != nil && info.Album.Title != "" {
		details = append(details, "💿 "+bot.Link(info.Album.Title, c.B.AlbumURL(artist, info.Album.Title)))
	}
	if d := info.Duration.Int(); d > 0 {
		details = append(details, "⏱️ "+bot.Duration(time.Duration(d)*time.Millisecond))
	}
	if len(details) > 0 {
		e = e.AddField("Details", strings.Join(details, "\n"), true)
	}
	if c.GuildID != 0 {
		if rows, err := c.B.Store.WhoKnowsTrack(c, artist, info.Name, store.Scope{GuildID: int64(c.GuildID)}, 1000); err == nil && len(rows) > 0 {
			total := 0
			for _, r := range rows {
				total += r.Playcount
			}
			e = e.AddField("This server", fmt.Sprintf("**%s** · **%s**", bot.Plural(len(rows), "listener"), bot.Plural(total, "play")), false)
		}
	}
	if sum := stripHTML(info.Wiki.Summary); sum != "" {
		e = e.WithDescription(bot.Trunc(sum, 400))
	}
	if len(info.TopTags.Tag) > 0 {
		e = e.AddField("Tags", tagList(info.TopTags.Tag, 6, c.B), false)
	}
	return c.Embed(e)
}

func topTracksCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("weekly")
	r, err := c.B.API.TopTracks(c, t.Username, p.LastFM, 100, 1)
	if err != nil {
		return err
	}
	var lines []string
	for i, a := range r.Track {
		lines = append(lines, numbered(i, bot.Link(a.Name, c.B.TrackURL(a.Artist.String(), a.Name))+" by **"+bot.Esc(a.Artist.String())+"**", a.Playcount.Int()))
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+p.Label+" top tracks").
		WithFooterText(bot.N(r.Attr.Total.Int()) + " different tracks")
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

func requireSession(c *bot.Ctx) (*store.User, error) {
	u, err := c.Self()
	if err != nil {
		return nil, err
	}
	if u.SessionKey == "" {
		return nil, bot.Errorf("Your connection is read-only. Run %s again to allow loving and scrobbling.", c.Cmdf("login"))
	}
	return u, nil
}

func sessionError(c *bot.Ctx, err error) error {
	if sonosphere.IsCode(err, sonosphere.ErrInvalidSession) || sonosphere.IsCode(err, sonosphere.ErrAuthFailed) {
		return bot.Errorf("Your Sonosphere session is no longer valid. Run %s to reconnect.", c.Cmdf("login"))
	}
	return err
}

func loveCmd(c *bot.Ctx, love bool) error {
	u, err := requireSession(c)
	if err != nil {
		return err
	}
	a, tr, err := resolveTrack(c, "track", "")
	if err != nil {
		return err
	}
	info, err := c.B.API.TrackInfo(c, a, tr, u.Username)
	if err == nil {
		a, tr = info.Artist.String(), info.Name
	}
	if love {
		if err := c.B.API.Love(c, u.SessionKey, a, tr); err != nil {
			return sessionError(c, err)
		}
		return c.Success("❤️ Loved **%s** by **%s**.", bot.Link(tr, c.B.TrackURL(a, tr)), bot.Esc(a))
	}
	if err := c.B.API.Unlove(c, u.SessionKey, a, tr); err != nil {
		return sessionError(c, err)
	}
	return c.Success("💔 Unloved **%s** by **%s**.", bot.Link(tr, c.B.TrackURL(a, tr)), bot.Esc(a))
}

func lovedCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	r, err := c.B.API.LovedTracks(c, t.Username, 200, 1)
	if err != nil {
		return err
	}
	var lines []string
	for _, tr := range r.Track {
		line := fmt.Sprintf("❤️ %s by **%s**", bot.Link(tr.Name, c.B.TrackURL(tr.Artist.String(), tr.Name)), bot.Esc(tr.Artist.String()))
		if tr.Date != nil && tr.Date.UTS > 0 {
			line += " · " + bot.Rel(time.Unix(int64(tr.Date.UTS), 0))
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"No loved tracks yet."}
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · loved tracks").
		WithFooterText(bot.Plural(r.Attr.Total.Int(), "loved track"))
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

func scrobbleCmd(c *bot.Ctx) error {
	u, err := requireSession(c)
	if err != nil {
		return err
	}
	if c.Str("track") == "" {
		return bot.Errorf("What should I scrobble? Example: %s", c.Cmdf("scrobble the beatles - hey jude"))
	}
	a, tr, err := resolveTrack(c, "track", "")
	if err != nil {
		return err
	}
	album := ""
	if info, err := c.B.API.TrackInfo(c, a, tr, u.Username); err == nil {
		a, tr = info.Artist.String(), info.Name
		if info.Album != nil {
			album = info.Album.Title
		}
	}
	if err := c.B.API.Scrobble(c, u.SessionKey, a, tr, album, time.Now().Unix()); err != nil {
		return sessionError(c, err)
	}
	return c.Success("✅ Scrobbled **%s** by **%s**.", bot.Link(tr, c.B.TrackURL(a, tr)), bot.Esc(a))
}
