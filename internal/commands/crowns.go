package commands

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/store"
)

func crownCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "crowns", Aliases: []string{"cws"}, Category: "Crowns", GuildOnly: true,
			Description: "Shows your crowns in this server (or someone else's)",
			Options: []bot.Option{optUser,
				{Name: "view", Description: "Sort or filter crowns", Kind: bot.OptChoice, Choices: []string{"playcount", "recent", "stolen"}}},
			Usage: []string{"crowns", "crowns @user", "crowns recent"},
			Run:   crownsCmd,
		},
		{
			Name: "crown", Aliases: []string{"cw"}, Category: "Crowns", GuildOnly: true,
			Description: "Shows the crown holder for an artist and the crown history",
			Options:     []bot.Option{optArtist},
			Usage:       []string{"crown", "crown radiohead"},
			Run:         crownCmd,
		},
		{
			Name: "crownleaderboard", Aliases: []string{"cwlb", "crownlb"}, Category: "Crowns", GuildOnly: true,
			Description: "Shows who has the most crowns in this server",
			Run:         crownLeaderboardCmd,
		},
		// ---- crown settings (admin) ----
		{
			Name: "togglecrowns", Category: "Crowns", Admin: true,
			Description: "Enables or disables crowns in this server",
			Run: func(c *bot.Ctx) error {
				g, err := c.B.Store.Guild(c, int64(c.GuildID))
				if err != nil {
					return err
				}
				if err := c.B.Store.SetGuildField(c, int64(c.GuildID), "crowns_enabled", !g.CrownsEnabled); err != nil {
					return err
				}
				if g.CrownsEnabled {
					return c.Success("Crowns are now **disabled** in this server. Existing crowns are kept.")
				}
				return c.Success("Crowns are now **enabled** in this server.")
			},
		},
		{
			Name: "crownthreshold", Aliases: []string{"cwt"}, Category: "Crowns", Admin: true,
			Description: "Sets the minimum plays needed to claim a crown (default 30)",
			Options:     []bot.Option{{Name: "plays", Description: "Minimum plays", Kind: bot.OptInt, Required: true, Min: 1, Max: 100000}},
			Usage:       []string{"crownthreshold 50"},
			Run: func(c *bot.Ctx) error {
				n := c.Int("plays", 0)
				if n < 1 {
					return bot.Errorf("Please provide a number of plays, e.g. %s", c.Cmdf("crownthreshold 50"))
				}
				if err := c.B.Store.SetGuildField(c, int64(c.GuildID), "crown_threshold", n); err != nil {
					return err
				}
				return c.Success("Crown threshold set to **%s**.", bot.Plural(n, "play"))
			},
		},
		{
			Name: "killcrown", Aliases: []string{"kc"}, Category: "Crowns", Admin: true,
			Description: "Removes the crown and history for an artist",
			Options:     []bot.Option{{Name: "artist", Description: "Artist name", Kind: bot.OptQuery, Required: true}},
			Usage:       []string{"killcrown the beatles"},
			Run: func(c *bot.Ctx) error {
				a := c.Str("artist")
				if a == "" {
					return bot.Errorf("Which artist?")
				}
				n, err := c.B.Store.KillCrown(c, int64(c.GuildID), a)
				if err != nil {
					return err
				}
				if n == 0 {
					return bot.Errorf("There is no crown for **%s**.", bot.Esc(a))
				}
				return c.Success("Removed the crown for **%s** (%s cleared).", bot.Esc(a), bot.Plural(int(n), "record"))
			},
		},
		{
			Name: "killallcrowns", Category: "Crowns", Admin: true,
			Description: "Removes every crown in this server",
			Options:     []bot.Option{{Name: "confirm", Description: "Confirm", Kind: bot.OptFlag, Required: true}},
			Usage:       []string{"killallcrowns confirm"},
			Run: func(c *bot.Ctx) error {
				if !c.Flag("confirm") {
					return bot.Errorf("This removes every crown in this server. Run %s to confirm.", c.Cmdf("killallcrowns confirm"))
				}
				n, err := c.B.Store.KillAllCrowns(c, int64(c.GuildID))
				if err != nil {
					return err
				}
				return c.Success("Removed %s.", bot.Plural(int(n), "crown record"))
			},
		},
		{
			Name: "removeusercrowns", Category: "Crowns", Admin: true,
			Description: "Removes all crowns of a user in this server",
			Options:     []bot.Option{{Name: "user", Description: "The user", Kind: bot.OptUser, Required: true}},
			Usage:       []string{"removeusercrowns @user"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				if t.DiscordID == 0 {
					return bot.Errorf("That user doesn't use the bot.")
				}
				n, err := c.B.Store.RemoveUserCrowns(c, int64(c.GuildID), t.DiscordID)
				if err != nil {
					return err
				}
				return c.Success("Removed %s from **%s**.", bot.Plural(int(n), "crown record"), bot.Esc(t.DisplayName))
			},
		},
		{
			Name: "crownblock", Aliases: []string{"cb"}, Category: "Crowns", Admin: true,
			Description: "Blocks a user from getting crowns (they still show in WhoKnows)",
			Options:     []bot.Option{{Name: "user", Description: "The user", Kind: bot.OptUser, Required: true}},
			Run:         func(c *bot.Ctx) error { return setMemberFlag(c, "crown_blocked", true) },
		},
	}
}

func crownsCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	if t.DiscordID == 0 {
		return bot.Errorf("That user doesn't use the bot in this server.")
	}
	view := c.Str("view")
	var crowns []store.Crown
	if view == "stolen" {
		crowns, err = c.B.Store.StolenCrowns(c, int64(c.GuildID), t.DiscordID)
	} else {
		crowns, err = c.B.Store.UserCrowns(c, int64(c.GuildID), t.DiscordID, view)
	}
	if err != nil {
		return err
	}
	var lines []string
	for i, cr := range crowns {
		line := fmt.Sprintf("%d. %s - *%s*", i+1, bot.Link(cr.Artist, c.B.ArtistURL(cr.Artist)), bot.Plural(cr.Playcount, "play"))
		if view == "stolen" {
			line += fmt.Sprintf(" · stolen by **%s** %s", bot.Esc(cr.Username), bot.Rel(cr.UpdatedAt))
		} else {
			line += " · " + bot.Rel(cr.ClaimedAt)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		if view == "stolen" {
			lines = []string{"No crowns have been stolen from this user."}
		} else {
			lines = []string{fmt.Sprintf("No crowns yet. Use %s on artists to claim them.", c.Cmdf("whoknows"))}
		}
	}
	title := " · crowns"
	if view == "stolen" {
		title = " · stolen crowns"
	}
	tmpl := c.AuthorFor(discord.NewEmbed().WithColor(bot.ColorGold), t, title).
		WithFooterText(bot.Plural(len(crowns), "crown"))
	return c.Paginate(bot.Pages(tmpl, lines, 15))
}

func crownCmd(c *bot.Ctx) error {
	self, err := c.Self()
	if err != nil {
		return err
	}
	a, err := resolveArtist(c, "artist", "")
	if err != nil {
		return err
	}
	info, err := c.B.API.ArtistInfo(c, a, self.Username)
	if err != nil {
		return err
	}
	artist := info.Name
	_ = c.B.Store.SetArtistPlays(c, self.DiscordID, artist, info.UserPlays())

	g, err := c.B.Store.Guild(c, int64(c.GuildID))
	if err != nil {
		return err
	}
	e := discord.NewEmbed().WithColor(bot.ColorGold).WithTitle("👑 Crown for " + artist).WithURL(c.B.ArtistURL(artist))
	if img := info.Image.Best(); img != "" {
		e = e.WithThumbnail(img)
	}
	if g.CrownsEnabled {
		if rows, err := c.B.Store.WhoKnowsArtist(c, artist, store.Scope{GuildID: int64(c.GuildID)}, 50); err == nil && len(rows) > 0 {
			if top := firstEligible(c, rows); top != nil {
				res, err := c.B.Store.EvaluateCrown(c, int64(c.GuildID), artist, *top, g.CrownThreshold)
				if err == nil && res.Claimed {
					e = e.WithDescription(fmt.Sprintf("**%s** just claimed this crown!", bot.Esc(nameOf(rows, top.UserID))))
				}
			}
		}
	} else {
		e = e.WithDescription("Crowns are disabled in this server.")
	}
	cur, err := c.B.Store.ActiveCrown(c, int64(c.GuildID), artist)
	if err != nil {
		return err
	}
	if cur == nil {
		e = e.AddField("Holder", fmt.Sprintf("Nobody holds this crown yet. It needs at least %s.", bot.Plural(g.CrownThreshold, "play")), false)
	} else {
		e = e.AddField("Holder", fmt.Sprintf("**%s** with %s, since %s", bot.Esc(cur.Username), bot.Plural(cur.Playcount, "play"), bot.Date(cur.ClaimedAt)), false)
		if cur.UserID != self.DiscordID {
			diff := cur.Playcount - info.UserPlays()
			e = e.AddField("You", fmt.Sprintf("%s · %s behind", bot.Plural(info.UserPlays(), "play"), bot.Plural(max(diff, 0)+1, "play")), false)
		}
	}
	if hist, err := c.B.Store.CrownHistory(c, int64(c.GuildID), artist, 10); err == nil && len(hist) > 1 {
		var lines []string
		for _, h := range hist {
			status := "claimed"
			if !h.Active {
				status = "lost"
			}
			lines = append(lines, fmt.Sprintf("%s **%s** (%s) %s", bot.Date(h.ClaimedAt), bot.Esc(h.Username), bot.Plural(h.Playcount, "play"), status))
		}
		e = e.AddField("History", strings.Join(lines, "\n"), false)
	}
	return c.Embed(e)
}

func crownLeaderboardCmd(c *bot.Ctx) error {
	rows, err := c.B.Store.CrownLeaderboard(c, int64(c.GuildID), 100)
	if err != nil {
		return err
	}
	var lines []string
	for i, r := range rows {
		lines = append(lines, fmt.Sprintf("%s **%s** - %s", bot.Medal(i), bot.Esc(r.Username), bot.Plural(r.Count, "crown")))
	}
	if len(lines) == 0 {
		lines = []string{"No crowns have been claimed in this server yet."}
	}
	return c.Paginate(bot.Pages(discord.NewEmbed().WithColor(bot.ColorGold).WithTitle("👑 Crown leaderboard"), lines, 15))
}

// setMemberFlag (un)sets a guild member flag such as blocked/crown_blocked.
func setMemberFlag(c *bot.Ctx, field string, v bool) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	if t.DiscordID == 0 {
		return bot.Errorf("That user doesn't use the bot.")
	}
	if err := c.B.Store.SetGuildUserFlag(c, int64(c.GuildID), t.DiscordID, field, v); err != nil {
		return err
	}
	what := map[string]string{"blocked": "WhoKnows, server charts and crowns", "crown_blocked": "crowns"}[field]
	if v {
		_, _ = c.B.Store.RemoveUserCrowns(c, int64(c.GuildID), t.DiscordID)
		return c.Success("**%s** is now blocked from %s.", bot.Esc(t.DisplayName), what)
	}
	return c.Success("**%s** has been unblocked.", bot.Esc(t.DisplayName))
}
