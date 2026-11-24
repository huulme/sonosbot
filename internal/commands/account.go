package commands

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

var fmModes = []string{"embedmini", "embedfull", "embedtiny", "textmini", "textfull", "oneline"}

func accountCommands() []*bot.Command {
	return []*bot.Command{
		{
			Name: "login", Category: "Account", Ephemeral: true,
			Description: "Connect your Sonosphere account to the bot",
			Usage:       []string{"login"},
			Run:         loginCmd,
		},
		{
			Name: "remove", Category: "Account", Ephemeral: true,
			Description: "Delete all your data from the bot (friends, settings, crowns)",
			Options:     []bot.Option{{Name: "confirm", Description: "Confirm deletion", Kind: bot.OptFlag, Required: true}},
			Usage:       []string{"remove confirm"},
			Run:         removeCmd,
		},
		{
			Name: "profile", Aliases: []string{"stats"}, Category: "Account",
			Description: "Shows your or someone else's profile with listening statistics",
			Options:     []bot.Option{optUser},
			Usage:       []string{"profile", "profile @user"},
			Run:         profileCmd,
		},
		{
			Name: "sonosphere", Aliases: []string{"sp", "lastfm", "lfm"}, Category: "Account",
			Description: "Shows a link to your or someone else's Sonosphere profile",
			Options:     []bot.Option{optUser},
			Usage:       []string{"sonosphere", "sp @user"},
			Run: func(c *bot.Ctx) error {
				t, err := c.Target()
				if err != nil {
					return err
				}
				_, err = c.Reply(bot.Response{Content: fmt.Sprintf("**%s**'s Sonosphere profile: <%s>", bot.Esc(t.DisplayName), c.B.API.UserURL(t.Username))})
				return err
			},
		},
		{
			Name: "settings", Category: "Account",
			Description: "Shows an overview of your bot settings",
			Run:         settingsCmd,
		},
		{
			Name: "mode", Aliases: []string{"md", "fmmode"}, Category: "Account",
			Description: "Change how the fm command looks for you",
			Options:     []bot.Option{{Name: "fm-mode", Description: "Embed style for fm", Kind: bot.OptChoice, Choices: fmModes}},
			Usage:       []string{"mode embedfull", "mode textmini"},
			Run:         modeCmd,
		},
		{
			Name: "update", Aliases: []string{"u", "index"}, Category: "Account",
			Description: "Refresh your stored library (used by WhoKnows, crowns and server charts)",
			Run:         updateCmd,
		},
		{
			Name: "refreshmembers", Aliases: []string{"refreshserver"}, Category: "Account", GuildOnly: true,
			Description: "Refresh which members of this server use the bot",
			Run:         refreshMembersCmd,
		},
		{
			Name: "help", Aliases: []string{"h", "commands"}, Category: "Account",
			Description: "Shows all commands, or details for one command",
			Options:     []bot.Option{{Name: "command", Description: "Command or category name", Kind: bot.OptQuery}},
			Usage:       []string{"help", "help whoknows"},
			Run:         helpCmd,
		},
		{
			Name: "info", Aliases: []string{"status", "about"}, Category: "Account",
			Description: "Shows information about the bot",
			Run:         infoCmd,
		},
		{
			Name: "shortcuts", Aliases: []string{"sc"}, Category: "Account", TextOnly: true,
			Description: "Manage personal text command shortcuts",
			Options:     []bot.Option{{Name: "args", Description: "add <shortcut> <command> | remove <shortcut>", Kind: bot.OptQuery}},
			Usage:       []string{"shortcuts", "shortcuts add yo fm textmini", "shortcuts remove yo"},
			Run:         shortcutsCmd,
		},
		{
			Name: "userreactions", Aliases: []string{"ur"}, Category: "Account",
			Description: "Set up to 5 emoji the bot reacts with on your fm command (empty to clear)",
			Options:     []bot.Option{{Name: "emojis", Description: "Emojis separated by spaces", Kind: bot.OptQuery}},
			Usage:       []string{"userreactions 😀 🔥", "userreactions"},
			Run: func(c *bot.Ctx) error {
				u, err := c.Self()
				if err != nil {
					return err
				}
				em := parseEmojis(c.Str("emojis"))
				if len(em) > 5 {
					return bot.Errorf("You can set at most 5 reactions.")
				}
				if err := c.B.Store.SetUserField(c, u.DiscordID, "reactions", strings.Join(em, " ")); err != nil {
					return err
				}
				if len(em) == 0 {
					return c.Success("Your fm reactions have been cleared.")
				}
				return c.Success("Your fm reactions are now: %s", strings.Join(displayEmojis(c.Str("emojis")), " "))
			},
		},
	}
}

// ---------------- login ----------------

func loginCmd(c *bot.Ctx) error {
	token, err := c.B.API.GetToken(c)
	if err != nil {
		return err
	}
	authURL := c.B.API.AuthURL(token)
	embed := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Connect your Sonosphere account").
		WithDescription(fmt.Sprintf("Click the button below to authorize the bot on Sonosphere.\n"+
			"This link is personal, don't share it. It expires in 5 minutes.\n\nNo Sonosphere account yet? [Sign up here](%s/join).", c.B.API.BaseURL))
	row := []discord.LayoutComponent{discord.NewActionRow(discord.NewLinkButton("Connect Sonosphere", authURL))}

	// Text commands: send the link privately.
	var msg *discord.Message
	var dmChannel snowflake.ID
	if !c.Slash {
		dm, err := c.B.Client.Rest.CreateDMChannel(c.Author.ID)
		if err == nil {
			msg, err = c.B.Client.Rest.CreateMessage(dm.ID(), discord.NewMessageCreate().WithEmbeds(embed).WithComponents(row...))
		}
		if err != nil {
			return bot.Errorf("I couldn't DM you the login link. Enable DMs from server members, or use `/login`.")
		}
		dmChannel = dm.ID()
		if c.GuildID != 0 {
			c.Say("📬 I've sent you a login link in your DMs.")
		}
	} else {
		msg, err = c.Reply(bot.Response{Embeds: []discord.Embed{embed}, Components: row})
		if err != nil {
			return err
		}
	}

	done := func(e discord.Embed) {
		if dmChannel != 0 {
			c.B.Client.Rest.UpdateMessage(dmChannel, msg.ID, discord.NewMessageUpdate().WithEmbeds(e).WithComponents())
		} else {
			c.Edit(msg, bot.Response{Embeds: []discord.Embed{e}})
		}
	}

	// Poll for authorization (the command context is short-lived, so use our own).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		tick := time.NewTicker(4 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				done(discord.NewEmbed().WithColor(bot.ColorError).WithDescription(fmt.Sprintf("Login timed out. Run %s to try again.", c.Cmdf("login"))))
				return
			case <-tick.C:
			}
			sess, err := c.B.API.GetSession(ctx, token)
			if err != nil {
				if sonosphere.IsCode(err, sonosphere.ErrUnauthorizedToken) || sonosphere.IsCode(err, sonosphere.ErrAuthFailed) {
					continue
				}
				var ae *sonosphere.APIError
				if errors.As(err, &ae) && ae.Code == sonosphere.ErrInvalidParameters {
					continue
				}
				c.B.Log.Warn("login: getSession", "err", err)
				continue
			}
			changed, err := c.B.Store.UpsertUser(ctx, int64(c.Author.ID), sess.Name, sess.Key)
			if err != nil {
				c.B.Log.Error("login: storing user", "err", err)
				done(discord.NewEmbed().WithColor(bot.ColorError).WithDescription("Something went wrong saving your account. Please try again."))
				return
			}
			c.B.MarkRegistered(int64(c.Author.ID), true)
			if c.GuildID != 0 {
				_ = c.B.Store.TouchGuildUser(ctx, int64(c.GuildID), int64(c.Author.ID), c.AuthorName)
			}
			desc := fmt.Sprintf("✅ Connected to Sonosphere as **%s**!\n\nI'm now indexing your library so WhoKnows and crowns work. Try %s now.",
				bot.Link(sess.Name, c.B.API.UserURL(sess.Name)), c.Cmdf("fm"))
			if !changed {
				desc = fmt.Sprintf("✅ Your session for **%s** has been refreshed.", bot.Esc(sess.Name))
			}
			done(discord.NewEmbed().WithColor(bot.ColorOK).WithDescription(desc))
			if changed {
				if u, err := c.B.Store.User(ctx, int64(c.Author.ID)); err == nil {
					ictx, icancel := context.WithTimeout(context.Background(), 5*time.Minute)
					if err := c.B.IndexUser(ictx, u); err != nil {
						c.B.Log.Warn("login: initial index", "user", sess.Name, "err", err)
					}
					icancel()
				}
			}
			return
		}
	}()
	return nil
}

func removeCmd(c *bot.Ctx) error {
	if _, err := c.Self(); err != nil {
		return err
	}
	if !c.Flag("confirm") {
		return bot.Errorf("This deletes all your data from the bot. Run %s to confirm.", c.Cmdf("remove confirm"))
	}
	if err := c.B.Store.DeleteUser(c, int64(c.Author.ID)); err != nil {
		return err
	}
	c.B.MarkRegistered(int64(c.Author.ID), false)
	return c.Success("All your data has been removed from the bot. Your Sonosphere account itself is untouched.")
}

// ---------------- profile ----------------

func profileCmd(c *bot.Ctx) error {
	t, err := c.Target()
	if err != nil {
		return err
	}
	info, err := c.B.API.UserInfo(c, t.Username)
	if err != nil {
		return err
	}
	e := c.AuthorFor(discord.NewEmbed().WithColor(bot.Accent), t, "'s profile")
	e = e.WithURL(c.B.API.UserURL(info.Name))
	if img := info.Image.Best(); img != "" {
		e = e.WithThumbnail(img)
	}
	reg := time.Unix(int64(info.Registered.Unixtime), 0)
	days := max(1, int(time.Since(reg).Hours()/24))
	var stats []string
	stats = append(stats, fmt.Sprintf("**%s** scrobbles", bot.N(info.Playcount.Int())))
	if info.ArtistCount > 0 {
		stats = append(stats, fmt.Sprintf("**%s** artists", bot.N(info.ArtistCount.Int())))
	}
	if info.AlbumCount > 0 {
		stats = append(stats, fmt.Sprintf("**%s** albums", bot.N(info.AlbumCount.Int())))
	}
	if info.TrackCount > 0 {
		stats = append(stats, fmt.Sprintf("**%s** tracks", bot.N(info.TrackCount.Int())))
	}
	e = e.AddField("Library", strings.Join(stats, "\n"), true)
	if info.Registered.Unixtime > 0 {
		e = e.AddField("Listening since", fmt.Sprintf("%s\n~**%s** scrobbles a day", bot.Date(reg), bot.N(info.Playcount.Int()/days)), true)
	}
	if info.Country != "" && info.Country != "None" {
		e = e.AddField("Country", info.Country, true)
	}

	// Top items all-time.
	var tops []string
	if ta, err := c.B.API.TopArtists(c, info.Name, sonosphere.PeriodOverall, 1, 1); err == nil && len(ta.Artist) > 0 {
		a := ta.Artist[0]
		tops = append(tops, fmt.Sprintf("🎤 %s (%s)", bot.Link(a.Name, c.B.ArtistURL(a.Name)), bot.Plural(a.Playcount.Int(), "play")))
	}
	if tb, err := c.B.API.TopAlbums(c, info.Name, sonosphere.PeriodOverall, 1, 1); err == nil && len(tb.Album) > 0 {
		a := tb.Album[0]
		tops = append(tops, fmt.Sprintf("💿 %s by %s (%s)", bot.Link(a.Name, c.B.AlbumURL(a.Artist.String(), a.Name)), bot.Esc(a.Artist.String()), bot.Plural(a.Playcount.Int(), "play")))
	}
	if tt, err := c.B.API.TopTracks(c, info.Name, sonosphere.PeriodOverall, 1, 1); err == nil && len(tt.Track) > 0 {
		a := tt.Track[0]
		tops = append(tops, fmt.Sprintf("🎵 %s by %s (%s)", bot.Link(a.Name, c.B.TrackURL(a.Artist.String(), a.Name)), bot.Esc(a.Artist.String()), bot.Plural(a.Playcount.Int(), "play")))
	}
	if len(tops) > 0 {
		e = e.AddField("All-time favourites", strings.Join(tops, "\n"), false)
	}

	// Weekly listening graph from the ListenBrainz-compatible stats.
	if act, err := c.B.API.UserListeningActivity(c, info.Name, sonosphere.RangeWeek); err == nil && len(act) > 0 {
		maxV := 0
		for _, a := range act {
			maxV = max(maxV, a.ListenCount)
		}
		var lines []string
		for _, a := range act {
			day := a.TimeRange
			if len(day) > 3 {
				day = day[:3]
			}
			lines = append(lines, fmt.Sprintf("`%s %s` %s", day, bot.Bar(a.ListenCount, maxV, 12), bot.N(a.ListenCount)))
		}
		e = e.AddField("Last week", strings.Join(lines, "\n"), false)
	}

	if t.DiscordID != 0 && c.GuildID != 0 {
		if cr, err := c.B.Store.UserCrowns(c, int64(c.GuildID), t.DiscordID, "playcount"); err == nil && len(cr) > 0 {
			e = e.WithFooterText(fmt.Sprintf("👑 %s in this server", bot.Plural(len(cr), "crown")))
		}
	}
	return c.Embed(e)
}

// ---------------- settings / mode ----------------

func settingsCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	e := discord.NewEmbed().WithTitle("Your settings").WithColor(bot.Accent).
		AddField("Sonosphere account", bot.Link(u.Username, c.B.API.UserURL(u.Username)), true).
		AddField("fm mode", "`"+u.FmMode+"` (change with "+c.Cmdf("mode")+")", true).
		AddField("Session", map[bool]string{true: "✅ connected (love & scrobble work)", false: "⚠️ read-only, run " + c.Cmdf("login")}[u.SessionKey != ""], true)
	idx := "never"
	if u.LastIndexed.Unix() > 0 {
		idx = bot.Rel(u.LastIndexed)
	}
	e = e.AddField("Library last indexed", idx+" (refresh with "+c.Cmdf("update")+")", true)
	if u.Reactions != "" {
		e = e.AddField("fm reactions", displayStored(u.Reactions), true)
	}
	if sc, err := c.B.Store.Shortcuts(c, u.DiscordID); err == nil && len(sc) > 0 {
		e = e.AddField("Shortcuts", bot.Plural(len(sc), "shortcut"), true)
	}
	if fr, err := c.B.Store.Friends(c, u.DiscordID); err == nil {
		e = e.AddField("Friends", bot.Plural(len(fr), "friend"), true)
	}
	if c.GuildID != 0 && c.IsAdmin() {
		e = e.WithFooterText("Server admins: see " + c.Prefix + "serversettings")
	}
	return c.Embed(e)
}

func modeCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	m := c.Str("fm-mode")
	if m == "" {
		return c.Say("Your fm mode is `%s`.\nAvailable modes: %s\nExample: %s", u.FmMode, "`"+strings.Join(fmModes, "`, `")+"`", c.Cmdf("mode embedfull"))
	}
	if err := c.B.Store.SetUserField(c, u.DiscordID, "fm_mode", m); err != nil {
		return err
	}
	return c.Success("Your fm mode is now `%s`.", m)
}

// ---------------- update / members ----------------

func updateCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	msg, err := c.Reply(bot.Response{Embeds: []discord.Embed{discord.NewEmbed().WithColor(bot.Accent).WithDescription("⏳ Updating your library…")}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := c.B.IndexUser(ctx, u); err != nil {
		if errors.Is(err, bot.ErrIndexing) {
			return c.Edit(msg, bot.Response{Embeds: []discord.Embed{discord.NewEmbed().WithColor(bot.ColorNotice).WithDescription("Your library is already being updated, hang tight.")}})
		}
		return err
	}
	arts, _ := c.B.Store.UserArtists(c, u.DiscordID, 1_000_000)
	return c.Edit(msg, bot.Response{Embeds: []discord.Embed{discord.NewEmbed().WithColor(bot.ColorOK).
		WithDescription(fmt.Sprintf("✅ Library updated: **%s** artists indexed.", bot.N(len(arts))))}})
}

func refreshMembersCmd(c *bot.Ctx) error {
	if !c.B.Cfg.MembersIntent {
		return bot.Errorf("Member refresh needs the Server Members intent, which is disabled on this bot. Members are added automatically when they chat or use commands.")
	}
	members := map[int64]string{}
	var after snowflake.ID
	for {
		page, err := c.B.Client.Rest.GetMembers(c.GuildID, 1000, after)
		if err != nil {
			return bot.Errorf("I couldn't fetch the member list: %s", err.Error())
		}
		for _, m := range page {
			if m.User.Bot {
				continue
			}
			members[int64(m.User.ID)] = m.EffectiveName()
			after = m.User.ID
		}
		if len(page) < 1000 {
			break
		}
	}
	n, err := c.B.Store.ReplaceGuildMembers(c, int64(c.GuildID), members)
	if err != nil {
		return err
	}
	return c.Success("Refreshed members: **%s** of %s members use the bot.", bot.N(n), bot.N(len(members)))
}

// ---------------- help / info ----------------

var categoryOrder = []string{"Account", "Plays", "Artists", "Albums", "Tracks", "Genres", "Friends", "Crowns", "Games", "Server"}

func helpCmd(c *bot.Ctx) error {
	q := strings.ToLower(strings.TrimSpace(c.Str("command")))
	if q != "" {
		if cmd, ok := c.B.Lookup(q); ok {
			return c.Embed(commandHelp(c, cmd))
		}
		for _, cat := range categoryOrder {
			if strings.EqualFold(cat, q) {
				var lines []string
				for _, cmd := range c.B.Commands() {
					if cmd.Category == cat {
						lines = append(lines, fmt.Sprintf("%s - %s", cmdName(c, cmd), cmd.Description))
					}
				}
				return c.Embed(discord.NewEmbed().WithTitle(cat + " commands").WithDescription(strings.Join(lines, "\n")))
			}
		}
		return bot.Errorf("No command or category named **%s**.", bot.Esc(q))
	}
	e := discord.NewEmbed().WithTitle("Sonosphere bot · commands").
		WithDescription(fmt.Sprintf("Powered by [Sonosphere](%s). Every feature is free, no supporter tiers.\n"+
			"Get started with %s, then try %s, %s or %s.\nUse %s for details on any command.",
			c.B.API.BaseURL, c.Cmdf("login"), c.Cmdf("fm"), c.Cmdf("whoknows"), c.Cmdf("chart"), c.Cmdf("help <command>")))
	byCat := map[string][]string{}
	for _, cmd := range c.B.Commands() {
		if c.Slash && cmd.TextOnly || !c.Slash && cmd.SlashOnly {
			continue
		}
		byCat[cmd.Category] = append(byCat[cmd.Category], "`"+cmd.Name+"`")
	}
	for _, cat := range categoryOrder {
		if l := byCat[cat]; len(l) > 0 {
			e = e.AddField(cat, strings.Join(l, " "), false)
		}
	}
	return c.Embed(e)
}

func cmdName(c *bot.Ctx, cmd *bot.Command) string {
	if c.Slash && !cmd.TextOnly {
		return "`/" + cmd.Name + "`"
	}
	return c.Cmdf(cmd.Name)
}

func commandHelp(c *bot.Ctx, cmd *bot.Command) discord.Embed {
	e := discord.NewEmbed().WithTitle(cmd.Name).WithDescription(cmd.Description)
	if len(cmd.Aliases) > 0 {
		e = e.AddField("Aliases", "`"+strings.Join(cmd.Aliases, "`, `")+"`", false)
	}
	var opts []string
	for _, o := range cmd.Options {
		s := fmt.Sprintf("• **%s** - %s", o.Name, o.Description)
		switch o.Kind {
		case bot.OptPeriod:
			s += " (`weekly`, `monthly`, `quarterly`, `half`, `yearly`, `alltime`)"
		case bot.OptChoice:
			s += " (`" + strings.Join(o.Choices, "`, `") + "`)"
		case bot.OptFlag:
			if len(o.Aliases) > 0 {
				s += " (text: `" + o.Name + "`/`" + strings.Join(o.Aliases, "`/`") + "`)"
			}
		}
		opts = append(opts, s)
	}
	if len(opts) > 0 {
		e = e.AddField("Options", strings.Join(opts, "\n"), false)
	}
	if len(cmd.Usage) > 0 && !cmd.SlashOnly {
		var ex []string
		for _, u := range cmd.Usage {
			ex = append(ex, "`"+c.Prefix+u+"`")
		}
		if c.Slash {
			ex = nil
			for _, u := range cmd.Usage {
				ex = append(ex, "`"+c.B.Cfg.Prefix+u+"`")
			}
		}
		e = e.AddField("Examples", strings.Join(ex, "\n"), false)
	}
	var where []string
	if !cmd.TextOnly {
		where = append(where, "slash")
	}
	if !cmd.SlashOnly {
		where = append(where, "text")
	}
	return e.WithFooterText("Category: " + cmd.Category + " · Available as " + strings.Join(where, " & "))
}

func infoCmd(c *bot.Ctx) error {
	users, _ := c.B.Store.CountUsers(c)
	guilds := c.B.Client.Caches.GuildsLen()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	e := discord.NewEmbed().WithTitle("Sonosphere bot").
		WithDescription(fmt.Sprintf("A free, open Discord bot for [Sonosphere](%s), written in Go with disgo.\nNo paywalls, every feature is available to everyone.", c.B.API.BaseURL)).
		AddField("Servers", bot.N(guilds), true).
		AddField("Users", bot.N(users), true).
		AddField("Uptime", bot.LongDuration(time.Since(c.B.Start)), true).
		AddField("Gateway latency", c.B.Client.Gateway.Latency().Round(time.Millisecond).String(), true).
		AddField("Memory", fmt.Sprintf("%.1f MB", float64(ms.Alloc)/1e6), true).
		AddField("Go", runtime.Version(), true)
	return c.Embed(e)
}

// ---------------- shortcuts / reactions ----------------

func shortcutsCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	fields := strings.Fields(c.Str("args"))
	if len(fields) == 0 {
		sc, err := c.B.Store.Shortcuts(c, u.DiscordID)
		if err != nil {
			return err
		}
		if len(sc) == 0 {
			return c.Say("You have no shortcuts. Add one with %s", c.Cmdf("shortcuts add <shortcut> <command and options>"))
		}
		keys := make([]string, 0, len(sc))
		for k := range sc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var lines []string
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("`%s%s` → `%s%s`", c.Prefix, k, c.Prefix, sc[k]))
		}
		return c.Embed(discord.NewEmbed().WithTitle("Your shortcuts").WithDescription(strings.Join(lines, "\n")))
	}
	switch strings.ToLower(fields[0]) {
	case "add", "set":
		if len(fields) < 3 {
			return bot.Errorf("Usage: %s", c.Cmdf("shortcuts add <shortcut> <command and options>"))
		}
		in := strings.ToLower(strings.TrimPrefix(fields[1], c.Prefix))
		if _, exists := c.B.Lookup(in); exists {
			return bot.Errorf("`%s` is already a command name.", in)
		}
		out := strings.TrimPrefix(strings.Join(fields[2:], " "), c.Prefix)
		target, _, _ := strings.Cut(out, " ")
		if _, ok := c.B.Lookup(target); !ok {
			return bot.Errorf("`%s` isn't a command.", target)
		}
		sc, _ := c.B.Store.Shortcuts(c, u.DiscordID)
		if len(sc) >= 25 {
			return bot.Errorf("You can have up to 25 shortcuts.")
		}
		if err := c.B.Store.SetShortcut(c, u.DiscordID, in, out); err != nil {
			return err
		}
		return c.Success("Added shortcut `%s%s` → `%s%s`", c.Prefix, in, c.Prefix, out)
	case "remove", "delete", "rm":
		if len(fields) < 2 {
			return bot.Errorf("Usage: %s", c.Cmdf("shortcuts remove <shortcut>"))
		}
		if err := c.B.Store.SetShortcut(c, u.DiscordID, strings.ToLower(fields[1]), ""); err != nil {
			return err
		}
		return c.Success("Removed shortcut `%s`.", fields[1])
	}
	return bot.Errorf("Usage: %s or %s", c.Cmdf("shortcuts add <shortcut> <command>"), c.Cmdf("shortcuts remove <shortcut>"))
}

var customEmojiRe = regexp.MustCompile(`^<a?:(\w+):(\d+)>$`)

// parseEmojis converts input into reaction API identifiers ("name:id" or unicode).
func parseEmojis(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if m := customEmojiRe.FindStringSubmatch(f); m != nil {
			out = append(out, m[1]+":"+m[2])
		} else if !strings.ContainsAny(f, "<>:") {
			out = append(out, f)
		}
	}
	return out
}

func displayEmojis(s string) []string { return strings.Fields(s) }

func displayStored(s string) string {
	var out []string
	for _, e := range strings.Fields(s) {
		if name, id, ok := strings.Cut(e, ":"); ok {
			out = append(out, fmt.Sprintf("<:%s:%s>", name, id))
		} else {
			out = append(out, e)
		}
	}
	return strings.Join(out, " ")
}

// addReactions applies the user's (or else the server's) reactions to a message.
func addReactions(c *bot.Ctx, msg *discord.Message, u *store.User) {
	if msg == nil {
		return
	}
	list := u.Reactions
	if list == "" && c.GuildID != 0 {
		if g, err := c.B.Store.Guild(c, int64(c.GuildID)); err == nil {
			list = g.Reactions
		}
	}
	for _, e := range slices.Compact(strings.Fields(list)) {
		if err := c.B.Client.Rest.AddReaction(msg.ChannelID, msg.ID, e); err != nil {
			c.B.Log.Debug("add reaction", "emoji", e, "err", err)
		}
	}
}
