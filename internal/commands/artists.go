package commands

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/chart"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

var (
	optNoTitles = bot.Option{Name: "notitles", Description: "Hide titles on the chart", Kind: bot.OptFlag, Aliases: []string{"nt"}}
	optSkip     = bot.Option{Name: "skip", Description: "Skip items without images", Kind: bot.OptFlag, Aliases: []string{"s", "skipemptyalbums", "skipempty"}}
	optPlays    = bot.Option{Name: "plays", Description: "Show play counts on the chart", Kind: bot.OptFlag, Aliases: []string{"pc"}}
)

func artistCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "artist", Aliases: []string{"a", "ai", "artistinfo"}, Category: "Artists",
			Description: "Shows information about an artist and your listening",
			Options:     []bot.Option{optArtist, optUser},
			Usage:       []string{"artist", "artist the beatles"},
			Run:         artistCmd,
		},
		{
			Name: "artistplays", Aliases: []string{"ap"}, Category: "Artists",
			Description: "Shows how many times you've listened to an artist",
			Options:     []bot.Option{optArtist, optUser},
			Usage:       []string{"artistplays", "artistplays radiohead"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				a, err := resolveArtist(c, "artist", t.Username)
				if err != nil {
					return err
				}
				info, err := c.B.API.ArtistInfo(c, a, t.Username)
				if err != nil {
					return err
				}
				if t.User != nil {
					_ = c.B.Store.SetArtistPlays(c, t.User.DiscordID, info.Name, info.UserPlays())
				}
				return c.Say("**%s** has **%s** for **%s**.", bot.Esc(t.DisplayName), bot.Plural(info.UserPlays(), "play"), bot.Link(info.Name, c.B.ArtistURL(info.Name)))
			},
		},
		{
			Name: "artisttracks", Aliases: []string{"at", "att"}, Category: "Artists",
			Description: "Shows your top tracks for an artist",
			Options:     []bot.Option{optArtist, optUser},
			Usage:       []string{"artisttracks", "artisttracks deftones"},
			Run:         func(c *bot.Ctx) error { return artistItems(c, false) },
		},
		{
			Name: "artistalbums", Aliases: []string{"aa", "aab"}, Category: "Artists",
			Description: "Shows your top albums for an artist",
			Options:     []bot.Option{optArtist, optUser},
			Usage:       []string{"artistalbums", "artistalbums deftones"},
			Run:         func(c *bot.Ctx) error { return artistItems(c, true) },
		},
		{
			Name: "topartists", Aliases: []string{"ta", "artists"}, Category: "Artists",
			Description: "Shows your top artists for a time period",
			Options:     []bot.Option{optPeriod, optUser},
			Usage:       []string{"topartists", "ta monthly", "ta weekly @user"},
			Run:         topArtistsCmd,
		},
		{
			Name: "artistchart", Aliases: []string{"ac", "achart"}, Category: "Artists",
			Description: "Generates an image chart of your top artists",
			Options:     []bot.Option{optSize, optPeriod, optUser, optNoTitles, optSkip, optPlays},
			Usage:       []string{"artistchart", "artistchart 4x4 monthly", "ac 5x5 alltime notitles"},
			Run:         func(c *bot.Ctx) error { return chartCmd(c, true) },
		},
		{
			Name: "similar", Aliases: []string{"sim", "similarartists"}, Category: "Artists",
			Description: "Shows artists similar to an artist, marking ones you already know",
			Options:     []bot.Option{optArtist},
			Usage:       []string{"similar", "similar björk"},
			Run:         similarCmd,
		},
		{
			Name: "taste", Aliases: []string{"t", "compare"}, Category: "Artists",
			Description: "Compares your top artists with another user",
			Options:     []bot.Option{{Name: "user", Description: "The user to compare with", Kind: bot.OptUser, Required: true}, optPeriod},
			Usage:       []string{"taste @user", "taste sp:username monthly"},
			Run:         tasteCmd,
		},
		{
			Name: "affinity", Aliases: []string{"aff", "neighbors", "n"}, Category: "Artists", GuildOnly: true,
			Description: "Shows which members of this server have the most similar taste to you",
			Run:         affinityCmd,
		},
		{
			Name: "whoknows", Aliases: []string{"wk", "w"}, Category: "Artists", GuildOnly: true,
			Description: "Shows who in this server listens to an artist",
			Options:     []bot.Option{optArtist},
			Usage:       []string{"whoknows", "wk the beatles"},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkArtist, modeServer) },
		},
		{
			Name: "friendwhoknows", Aliases: []string{"fwk", "fw"}, Category: "Friends",
			Description: "Shows who of your friends listens to an artist",
			Options:     []bot.Option{optArtist},
			Usage:       []string{"fwk", "fwk the beatles"},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkArtist, modeFriends) },
		},
		{
			Name: "globalwhoknows", Aliases: []string{"gwk", "gw"}, Category: "Artists",
			Description: "Shows who of all bot users listens to an artist",
			Options:     []bot.Option{optArtist},
			Usage:       []string{"gwk", "gwk mac miller"},
			Run:         func(c *bot.Ctx) error { return whoKnows(c, wkArtist, modeGlobal) },
		},
		{
			Name: "serverartists", Aliases: []string{"sa", "sta"}, Category: "Artists", GuildOnly: true,
			Description: "Shows the top artists of this server",
			Usage:       []string{"serverartists"},
			Run:         func(c *bot.Ctx) error { return serverTop(c, "artists") },
		},
	}
}

func artistCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	a, err := resolveArtist(c, "artist", t.Username)
	if err != nil {
		return err
	}
	info, err := c.B.API.ArtistInfo(c, a, t.Username)
	if err != nil {
		return err
	}
	if t.User != nil {
		_ = c.B.Store.SetArtistPlays(c, t.User.DiscordID, info.Name, info.UserPlays())
	}
	e := discord.NewEmbed().WithColor(bot.Accent).WithTitle(info.Name).WithURL(c.B.ArtistURL(info.Name))
	if img := info.Image.Best(); img != "" {
		e = e.WithThumbnail(img)
	}
	e = e.AddField("Statistics", fmt.Sprintf("**%s** listeners\n**%s** global plays", bot.N(info.Stats.Listeners.Int()), bot.N(info.Stats.Playcount.Int())), true)
	e = e.AddField(t.DisplayName, fmt.Sprintf("**%s**", bot.Plural(info.UserPlays(), "play")), true)
	if c.GuildID != 0 {
		if rows, err := c.B.Store.WhoKnowsArtist(c, info.Name, store.Scope{GuildID: int64(c.GuildID)}, 1000); err == nil && len(rows) > 0 {
			total := 0
			for _, r := range rows {
				total += r.Playcount
			}
			e = e.AddField("This server", fmt.Sprintf("**%s**\n**%s**", bot.Plural(len(rows), "listener"), bot.Plural(total, "play")), true)
		}
		if cr, err := c.B.Store.ActiveCrown(c, int64(c.GuildID), info.Name); err == nil && cr != nil {
			e = e.WithFooterText(fmt.Sprintf("👑 Crown held by %s (%s)", cr.Username, bot.Plural(cr.Playcount, "play")))
		}
	}
	if bio := stripHTML(info.Bio.Summary); bio != "" {
		e = e.WithDescription(bot.Trunc(bio, 600))
	}
	if len(info.Tags.Tag) > 0 {
		e = e.AddField("Tags", tagList(info.Tags.Tag, 6, c.B), false)
	}
	if len(info.Similar.Artist) > 0 {
		var sim []string
		for i, s := range info.Similar.Artist {
			if i >= 6 {
				break
			}
			sim = append(sim, bot.Link(s.Name, c.B.ArtistURL(s.Name)))
		}
		e = e.AddField("Similar", strings.Join(sim, " · "), false)
	}
	return c.Embed(e)
}

// artistItems lists a user's indexed tracks or albums for an artist.
func artistItems(c *bot.Ctx, albums bool) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	if t.User == nil {
		return bot.Errorf("This command only works for users who connected their account to the bot.")
	}
	a, err := resolveArtist(c, "artist", t.Username)
	if err != nil {
		return err
	}
	info, err := c.B.API.ArtistInfo(c, a, t.Username)
	if err != nil {
		return err
	}
	var lines []string
	total := 0
	if albums {
		items, err := c.B.Store.UserArtistAlbums(c, t.User.DiscordID, info.Name)
		if err != nil {
			return err
		}
		for i, it := range items {
			lines = append(lines, numbered(i, bot.Link(it.Name, c.B.AlbumURL(it.Artist, it.Name)), it.Playcount))
			total += it.Playcount
		}
	} else {
		items, err := c.B.Store.UserArtistTracks(c, t.User.DiscordID, info.Name)
		if err != nil {
			return err
		}
		for i, it := range items {
			lines = append(lines, numbered(i, bot.Link(it.Name, c.B.TrackURL(it.Artist, it.Name)), it.Playcount))
			total += it.Playcount
		}
	}
	kind := "tracks"
	if albums {
		kind = "albums"
	}
	if len(lines) == 0 {
		lines = []string{fmt.Sprintf("No indexed %s by this artist. If you listened recently, run %s.", kind, c.Cmdf("update"))}
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, fmt.Sprintf(" · top %s for %s", kind, info.Name)).
		WithURL(c.B.ArtistURL(info.Name)).
		WithFooterText(fmt.Sprintf("%s · %s total for this artist", bot.Plural(len(lines), strings.TrimSuffix(kind, "s")), bot.Plural(info.UserPlays(), "play")))
	if img := info.Image.Best(); img != "" {
		tmpl = tmpl.WithThumbnail(img)
	}
	return c.Paginate(bot.Pages(tmpl, lines, 12))
}

func topArtistsCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("weekly")
	r, err := c.B.API.TopArtists(c, t.Username, p.LastFM, 100, 1)
	if err != nil {
		return err
	}
	var lines []string
	for i, a := range r.Artist {
		lines = append(lines, numbered(i, "**"+bot.Link(a.Name, c.B.ArtistURL(a.Name))+"**", a.Playcount.Int()))
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+p.Label+" top artists").
		WithFooterText(bot.N(r.Attr.Total.Int()) + " different artists")
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

// chartCmd renders an album or artist chart image.
func chartCmd(c *bot.Ctx, artists bool) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	w, h := c.Size(3, 3)
	if w < 1 || h < 1 || w > 10 || h > 10 {
		return bot.Errorf("Chart size must be between 1x1 and 10x10.")
	}
	p := c.Period("weekly")
	n := w * h
	fetchN := n
	if c.Flag("skip") {
		fetchN = min(n*2, 200)
	}
	var tiles []chart.Tile
	if artists {
		r, err := c.B.API.TopArtists(c, t.Username, p.LastFM, fetchN, 1)
		if err != nil {
			return err
		}
		for _, a := range r.Artist {
			img := a.Image.Best()
			if c.Flag("skip") && img == "" {
				continue
			}
			sub := ""
			if c.Flag("plays") {
				sub = bot.Plural(a.Playcount.Int(), "play")
			}
			tiles = append(tiles, chart.Tile{ImageURL: img, Title: a.Name, Subtitle: sub})
		}
	} else {
		r, err := c.B.API.TopAlbums(c, t.Username, p.LastFM, fetchN, 1)
		if err != nil {
			return err
		}
		for _, a := range r.Album {
			img := a.Image.Best()
			if c.Flag("skip") && img == "" {
				continue
			}
			sub := a.Artist.String()
			if c.Flag("plays") {
				sub += " · " + bot.Plural(a.Playcount.Int(), "play")
			}
			tiles = append(tiles, chart.Tile{ImageURL: img, Title: a.Name, Subtitle: sub})
		}
	}
	if len(tiles) == 0 {
		return bot.Errorf("No %s to chart for this period.", map[bool]string{true: "artists", false: "albums"}[artists])
	}
	if len(tiles) > n {
		tiles = tiles[:n]
	}
	// Shrink the grid if there isn't enough data.
	if len(tiles) < n {
		h = int(math.Ceil(float64(len(tiles)) / float64(w)))
	}
	img, err := chart.Render(c, tiles, chart.Options{Width: w, Height: h, Titles: !c.Flag("notitles")})
	if err != nil {
		return err
	}
	kind := "album"
	if artists {
		kind = "artist"
	}
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, fmt.Sprintf(" · %dx%d %s %s chart", w, h, strings.ToLower(p.Label), kind)).
		WithImage("attachment://chart.jpg")
	_, err = c.Reply(bot.Response{Embeds: []discord.Embed{e}, Files: []*discord.File{discord.NewFile("chart.jpg", "chart", bytes.NewReader(img))}})
	return err
}

func similarCmd(c *bot.Ctx) error {
	a, err := resolveArtist(c, "artist", "")
	if err != nil {
		return err
	}
	sim, err := c.B.API.ArtistSimilar(c, a, 30)
	if err != nil {
		return err
	}
	known := map[string]int{}
	if self, err := c.Self(); err == nil {
		if arts, err := c.B.Store.UserArtists(c, self.DiscordID, 100000); err == nil {
			for _, x := range arts {
				known[store.Key(x.Name)] = x.Playcount
			}
		}
	}
	var lines []string
	for i, s := range sim {
		line := fmt.Sprintf("%d. %s", i+1, bot.Link(s.Name, c.B.ArtistURL(s.Name)))
		if p, ok := known[store.Key(s.Name)]; ok {
			line += fmt.Sprintf(" - *%s* ✓", bot.Plural(p, "play"))
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"No similar artists found."}
	}
	tmpl := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Artists similar to " + a).WithURL(c.B.ArtistURL(a)).
		WithFooterText("✓ = artists you already listen to")
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

func tasteCmd(c *bot.Ctx) error {
	self, err := c.Self()
	if err != nil {
		return err
	}
	other, err := c.Target()
	if err != nil {
		return err
	}
	if other.IsSelf {
		return bot.Errorf("Pick someone other than yourself to compare with.")
	}
	p := c.Period("alltime")
	mine, err := c.B.API.TopArtists(c, self.Username, p.LastFM, 1000, 1)
	if err != nil {
		return err
	}
	theirs, err := c.B.API.TopArtists(c, other.Username, p.LastFM, 1000, 1)
	if err != nil {
		return err
	}
	type pair struct {
		name     string
		me, them int
	}
	theirMap := map[string]sonosphere.TopArtist{}
	theirTotal := 0
	for _, a := range theirs.Artist {
		theirMap[store.Key(a.Name)] = a
		theirTotal += a.Playcount.Int()
	}
	myTotal := 0
	var shared []pair
	for _, a := range mine.Artist {
		myTotal += a.Playcount.Int()
		if t, ok := theirMap[store.Key(a.Name)]; ok {
			shared = append(shared, pair{a.Name, a.Playcount.Int(), t.Playcount.Int()})
		}
	}
	// Similarity: overlap of normalised play shares.
	score := 0.0
	for _, s := range shared {
		score += math.Min(float64(s.me)/float64(max(myTotal, 1)), float64(s.them)/float64(max(theirTotal, 1)))
	}
	sort.Slice(shared, func(i, j int) bool { return min(shared[i].me, shared[i].them) > min(shared[j].me, shared[j].them) })
	var lines []string
	for i, s := range shared {
		if i >= 20 {
			break
		}
		marker := "•"
		if s.me > s.them {
			marker = "▸"
		} else if s.them > s.me {
			marker = "◂"
		}
		lines = append(lines, fmt.Sprintf("%s **%s** - %s / %s", marker, bot.Esc(bot.Trunc(s.name, 40)), bot.N(s.me), bot.N(s.them)))
	}
	if len(lines) == 0 {
		lines = []string{"You have no artists in common."}
	}
	e := discord.NewEmbed().WithColor(bot.Accent).
		WithTitle(bot.Trunc(fmt.Sprintf("%s taste comparison: %s vs %s", p.Label, c.AuthorName, other.DisplayName), 256)).
		WithDescription(fmt.Sprintf("**%.0f%%** taste match · **%s** artists in common\n\n%s", score*100, bot.N(len(shared)), strings.Join(lines, "\n"))).
		WithFooterText("▸ you listen more · ◂ they listen more · plays shown as you / them")
	return c.Embed(e)
}

func affinityCmd(c *bot.Ctx) error {
	self, err := c.Self()
	if err != nil {
		return err
	}
	mine, err := c.B.Store.UserArtists(c, self.DiscordID, 500)
	if err != nil {
		return err
	}
	if len(mine) == 0 {
		return bot.Errorf("Your library isn't indexed yet. Run %s first.", c.Cmdf("update"))
	}
	myTotal := 0
	myShare := map[string]float64{}
	for _, a := range mine {
		myTotal += a.Playcount
	}
	for _, a := range mine {
		myShare[store.Key(a.Name)] = float64(a.Playcount) / float64(myTotal)
	}
	members, err := c.B.Store.GuildMembers(c, int64(c.GuildID))
	if err != nil {
		return err
	}
	type res struct {
		name   string
		score  float64
		shared int
	}
	var out []res
	for _, m := range members {
		if m.UserID == self.DiscordID || m.Blocked {
			continue
		}
		theirs, err := c.B.Store.UserArtists(c, m.UserID, 500)
		if err != nil || len(theirs) == 0 {
			continue
		}
		total := 0
		for _, a := range theirs {
			total += a.Playcount
		}
		score, shared := 0.0, 0
		for _, a := range theirs {
			if s, ok := myShare[store.Key(a.Name)]; ok {
				score += math.Min(s, float64(a.Playcount)/float64(total))
				shared++
			}
		}
		name := m.DisplayName
		if name == "" {
			name = m.Username
		}
		out = append(out, res{name, score, shared})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	var lines []string
	for i, r := range out {
		lines = append(lines, fmt.Sprintf("%d. **%s** - %.0f%% match · %s shared", i+1, bot.Esc(r.name), r.score*100, bot.Plural(r.shared, "artist")))
	}
	if len(lines) == 0 {
		lines = []string{"No other members with indexed libraries yet."}
	}
	tmpl := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Server affinity for " + c.AuthorName).
		WithFooterText("Based on the overlap of your top 500 artists")
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

func serverTop(c *bot.Ctx, kind string) error {
	rows, err := c.B.Store.ServerTop(c, int64(c.GuildID), kind, 100)
	if err != nil {
		return err
	}
	var lines []string
	for i, r := range rows {
		var name string
		switch kind {
		case "artists":
			name = "**" + bot.Link(r.Name, c.B.ArtistURL(r.Name)) + "**"
		case "albums":
			name = bot.Link(r.Name, c.B.AlbumURL(r.Artist, r.Name)) + " by **" + bot.Esc(r.Artist) + "**"
		default:
			name = bot.Link(r.Name, c.B.TrackURL(r.Artist, r.Name)) + " by **" + bot.Esc(r.Artist) + "**"
		}
		lines = append(lines, fmt.Sprintf("%d. %s - *%s* · %s", i+1, name, bot.Plural(r.Playcount, "play"), bot.Plural(r.Listeners, "listener")))
	}
	if len(lines) == 0 {
		lines = []string{"No data yet. Members are added when they use the bot here."}
	}
	title := "Top " + kind
	if g, ok := c.B.Client.Caches.Guild(c.GuildID); ok {
		title += " in " + g.Name
	}
	tmpl := discord.NewEmbed().WithColor(bot.Accent).WithTitle(title).WithFooterText("All-time, based on indexed libraries")
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}
