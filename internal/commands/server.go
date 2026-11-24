package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
)

func serverCommands() []*bot.Command {
	optTarget := bot.Option{Name: "user", Description: "The user", Kind: bot.OptUser, Required: true}
	return []*bot.Command{
		{
			Name: "serversettings", Aliases: []string{"ss", "guildsettings"}, Category: "Server", Admin: true,
			Description: "Shows this server's bot settings",
			Run:         serverSettingsCmd,
		},
		{
			Name: "prefix", Category: "Server", Admin: true,
			Description: "Changes the text command prefix for this server (empty to reset)",
			Options:     []bot.Option{{Name: "prefix", Description: "New prefix, e.g. !", Kind: bot.OptQuery}},
			Usage:       []string{"prefix !", "prefix"},
			Run: func(c *bot.Ctx) error {
				p := strings.TrimSpace(c.Str("prefix"))
				if len([]rune(p)) > 5 || strings.ContainsAny(p, " `") {
					return bot.Errorf("Prefixes can be at most 5 characters and can't contain spaces or backticks.")
				}
				if err := c.B.Store.SetGuildField(c, int64(c.GuildID), "prefix", p); err != nil {
					return err
				}
				c.B.InvalidatePrefix(c.GuildID)
				if p == "" {
					return c.Success("Prefix reset to `%s`.", c.B.Cfg.Prefix)
				}
				return c.Success("Prefix set to `%s`. Example: `%sfm`", p, p)
			},
		},
		{
			Name: "block", Category: "Server", Admin: true,
			Description: "Blocks a user from WhoKnows, server charts and crowns in this server",
			Options:     []bot.Option{optTarget},
			Usage:       []string{"block @user"},
			Run:         func(c *bot.Ctx) error { return setMemberFlag(c, "blocked", true) },
		},
		{
			Name: "unblock", Category: "Server", Admin: true,
			Description: "Unblocks a user from WhoKnows and crowns",
			Options:     []bot.Option{optTarget},
			Usage:       []string{"unblock @user"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				if t.DiscordID == 0 {
					return bot.Errorf("That user doesn't use the bot.")
				}
				for _, f := range []string{"blocked", "crown_blocked"} {
					if err := c.B.Store.SetGuildUserFlag(c, int64(c.GuildID), t.DiscordID, f, false); err != nil {
						return err
					}
				}
				return c.Success("**%s** has been unblocked.", bot.Esc(t.DisplayName))
			},
		},
		{
			Name: "blockedmembers", Aliases: []string{"blocked", "blockedusers"}, Category: "Server", Admin: true,
			Description: "Lists blocked members",
			Run: func(c *bot.Ctx) error {
				members, err := c.B.Store.GuildMembers(c, int64(c.GuildID))
				if err != nil {
					return err
				}
				var lines []string
				for _, m := range members {
					switch {
					case m.Blocked:
						lines = append(lines, fmt.Sprintf("<@%d> (%s) - fully blocked", m.UserID, bot.Esc(m.Username)))
					case m.CrownBlocked:
						lines = append(lines, fmt.Sprintf("<@%d> (%s) - crowns only", m.UserID, bot.Esc(m.Username)))
					}
				}
				if len(lines) == 0 {
					lines = []string{"No blocked members."}
				}
				return c.Paginate(bot.Pages(discord.NewEmbed().WithTitle("Blocked members"), lines, 20))
			},
		},
		{
			Name: "members", Aliases: []string{"mb", "users"}, Category: "Server", GuildOnly: true,
			Description: "Lists members of this server who use the bot",
			Run: func(c *bot.Ctx) error {
				members, err := c.B.Store.GuildMembers(c, int64(c.GuildID))
				if err != nil {
					return err
				}
				var lines []string
				for _, m := range members {
					name := m.DisplayName
					if name == "" {
						name = m.Username
					}
					lines = append(lines, fmt.Sprintf("%s · %s", bot.Esc(name), bot.Link(m.Username, c.B.API.UserURL(m.Username))))
				}
				if len(lines) == 0 {
					lines = []string{"No members registered yet."}
				}
				tmpl := discord.NewEmbed().WithTitle("Members using the bot").WithFooterText(bot.Plural(len(members), "member"))
				return c.Paginate(bot.Pages(tmpl, lines, 20))
			},
		},
		{
			Name: "togglecommand", Aliases: []string{"tc", "disablecommand"}, Category: "Server", Admin: true,
			Description: "Enables or disables a command in this server (admins can still use it)",
			Options:     []bot.Option{{Name: "command", Description: "Command name", Kind: bot.OptQuery, Required: true}},
			Usage:       []string{"togglecommand chart"},
			Run: func(c *bot.Ctx) error {
				cmd, ok := c.B.Lookup(c.Str("command"))
				if !ok {
					return bot.Errorf("No command named **%s**.", bot.Esc(c.Str("command")))
				}
				if cmd.Admin || cmd.Name == "togglecommand" || cmd.Name == "help" {
					return bot.Errorf("That command can't be disabled.")
				}
				g, err := c.B.Store.Guild(c, int64(c.GuildID))
				if err != nil {
					return err
				}
				list := g.DisabledCmds
				enabled := slices.Contains(list, cmd.Name)
				if enabled {
					list = slices.DeleteFunc(list, func(s string) bool { return s == cmd.Name })
				} else {
					list = append(list, cmd.Name)
				}
				if err := c.B.Store.SetGuildField(c, int64(c.GuildID), "disabled_cmds", strings.Join(list, ",")); err != nil {
					return err
				}
				if enabled {
					return c.Success("`%s` is now **enabled** in this server.", cmd.Name)
				}
				return c.Success("`%s` is now **disabled** in this server.", cmd.Name)
			},
		},
		{
			Name: "serverreactions", Aliases: []string{"sr"}, Category: "Server", Admin: true,
			Description: "Sets emoji reactions added to every fm command in this server (empty to clear)",
			Options:     []bot.Option{{Name: "emojis", Description: "Emojis separated by spaces", Kind: bot.OptQuery}},
			Usage:       []string{"serverreactions 🔥 👀", "serverreactions"},
			Run: func(c *bot.Ctx) error {
				em := parseEmojis(c.Str("emojis"))
				if len(em) > 5 {
					return bot.Errorf("You can set at most 5 reactions.")
				}
				if err := c.B.Store.SetGuildField(c, int64(c.GuildID), "reactions", strings.Join(em, " ")); err != nil {
					return err
				}
				if len(em) == 0 {
					return c.Success("Server reactions cleared.")
				}
				return c.Success("Server reactions set to %s. Users' own reactions take priority.", c.Str("emojis"))
			},
		},
	}
}

func serverSettingsCmd(c *bot.Ctx) error {
	g, err := c.B.Store.Guild(c, int64(c.GuildID))
	if err != nil {
		return err
	}
	members, _ := c.B.Store.GuildMembers(c, int64(c.GuildID))
	blocked := 0
	for _, m := range members {
		if m.Blocked || m.CrownBlocked {
			blocked++
		}
	}
	prefix := g.Prefix
	if prefix == "" {
		prefix = c.B.Cfg.Prefix + " (default)"
	}
	crowns := fmt.Sprintf("Enabled · threshold %s", bot.Plural(g.CrownThreshold, "play"))
	if !g.CrownsEnabled {
		crowns = "Disabled"
	}
	disabled := "None"
	if len(g.DisabledCmds) > 0 {
		disabled = "`" + strings.Join(g.DisabledCmds, "`, `") + "`"
	}
	reactions := "None"
	if g.Reactions != "" {
		reactions = displayStored(g.Reactions)
	}
	e := discord.NewEmbed().WithTitle("Server settings").WithColor(bot.Accent).
		AddField("Prefix", "`"+prefix+"` · "+c.Cmdf("prefix"), true).
		AddField("Crowns", crowns+"\n"+c.Cmdf("togglecrowns")+" "+c.Cmdf("crownthreshold"), true).
		AddField("Members", fmt.Sprintf("%s registered · %s blocked", bot.N(len(members)), bot.N(blocked)), true).
		AddField("Disabled commands", disabled+"\n"+c.Cmdf("togglecommand"), false).
		AddField("fm reactions", reactions+" · "+c.Cmdf("serverreactions"), false)
	return c.Embed(e)
}
