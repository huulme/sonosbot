package commands

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

func genreCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "topgenres", Aliases: []string{"tg", "genres", "toptags"}, Category: "Genres",
			Description: "Shows your top genres, based on the tags of your top artists",
			Options:     []bot.Option{optPeriod, optUser},
			Usage:       []string{"topgenres", "tg monthly"},
			Run:         topGenresCmd,
		},
		{
			Name: "genre", Aliases: []string{"g", "tag"}, Category: "Genres",
			Description: "Shows info about a genre and your top artists in it (defaults to your current artist's genres)",
			Options:     []bot.Option{{Name: "genre", Description: "Genre/tag name", Kind: bot.OptQuery}, optUser},
			Usage:       []string{"genre", "genre shoegaze"},
			Run:         genreCmd,
		},
		{
			Name: "whoknowsgenre", Aliases: []string{"wg", "wkg", "wkgenre"}, Category: "Genres", GuildOnly: true,
			Description: "Shows who in this server listens to a genre the most",
			Options:     []bot.Option{{Name: "genre", Description: "Genre/tag name", Kind: bot.OptQuery}},
			Usage:       []string{"wg", "wg hyperpop"},
			Run:         whoKnowsGenreCmd,
		},
	}
}

// ignoredTags are noisy tags that aren't genres.
var ignoredTags = map[string]bool{
	"seen live": true, "favorites": true, "favourite": true, "favorite": true, "favourites": true,
	"under 2000 listeners": true, "spotify": true, "albums i own": true, "my favorite": true,
	"male vocalists": true, "female vocalists": true, "beautiful": true, "awesome": true, "love": true, "": true,
}

func topGenresCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("alltime")
	r, err := c.B.API.TopArtists(c, t.Username, p.LastFM, 60, 1)
	if err != nil {
		return err
	}
	type gs struct {
		plays   int
		artists []string
	}
	genres := map[string]*gs{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range r.Artist {
		wg.Add(1)
		go func(a sonosphere.TopArtist) {
			defer wg.Done()
			tags, err := c.B.API.ArtistTopTags(c, a.Name)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			n := 0
			for _, tg := range tags {
				name := strings.ToLower(strings.TrimSpace(tg.Name))
				if ignoredTags[name] {
					continue
				}
				g := genres[name]
				if g == nil {
					g = &gs{}
					genres[name] = g
				}
				g.plays += a.Playcount.Int()
				g.artists = append(g.artists, a.Name)
				if n++; n >= 3 {
					break
				}
			}
		}(a)
	}
	wg.Wait()
	type row struct {
		name string
		*gs
	}
	var rows []row
	for k, v := range genres {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].plays > rows[j].plays })
	var lines []string
	for i, g := range rows {
		if i >= 50 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. **%s** - *%s*\n-# %s", i+1, bot.Link(g.name, c.B.TagURL(g.name)), bot.Plural(g.plays, "play"),
			bot.Esc(bot.Trunc(strings.Join(g.artists, ", "), 90))))
	}
	if len(lines) == 0 {
		lines = []string{"No genres found."}
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+p.Label+" top genres").
		WithFooterText("Based on the top 3 tags of your top 60 artists")
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

func resolveGenre(c *bot.Ctx) (string, error) {
	if g := c.Str("genre"); g != "" {
		return g, nil
	}
	a, err := resolveArtist(c, "", "")
	if err != nil {
		return "", err
	}
	tags, err := c.B.API.ArtistTopTags(c, a)
	if err != nil {
		return "", err
	}
	for _, t := range tags {
		if !ignoredTags[strings.ToLower(t.Name)] {
			return t.Name, nil
		}
	}
	return "", bot.Errorf("**%s** has no genre tags. Try searching for a genre.", bot.Esc(a))
}

func genreCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	g, err := resolveGenre(c)
	if err != nil {
		return err
	}
	info, _ := c.B.API.TagInfo(c, g)
	top, err := c.B.API.TagTopArtists(c, g, 100)
	if err != nil {
		return err
	}
	e := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Genre: " + strings.ToLower(g)).WithURL(c.B.TagURL(g))
	if info != nil {
		if sum := stripHTML(info.Wiki.Summary); sum != "" {
			e = e.WithDescription(bot.Trunc(sum, 500))
		}
	}
	// Your plays for artists in this genre (from the index).
	if t.User != nil {
		known := map[string]int{}
		if arts, err := c.B.Store.UserArtists(c, t.User.DiscordID, 100000); err == nil {
			for _, a := range arts {
				known[store.Key(a.Name)] = a.Playcount
			}
		}
		type ua struct {
			name  string
			plays int
		}
		var yours []ua
		for _, a := range top {
			if p, ok := known[store.Key(a.Name)]; ok {
				yours = append(yours, ua{a.Name, p})
			}
		}
		sort.Slice(yours, func(i, j int) bool { return yours[i].plays > yours[j].plays })
		var lines []string
		for i, y := range yours {
			if i >= 10 {
				break
			}
			lines = append(lines, numbered(i, bot.Link(y.name, c.B.ArtistURL(y.name)), y.plays))
		}
		if len(lines) > 0 {
			e = e.AddField(t.DisplayName+"'s top artists", strings.Join(lines, "\n"), true)
		}
	}
	var lines []string
	for i, a := range top {
		if i >= 10 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, bot.Link(a.Name, c.B.ArtistURL(a.Name))))
	}
	if len(lines) > 0 {
		e = e.AddField("Top artists on Sonosphere", strings.Join(lines, "\n"), true)
	}
	return c.Embed(e)
}

func whoKnowsGenreCmd(c *bot.Ctx) error {
	if _, err := c.Self(); err != nil {
		return err
	}
	g, err := resolveGenre(c)
	if err != nil {
		return err
	}
	top, err := c.B.API.TagTopArtists(c, g, 200)
	if err != nil {
		return err
	}
	type agg struct {
		name  string
		plays int
		top   string
		topN  int
	}
	users := map[int64]*agg{}
	for _, a := range top {
		rows, err := c.B.Store.WhoKnowsArtist(c, a.Name, store.Scope{GuildID: int64(c.GuildID)}, 1000)
		if err != nil {
			return err
		}
		for _, r := range rows {
			u := users[r.UserID]
			if u == nil {
				name := r.DisplayName
				if name == "" {
					name = r.Username
				}
				u = &agg{name: name}
				users[r.UserID] = u
			}
			u.plays += r.Playcount
			if r.Playcount > u.topN {
				u.top, u.topN = a.Name, r.Playcount
			}
		}
	}
	list := make([]*agg, 0, len(users))
	for _, u := range users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].plays > list[j].plays })
	var lines []string
	for i, u := range list {
		line := fmt.Sprintf("%s **%s** - *%s*", bot.Medal(i), bot.Esc(u.name), bot.Plural(u.plays, "play"))
		if u.top != "" {
			line += " · top: " + bot.Esc(u.top)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"Nobody in this server listens to this genre yet."}
	}
	tmpl := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Who knows " + strings.ToLower(g) + " in this server").WithURL(c.B.TagURL(g)).
		WithFooterText(fmt.Sprintf("Based on the top %d %s artists on Sonosphere", len(top), strings.ToLower(g)))
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}
