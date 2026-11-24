package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
)

func playCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "fm", Aliases: []string{"np", "nowplaying"}, Category: "Plays",
			Description: "Shows what you or someone else is listening to",
			Options:     []bot.Option{optUser, {Name: "mode", Description: "Override your fm mode for this response", Kind: bot.OptChoice, Choices: fmModes}},
			Usage:       []string{"fm", "fm @user", "fm sp:username", "fm embedfull"},
			Run:         fmCmd,
		},
		{
			Name: "recent", Aliases: []string{"r", "recenttracks", "rt"}, Category: "Plays",
			Description: "Shows your latest scrobbles",
			Options:     []bot.Option{optUser, {Name: "artist", Description: "Only show tracks by this artist", Kind: bot.OptQuery}},
			Usage:       []string{"recent", "recent @user", "recent moby"},
			Run:         recentCmd,
		},
		{
			Name: "plays", Aliases: []string{"p", "scrobbles"}, Category: "Plays",
			Description: "Shows your total scrobble count for a time period",
			Options:     []bot.Option{optUser, optPeriod},
			Usage:       []string{"plays", "plays monthly", "plays @user weekly"},
			Run:         playsCmd,
		},
		{
			Name: "streak", Aliases: []string{"str", "combo"}, Category: "Plays",
			Description: "Shows your current artist, album and track listening streak",
			Options:     []bot.Option{optUser},
			Usage:       []string{"streak", "streak @user"},
			Run:         streakCmd,
		},
		{
			Name: "pace", Aliases: []string{"pc"}, Category: "Plays",
			Description: "Estimates when you'll reach a scrobble goal at your current pace",
			Options: []bot.Option{optUser, optPeriod,
				{Name: "goal", Description: "Scrobble goal (defaults to your next milestone)", Kind: bot.OptInt, Min: 1, Max: 100_000_000}},
			Usage: []string{"pace", "pace 100000", "pace 50000 weekly"},
			Run:   paceCmd,
		},
		{
			Name: "milestone", Aliases: []string{"ms", "mspl"}, Category: "Plays",
			Description: "Shows a milestone scrobble from your history, e.g. your 10,000th",
			Options: []bot.Option{optUser,
				{Name: "number", Description: "Which scrobble (e.g. 1000)", Kind: bot.OptInt, Min: 1, Max: 100_000_000}},
			Usage: []string{"milestone", "milestone 1", "milestone 50000"},
			Run:   milestoneCmd,
		},
		{
			Name: "overview", Aliases: []string{"o", "ov"}, Category: "Plays",
			Description: "Shows a daily overview of your recent listening",
			Options: []bot.Option{optUser,
				{Name: "days", Description: "Number of days (1-8)", Kind: bot.OptInt, Min: 1, Max: 8}},
			Usage: []string{"overview", "overview 7"},
			Run:   overviewCmd,
		},
		{
			Name: "year", Aliases: []string{"yr", "lastyear"}, Category: "Plays",
			Description: "Shows your top artists, albums and tracks of this year (or last year)",
			Options: []bot.Option{optUser,
				{Name: "which", Description: "This year or last year", Kind: bot.OptChoice, Choices: []string{"this", "last"}}},
			Usage: []string{"year", "year last"},
			Run:   yearCmd,
		},
		{
			Name: "recap", Aliases: []string{"rc", "wrapped"}, Category: "Plays",
			Description: "A multi-page recap of your listening in a time period",
			Options:     []bot.Option{optUser, optPeriod},
			Usage:       []string{"recap", "recap monthly"},
			Run:         recapCmd,
		},
	}
}

// ---------------- fm ----------------

func fmCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	rt, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 2, NoCache: true})
	if err != nil {
		return err
	}
	if len(rt.Track) == 0 {
		return bot.Errorf("**%s** hasn't scrobbled anything yet.", bot.Esc(t.DisplayName))
	}
	cur := rt.Track[0]
	var prev *sonosphere.RecentTrack
	if len(rt.Track) > 1 {
		prev = &rt.Track[1]
	}
	mode := c.Str("mode")
	if mode == "" {
		mode = "embedmini"
		if t.User != nil && t.IsSelf {
			mode = t.User.FmMode
		} else if self, err := c.Self(); err == nil {
			mode = self.FmMode
		}
	}

	artist, track, album := cur.Artist.String(), cur.Name, cur.Album.String()
	var trackPlays int
	var loved bool
	if info, err := c.B.API.TrackInfo(c, artist, track, t.Username); err == nil {
		trackPlays, loved = info.UserPlays(), bool(info.UserLoved)
	}
	total := rt.Attr.Total.Int()

	status := "Now playing"
	if !cur.NowPlaying() {
		status = "Last played"
	}
	trackLink := bot.Link(track, c.B.TrackURL(artist, track))
	heart := ""
	if loved {
		heart = " ❤️"
	}
	footerParts := []string{}
	if trackPlays > 0 {
		footerParts = append(footerParts, bot.Plural(trackPlays, "play")+" on this track")
	}
	footerParts = append(footerParts, bot.N(total)+" total scrobbles")
	footer := strings.Join(footerParts, " · ")
	img := cur.Image.Best()

	line := func(rt sonosphere.RecentTrack) string {
		s := fmt.Sprintf("**%s**\nby **%s**", bot.Link(rt.Name, c.B.TrackURL(rt.Artist.String(), rt.Name)), bot.Esc(rt.Artist.String()))
		if a := rt.Album.String(); a != "" {
			s += " | *" + bot.Esc(a) + "*"
		}
		return s
	}

	var resp bot.Response
	switch mode {
	case "embedfull":
		e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+status).
			AddField("Current:", line(cur)+heart, false)
		if prev != nil {
			e = e.AddField("Previous:", line(*prev), false)
		}
		if img != "" {
			e = e.WithThumbnail(img)
		}
		resp.Embeds = []discord.Embed{e.WithFooterText(footer)}
	case "embedtiny":
		e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+status).
			WithDescription(fmt.Sprintf("**%s** by **%s**%s", trackLink, bot.Esc(artist), heart))
		resp.Embeds = []discord.Embed{e}
	case "textmini":
		s := fmt.Sprintf("**%s** by **%s**%s", bot.Esc(track), bot.Esc(artist), heart)
		if album != "" {
			s += " · *" + bot.Esc(album) + "*"
		}
		resp.Content = s + "\n-# " + bot.Esc(t.DisplayName) + " · " + footer
	case "textfull":
		s := fmt.Sprintf("**%s** · %s\n**%s** by **%s**%s", bot.Esc(t.DisplayName), status, bot.Esc(track), bot.Esc(artist), heart)
		if album != "" {
			s += " · *" + bot.Esc(album) + "*"
		}
		if prev != nil {
			s += fmt.Sprintf("\nPrevious: **%s** by **%s**", bot.Esc(prev.Name), bot.Esc(prev.Artist.String()))
		}
		resp.Content = s + "\n-# " + footer
	case "oneline":
		verb := "is now listening to"
		if !cur.NowPlaying() {
			verb = "last listened to"
		}
		resp.Content = fmt.Sprintf("**%s** %s **%s** by **%s**%s", bot.Esc(t.DisplayName), verb, bot.Esc(track), bot.Esc(artist), heart)
	default: // embedmini
		desc := line(cur) + heart
		e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+status).WithDescription(desc).WithFooterText(footer)
		if img != "" {
			e = e.WithThumbnail(img)
		}
		resp.Embeds = []discord.Embed{e}
	}
	msg, err := c.Reply(resp)
	if err != nil {
		return err
	}
	if self, err := c.Self(); err == nil {
		addReactions(c, msg, self)
	}
	return nil
}

// ---------------- recent ----------------

func recentCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	filter := strings.ToLower(c.Str("artist"))
	limit := 50
	if filter != "" {
		limit = 200
	}
	rt, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: limit, NoCache: true})
	if err != nil {
		return err
	}
	var lines []string
	for _, tr := range rt.Track {
		if filter != "" && !strings.Contains(strings.ToLower(tr.Artist.String()), filter) {
			continue
		}
		when := "▶️ *now playing*"
		if !tr.NowPlaying() && tr.Timestamp() > 0 {
			when = bot.Rel(time.Unix(tr.Timestamp(), 0))
		}
		lines = append(lines, fmt.Sprintf("%s by **%s** · %s", bot.Link(bot.Trunc(tr.Name, 60), c.B.TrackURL(tr.Artist.String(), tr.Name)), bot.Esc(tr.Artist.String()), when))
	}
	title := "Recent tracks"
	if filter != "" {
		title += " matching \"" + c.Str("artist") + "\""
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+title).
		WithFooterText(bot.N(rt.Attr.Total.Int()) + " total scrobbles")
	if len(rt.Track) > 0 {
		if img := rt.Track[0].Image.Best(); img != "" {
			tmpl = tmpl.WithThumbnail(img)
		}
	}
	if len(lines) == 0 {
		lines = []string{"No matching tracks in your recent history."}
	}
	return c.Paginate(bot.Pages(tmpl, lines, 10))
}

// ---------------- plays / pace / milestone ----------------

// scrobblesSince returns the number of scrobbles since a unix time (0 = all time).
func scrobblesSince(c *bot.Ctx, username string, from int64) (int, error) {
	if from == 0 {
		info, err := c.B.API.UserInfo(c, username)
		if err != nil {
			return 0, err
		}
		return info.Playcount.Int(), nil
	}
	rt, err := c.B.API.RecentTracks(c, username, sonosphere.RecentOpts{Limit: 1, From: from})
	if err != nil {
		return 0, err
	}
	return rt.Attr.Total.Int(), nil
}

func playsCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("alltime")
	n, err := scrobblesSince(c, t.Username, p.From())
	if err != nil {
		return err
	}
	var desc string
	if p.Days == 0 {
		desc = fmt.Sprintf("**%s** has **%s** scrobbles in total.", bot.Esc(t.DisplayName), bot.N(n))
		if info, err := c.B.API.UserInfo(c, t.Username); err == nil && info.Registered.Unixtime > 0 {
			days := max(1, int(time.Since(time.Unix(int64(info.Registered.Unixtime), 0)).Hours()/24))
			desc += fmt.Sprintf("\nThat's about **%s** a day since %s.", bot.N(n/days), bot.Date(time.Unix(int64(info.Registered.Unixtime), 0)))
		}
	} else {
		desc = fmt.Sprintf("**%s** has **%s** scrobbles in the last %s.\nThat's about **%s** a day.",
			bot.Esc(t.DisplayName), bot.N(n), bot.Plural(p.Days, "day"), bot.N(n/p.Days))
	}
	return c.Say("%s", desc)
}

var milestones = []int{1, 100, 500, 1000, 2500, 5000, 10000, 25000, 50000, 75000, 100000, 150000, 200000, 250000, 300000,
	400000, 500000, 750000, 1000000, 1500000, 2000000, 3000000, 5000000}

func paceCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("monthly")
	if p.Days == 0 {
		p = bot.PeriodByName("monthly")
	}
	total, err := scrobblesSince(c, t.Username, 0)
	if err != nil {
		return err
	}
	goal := c.Int("goal", 0)
	if goal == 0 {
		for _, m := range milestones {
			if m > total {
				goal = m
				break
			}
		}
	}
	if goal <= total {
		return c.Say("**%s** already has **%s** scrobbles, more than %s! 🎉", bot.Esc(t.DisplayName), bot.N(total), bot.N(goal))
	}
	recent, err := scrobblesSince(c, t.Username, p.From())
	if err != nil {
		return err
	}
	perDay := float64(recent) / float64(p.Days)
	if perDay <= 0 {
		return bot.Errorf("No scrobbles in the last %s, so there's no pace to measure.", bot.Plural(p.Days, "day"))
	}
	daysLeft := float64(goal-total) / perDay
	eta := time.Now().Add(time.Duration(daysLeft*24) * time.Hour)
	return c.Say("At **%s** scrobbles a day (based on the last %s), **%s** will reach **%s** scrobbles on %s (%s).\n-# %s to go from %s",
		bot.N(int(perDay+0.5)), bot.Plural(p.Days, "day"), bot.Esc(t.DisplayName), bot.N(goal), bot.Date(eta), bot.Rel(eta),
		bot.N(goal-total), bot.N(total))
}

func milestoneCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	rt, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 1, NoCache: true})
	if err != nil {
		return err
	}
	total := rt.Attr.Total.Int()
	if total == 0 {
		return bot.Errorf("**%s** hasn't scrobbled anything yet.", bot.Esc(t.DisplayName))
	}
	n := c.Int("number", 0)
	if n == 0 {
		for _, m := range milestones {
			if m <= total {
				n = m
			}
		}
	}
	if n > total {
		return bot.Errorf("**%s** only has %s scrobbles.", bot.Esc(t.DisplayName), bot.N(total))
	}
	page := total - n + 1
	mt, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 1, Page: page})
	if err != nil {
		return err
	}
	var hit *sonosphere.RecentTrack
	for i := range mt.Track {
		if !mt.Track[i].NowPlaying() {
			hit = &mt.Track[i]
			break
		}
	}
	if hit == nil {
		return bot.Errorf("Couldn't find that scrobble.")
	}
	when := time.Unix(hit.Timestamp(), 0)
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.ColorGold), t, fmt.Sprintf(" · %s scrobble", ordinal(n))).
		WithDescription(fmt.Sprintf("**%s**\nby **%s**", bot.Link(hit.Name, c.B.TrackURL(hit.Artist.String(), hit.Name)), bot.Esc(hit.Artist.String()))).
		WithFooterText("Scrobbled").WithTimestamp(when)
	if a := hit.Album.String(); a != "" {
		e.Description += " | *" + bot.Esc(a) + "*"
	}
	if img := hit.Image.Best(); img != "" {
		e = e.WithThumbnail(img)
	}
	return c.Embed(e)
}

func ordinal(n int) string {
	suffix := "th"
	switch n % 100 {
	case 11, 12, 13:
	default:
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return bot.N(n) + suffix
}

// ---------------- streak ----------------

func streakCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	var tracks []sonosphere.RecentTrack
	first, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 200, NoCache: true})
	if err != nil {
		return err
	}
	if len(first.Track) == 0 {
		return bot.Errorf("**%s** hasn't scrobbled anything yet.", bot.Esc(t.DisplayName))
	}
	tracks = append(tracks, first.Track...)
	head := tracks[0]
	ak, bk, tk := strings.ToLower(head.Artist.String()), strings.ToLower(head.Album.String()), strings.ToLower(head.Name)
	var as, bs, ts int
	aliveA, aliveB, aliveT := true, head.Album.String() != "", true
	var streakStart int64
	page := 1
	for {
		for _, tr := range tracks {
			if aliveA && strings.ToLower(tr.Artist.String()) == ak {
				as++
				if stamp := tr.Timestamp(); stamp > 0 {
					streakStart = stamp
				}
			} else {
				aliveA = false
			}
			if aliveB && strings.ToLower(tr.Album.String()) == bk && strings.ToLower(tr.Artist.String()) == ak {
				bs++
			} else {
				aliveB = false
			}
			if aliveT && strings.ToLower(tr.Name) == tk && strings.ToLower(tr.Artist.String()) == ak {
				ts++
			} else {
				aliveT = false
			}
		}
		if !aliveA || page >= 25 || page >= first.Attr.TotalPages.Int() {
			break
		}
		page++
		next, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 200, Page: page})
		if err != nil || len(next.Track) == 0 {
			break
		}
		tracks = tracks[:0]
		for _, tr := range next.Track {
			if !tr.NowPlaying() {
				tracks = append(tracks, tr)
			}
		}
	}
	var lines []string
	if as > 1 {
		lines = append(lines, fmt.Sprintf("🎤 **Artist:** %s - %s", bot.Link(head.Artist.String(), c.B.ArtistURL(head.Artist.String())), bot.Plural(as, "play")))
	}
	if bs > 1 {
		lines = append(lines, fmt.Sprintf("💿 **Album:** %s - %s", bot.Link(head.Album.String(), c.B.AlbumURL(head.Artist.String(), head.Album.String())), bot.Plural(bs, "play")))
	}
	if ts > 1 {
		lines = append(lines, fmt.Sprintf("🎵 **Track:** %s - %s", bot.Link(head.Name, c.B.TrackURL(head.Artist.String(), head.Name)), bot.Plural(ts, "play")))
	}
	if len(lines) == 0 {
		return c.Say("**%s** has no active streak right now.", bot.Esc(t.DisplayName))
	}
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · streak").WithDescription(strings.Join(lines, "\n"))
	if streakStart > 0 {
		e = e.WithFooterText("Streak started").WithTimestamp(time.Unix(streakStart, 0))
	}
	if aliveA && page >= 25 {
		e.Description += "\n-# Streak goes back further than 5,000 scrobbles."
	}
	return c.Embed(e)
}

// ---------------- overview ----------------

func overviewCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	days := max(1, min(c.Int("days", 4), 8))
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))

	type dayAgg struct {
		plays                   int
		artists, albums, tracks map[string]int
	}
	agg := map[string]*dayAgg{}
	var all []sonosphere.RecentTrack
	for page := 1; page <= 10; page++ {
		rt, err := c.B.API.RecentTracks(c, t.Username, sonosphere.RecentOpts{Limit: 200, Page: page, From: start.Unix()})
		if err != nil {
			return err
		}
		all = append(all, rt.Track...)
		if page >= rt.Attr.TotalPages.Int() || len(rt.Track) == 0 {
			break
		}
	}
	for _, tr := range all {
		if tr.NowPlaying() || tr.Timestamp() == 0 {
			continue
		}
		day := time.Unix(tr.Timestamp(), 0).UTC().Format("2006-01-02")
		d := agg[day]
		if d == nil {
			d = &dayAgg{artists: map[string]int{}, albums: map[string]int{}, tracks: map[string]int{}}
			agg[day] = d
		}
		d.plays++
		d.artists[tr.Artist.String()]++
		if a := tr.Album.String(); a != "" {
			d.albums[a+" by "+tr.Artist.String()]++
		}
		d.tracks[tr.Name+" by "+tr.Artist.String()]++
	}
	top := func(m map[string]int) (string, int) {
		best, n := "", 0
		for k, v := range m {
			if v > n || (v == n && k < best) {
				best, n = k, v
			}
		}
		return best, n
	}
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, fmt.Sprintf(" · %s overview", bot.Plural(days, "day")))
	total := 0
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		d := agg[key]
		name := fmt.Sprintf("%s - %s", day.Format("Monday, Jan 2"), "0 plays")
		if d == nil {
			e = e.AddField(name, "*No scrobbles*", false)
			continue
		}
		total += d.plays
		name = fmt.Sprintf("%s - %s", day.Format("Monday, Jan 2"), bot.Plural(d.plays, "play"))
		ta, tan := top(d.artists)
		tb, tbn := top(d.albums)
		tt, ttn := top(d.tracks)
		var lines []string
		lines = append(lines, fmt.Sprintf("🎤 %s (%d)", bot.Esc(bot.Trunc(ta, 60)), tan))
		if tb != "" {
			lines = append(lines, fmt.Sprintf("💿 %s (%d)", bot.Esc(bot.Trunc(tb, 60)), tbn))
		}
		lines = append(lines, fmt.Sprintf("🎵 %s (%d)", bot.Esc(bot.Trunc(tt, 60)), ttn))
		lines = append(lines, fmt.Sprintf("-# %s · %s",
			bot.Plural(len(d.artists), "artist"), bot.Plural(len(d.tracks), "unique track")))
		e = e.AddField(name, strings.Join(lines, "\n"), false)
	}
	e = e.WithFooterText(fmt.Sprintf("%s in %s · times in UTC", bot.Plural(total, "play"), bot.Plural(days, "day")))
	return c.Embed(e)
}

// ---------------- year / recap ----------------

func yearCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	rng, label := sonosphere.RangeThisYear, fmt.Sprintf("%d so far", time.Now().Year())
	if c.Str("which") == "last" {
		rng, label = sonosphere.RangeYear, fmt.Sprintf("%d", time.Now().Year()-1)
	}
	arts, err := c.B.API.UserStatsArtists(c, t.Username, rng, 10)
	if err != nil {
		return err
	}
	if arts == nil || len(arts.Artists) == 0 {
		return c.Say("No listening data for **%s** in %s yet.", bot.Esc(t.DisplayName), label)
	}
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+label)
	var lines []string
	for i, a := range arts.Artists {
		lines = append(lines, numbered(i, bot.Link(a.ArtistName, c.B.ArtistURL(a.ArtistName)), a.ListenCount))
	}
	e = e.AddField("Top artists", strings.Join(lines, "\n"), false)
	if rg, err := c.B.API.UserStatsReleaseGroups(c, t.Username, rng, 5); err == nil && rg != nil {
		lines = nil
		for i, a := range rg.ReleaseGroups {
			lines = append(lines, numbered(i, bot.Link(a.ReleaseGroupName, c.B.AlbumURL(a.ArtistName, a.ReleaseGroupName))+" by "+bot.Esc(a.ArtistName), a.ListenCount))
		}
		if len(lines) > 0 {
			e = e.AddField("Top albums", strings.Join(lines, "\n"), false)
		}
	}
	if rc, err := c.B.API.UserStatsRecordings(c, t.Username, rng, 5); err == nil && rc != nil {
		lines = nil
		for i, a := range rc.Recordings {
			lines = append(lines, numbered(i, bot.Link(a.TrackName, c.B.TrackURL(a.ArtistName, a.TrackName))+" by "+bot.Esc(a.ArtistName), a.ListenCount))
		}
		if len(lines) > 0 {
			e = e.AddField("Top tracks", strings.Join(lines, "\n"), false)
		}
	}
	footer := bot.Plural(arts.TotalArtistCount, "artist") + " listened to"
	if act, err := c.B.API.UserListeningActivity(c, t.Username, rng); err == nil {
		sum := 0
		for _, a := range act {
			sum += a.ListenCount
		}
		if sum > 0 {
			footer = bot.N(sum) + " scrobbles · " + footer
		}
	}
	return c.Embed(e.WithFooterText(footer))
}

func recapCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	p := c.Period("weekly")
	scrobbles, err := scrobblesSince(c, t.Username, p.From())
	if err != nil {
		return err
	}
	ta, err := c.B.API.TopArtists(c, t.Username, p.LastFM, 15, 1)
	if err != nil {
		return err
	}
	tb, _ := c.B.API.TopAlbums(c, t.Username, p.LastFM, 15, 1)
	tt, _ := c.B.API.TopTracks(c, t.Username, p.LastFM, 15, 1)

	base := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, " · "+p.Label+" recap")
	var pages []discord.Embed

	// Overview page.
	ov := base
	ov = ov.AddField("Scrobbles", bot.N(scrobbles), true)
	ov = ov.AddField("Artists", bot.N(ta.Attr.Total.Int()), true)
	if tb != nil {
		ov = ov.AddField("Albums", bot.N(tb.Attr.Total.Int()), true)
	}
	if tt != nil {
		ov = ov.AddField("Tracks", bot.N(tt.Attr.Total.Int()), true)
	}
	if p.Days > 0 {
		ov = ov.AddField("Per day", bot.N(scrobbles/p.Days), true)
	}
	if len(ta.Artist) > 0 {
		a := ta.Artist[0]
		ov = ov.AddField("Top artist", fmt.Sprintf("%s (%s)", bot.Link(a.Name, c.B.ArtistURL(a.Name)), bot.Plural(a.Playcount.Int(), "play")), false)
	}
	if tb != nil && len(tb.Album) > 0 {
		a := tb.Album[0]
		ov = ov.AddField("Top album", fmt.Sprintf("%s by %s (%s)", bot.Link(a.Name, c.B.AlbumURL(a.Artist.String(), a.Name)), bot.Esc(a.Artist.String()), bot.Plural(a.Playcount.Int(), "play")), false)
		if img := a.Image.Best(); img != "" {
			ov = ov.WithThumbnail(img)
		}
	}
	if tt != nil && len(tt.Track) > 0 {
		a := tt.Track[0]
		ov = ov.AddField("Top track", fmt.Sprintf("%s by %s (%s)", bot.Link(a.Name, c.B.TrackURL(a.Artist.String(), a.Name)), bot.Esc(a.Artist.String()), bot.Plural(a.Playcount.Int(), "play")), false)
	}
	pages = append(pages, ov)

	var lines []string
	for i, a := range ta.Artist {
		lines = append(lines, numbered(i, bot.Link(a.Name, c.B.ArtistURL(a.Name)), a.Playcount.Int()))
	}
	if len(lines) > 0 {
		pages = append(pages, base.WithTitle("Top artists").WithDescription(strings.Join(lines, "\n")))
	}
	if tb != nil {
		lines = nil
		for i, a := range tb.Album {
			lines = append(lines, numbered(i, bot.Link(a.Name, c.B.AlbumURL(a.Artist.String(), a.Name))+" by **"+bot.Esc(a.Artist.String())+"**", a.Playcount.Int()))
		}
		if len(lines) > 0 {
			pages = append(pages, base.WithTitle("Top albums").WithDescription(strings.Join(lines, "\n")))
		}
	}
	if tt != nil {
		lines = nil
		for i, a := range tt.Track {
			lines = append(lines, numbered(i, bot.Link(a.Name, c.B.TrackURL(a.Artist.String(), a.Name))+" by **"+bot.Esc(a.Artist.String())+"**", a.Playcount.Int()))
		}
		if len(lines) > 0 {
			pages = append(pages, base.WithTitle("Top tracks").WithDescription(strings.Join(lines, "\n")))
		}
	}
	// Listening activity per day (ListenBrainz stats).
	if act, err := c.B.API.UserListeningActivity(c, t.Username, p.LBRange); err == nil && len(act) > 0 && len(act) <= 31 {
		maxV := 0
		for _, a := range act {
			maxV = max(maxV, a.ListenCount)
		}
		lines = nil
		sort.SliceStable(act, func(i, j int) bool { return act[i].FromTS < act[j].FromTS })
		for _, a := range act {
			label := time.Unix(a.FromTS, 0).UTC().Format("Jan 02")
			lines = append(lines, fmt.Sprintf("`%s %s` %s", label, bot.Bar(a.ListenCount, maxV, 14), bot.N(a.ListenCount)))
		}
		pages = append(pages, base.WithTitle("Listening activity").WithDescription(strings.Join(lines, "\n")))
	}
	return c.Paginate(pages)
}
