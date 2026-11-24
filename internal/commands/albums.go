package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/store"
)

func albumCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "album", Aliases: []string{"ab", "abi", "albuminfo"}, Category: "Albums",
			Description: "Shows information about an album and your listening",
			Options:     []bot.Option{optAlbum, optUser},
			Usage:       []string{"album", "album radiohead - ok computer"},
			Run:         albumCmd,
		},
		{
			Name: "albumplays", Aliases: []string{"abp"}, Category: "Albums",
			Description: "Shows how many times you've listened to an album",
			Options:     []bot.Option{optAlbum, optUser},
			Usage:       []string{"albumplays", "albumplays the cure - disintegration"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				a, al, err := resolveAlbum(c, "album", t.Username)
				if err != nil {
					return err
				}
				info, err := c.B.API.AlbumInfo(c, a, al, t.Username)
				if err != nil {
					return err
				}
				if t.User != nil {
					_ = c.B.Store.SetAlbumPlays(c, t.User.DiscordID, info.Artist.String(), info.Name, info.Image.Best(), info.UserPlays())
				}
				return c.Say("**%s** has **%s** for **%s** by **%s**.", bot.Esc(t.DisplayName), bot.Plural(info.UserPlays(), "play"),
					bot.Link(info.Name, c.B.AlbumURL(info.Artist.String(), info.Name)), bot.Esc(info.Artist.String()))
			},
		},
		{
			Name: "topalbums", Aliases: []string{"tab", "albums", "tl"}, Category: "Albums",
			Description: "Shows your top albums for a time period",
			Options:     []bot.Option{optPeriod, optUser},
			Usage:       []string{"topalbums", "tab monthly"},
			Run:         topAlbumsCmd,
		},
		{
			Name: "albumtracks", Aliases: []string{"abt", "abtracks"}, Category: "Albums",
			Description: "Shows an album's tracklist with your plays per track",
			Options:     []bot.Option{optAlbum, optUser},
			Usage:       []string{"albumtracks", "abt pink floyd - animals"},
			Run:         albumTracksCmd,
		},
		{
			Name: "cover", Aliases: []string{"co", "art", "albumcover"}, Category: "Albums",
			Description: "Shows the cover of an album",
			Options:     []bot.Option{optAlbum},
			Usage:       []string{"cover", "cover daft punk - discovery"},
			Run:         coverCmd,
		},
		{
			Name: "chart", Aliases: []string{"c", "albumchart"}, Category: "Albums",
			Description: "Generates an image chart of your top albums",
			Options:     []bot.Option{optSize, optPeriod, optUser, optNoTitles, optSkip, optPlays},
			Usage:       []string{"chart", "chart 4x4 monthly", "c 5x5 alltime notitles skip"},
			Run:         func(c *bot.Ctx) error { return chartCmd(c, false) },
		},
		{
			Name: "whoknowsalbum", Aliases: []string{"wkab", "wa", "wkalbum"}, Category: "Albums", GuildOnly: true,
			Description: "Shows who in this server listens to an album",
			Options:     []bot.Option{optAlbum},
			Usage:       []string{"wa", "wa radiohead - kid a"},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkAlbum, modeServer) },
		},
		{
			Name: "friendwhoknowsalbum", Aliases: []string{"fwkab", "fwa", "fwkalbum"}, Category: "Friends",
			Description: "Shows who of your friends listens to an album",
			Options:     []bot.Option{optAlbum},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkAlbum, modeFriends) },
		},
		{
			Name: "globalwhoknowsalbum", Aliases: []string{"gwka", "gwa", "gwkalbum"}, Category: "Albums",
			Description: "Shows who of all bot users listens to an album",
			Options:     []bot.Option{optAlbum},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkAlbum, modeGlobal) },
		},
		{
			Name: "serveralbums", Aliases: []string{"sab", "stab"}, Category: "Albums", GuildOnly: true,
			Description: "Shows the top albums of this server",
			Run:         func(c *bot.Ctx) error { return serverTop(c, "albums") },
		},
	}
}

func albumCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	a, al, err := resolveAlbum(c, "album", t.Username)
	if err != nil {
		return err
	}
	info, err := c.B.API.AlbumInfo(c, a, al, t.Username)
	if err != nil {
		return err
	}
	artist := info.Artist.String()
	if t.User != nil {
		_ = c.B.Store.SetAlbumPlays(c, t.User.DiscordID, artist, info.Name, info.Image.Best(), info.UserPlays())
	}
	e := discord.NewEmbed().WithColor(bot.Accent).WithTitle(bot.Trunc(info.Name+" by "+artist, 256)).WithURL(c.B.AlbumURL(artist, info.Name))
	if img := info.Image.Best(); img != "" {
		e = e.WithThumbnail(img)
	}
	e = e.AddField("Statistics", fmt.Sprintf("**%s** listeners\n**%s** global plays", bot.N(info.Listeners.Int()), bot.N(info.Playcount.Int())), true)
	e = e.AddField(t.DisplayName, fmt.Sprintf("**%s**", bot.Plural(info.UserPlays(), "play")), true)
	if c.GuildID != 0 {
		if rows, err := c.B.Store.WhoKnowsAlbum(c, artist, info.Name, store.Scope{GuildID: int64(c.GuildID)}, 1000); err == nil && len(rows) > 0 {
			total := 0
			for _, r := range rows {
				total += r.Playcount
			}
			e = e.AddField("This server", fmt.Sprintf("**%s**\n**%s**", bot.Plural(len(rows), "listener"), bot.Plural(total, "play")), true)
		}
	}
	if n := len(info.Tracks.Track); n > 0 {
		var dur int
		for _, tr := range info.Tracks.Track {
			dur += tr.Duration.Int()
		}
		s := bot.Plural(n, "track")
		if dur > 0 {
			s += " · " + bot.Duration(time.Duration(dur)*time.Second)
		}
		e = e.AddField("Tracklist", s, true)
	}
	if info.Wiki.Published != "" {
		e = e.AddField("Released", info.Wiki.Published, true)
	}
	if sum := stripHTML(info.Wiki.Summary); sum != "" {
		e = e.WithDescription(bot.Trunc(sum, 500))
	}
	if len(info.Tags.Tag) > 0 {
		e = e.AddField("Tags", tagList(info.Tags.Tag, 6, c.B), false)
	}
	return c.Embed(e)
}

func topAlbumsCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("weekly")
	r, err := c.B.API.TopAlbums(c, t.Username, p.LastFM, 100, 1)
	if err != nil {
		return err
	}
	var lines []string
	for i, a := range r.Album {
		lines = append(lines, numbered(i, bot.Link(a.Name, c.B.AlbumURL(a.Artist.String(), a.Name))+" by **"+bot.Esc(a.Artist.String())+"**", a.Playcount.Int()))
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+p.Label+" top albums").
		WithFooterText(bot.N(r.Attr.Total.Int()) + " different albums")
	if len(r.Album) > 0 {
		if img := r.Album[0].Image.Best(); img != "" {
			tmpl = tmpl.WithThumbnail(img)
		}
	}
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

func albumTracksCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	a, al, err := resolveAlbum(c, "album", t.Username)
	if err != nil {
		return err
	}
	info, err := c.B.API.AlbumInfo(c, a, al, t.Username)
	if err != nil {
		return err
	}
	artist := info.Artist.String()
	plays := map[string]int{}
	if t.User != nil {
		if items, err := c.B.Store.UserArtistTracks(c, t.User.DiscordID, artist); err == nil {
			for _, it := range items {
				plays[strings.ToLower(it.Name)] = it.Playcount
			}
		}
	}
	var lines []string
	var totalDur, counted int
	for i, tr := range info.Tracks.Track {
		p := plays[strings.ToLower(tr.Name)]
		counted += p
		d := ""
		if tr.Duration > 0 {
			totalDur += tr.Duration.Int()
			d = " `" + bot.Duration(time.Duration(tr.Duration.Int())*time.Second) + "`"
		}
		line := fmt.Sprintf("%d. %s%s", i+1, bot.Link(tr.Name, c.B.TrackURL(artist, tr.Name)), d)
		if p > 0 {
			line += " - *" + bot.Plural(p, "play") + "*"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"Sonosphere has no tracklist for this album."}
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+bot.Trunc(info.Name+" by "+artist, 200)).
		WithURL(c.B.AlbumURL(artist, info.Name)).
		WithFooterText(fmt.Sprintf("%s on this album", bot.Plural(info.UserPlays(), "play")))
	if img := info.Image.Best(); img != "" {
		tmpl = tmpl.WithThumbnail(img)
	}
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

func coverCmd(c *bot.Ctx) error {
	a, al, err := resolveAlbum(c, "album", "")
	if err != nil {
		return err
	}
	info, err := c.B.API.AlbumInfo(c, a, al, "")
	if err != nil {
		return err
	}
	img := info.Image.Best()
	if img == "" {
		return bot.Errorf("Sonosphere has no cover for **%s** by **%s**.", bot.Esc(info.Name), bot.Esc(info.Artist.String()))
	}
	e := discord.NewEmbed().WithColor(bot.Accent).
		WithDescription(fmt.Sprintf("**%s** by **%s**", bot.Link(info.Name, c.B.AlbumURL(info.Artist.String(), info.Name)), bot.Esc(info.Artist.String()))).
		WithImage(img)
	return c.Embed(e)
}
