package commands

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/sonosphere"
)

const maxFriends = 100

func friendCommands() []*bot.Command {
	optNames := bot.Option{Name: "users", Description: "Usernames or mentions, separated by spaces", Kind: bot.OptQuery, Required: true}
	return []*bot.Command{
		{
			Name: "friends", Aliases: []string{"f", "friendsfm"}, Category: "Friends",
			Description: "Shows what your friends are listening to",
			Usage:       []string{"friends"},
			Run:         friendsCmd,
		},
		{
			Name: "addfriends", Aliases: []string{"addfriend", "af"}, Category: "Friends",
			Description: "Adds one or more friends",
			Options:     []bot.Option{optNames},
			Usage:       []string{"addfriends someuser", "addfriends @user1 user2"},
			Run:         func(c *bot.Ctx) error { return editFriends(c, true) },
		},
		{
			Name: "removefriends", Aliases: []string{"removefriend", "rf", "deletefriend"}, Category: "Friends",
			Description: "Removes one or more friends",
			Options:     []bot.Option{optNames},
			Usage:       []string{"removefriends someuser"},
			Run:         func(c *bot.Ctx) error { return editFriends(c, false) },
		},
		{
			Name: "removeallfriends", Aliases: []string{"clearfriends"}, Category: "Friends", TextOnly: true,
			Description: "Removes all your friends",
			Run: func(c *bot.Ctx) error {
				u, err := c.Self()
				if err != nil {
					return err
				}
				if err := c.B.Store.ClearFriends(c, u.DiscordID); err != nil {
					return err
				}
				return c.Success("All your friends have been removed.")
			},
		},
	}
}

func friendsCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	names, err := c.B.Store.Friends(c, u.DiscordID)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return bot.Errorf("You haven't added any friends yet. Use %s.", c.Cmdf("addfriends <username>"))
	}
	type row struct {
		name string
		tr   *sonosphere.RecentTrack
		err  bool
	}
	rows := make([]row, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			rows[i].name = n
			rt, err := c.B.API.RecentTracks(c, n, sonosphere.RecentOpts{Limit: 1})
			if err != nil || len(rt.Track) == 0 {
				rows[i].err = true
				return
			}
			rows[i].tr = &rt.Track[0]
		}(i, n)
	}
	wg.Wait()
	// Now playing first, then most recent.
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := rows[i].tr, rows[j].tr
		if ti == nil || tj == nil {
			return tj == nil && ti != nil
		}
		if ti.NowPlaying() != tj.NowPlaying() {
			return ti.NowPlaying()
		}
		return ti.Timestamp() > tj.Timestamp()
	})
	var lines []string
	for _, r := range rows {
		name := bot.Link(r.name, c.B.API.UserURL(r.name))
		if r.tr == nil {
			lines = append(lines, fmt.Sprintf("**%s** · *no scrobbles or private*", name))
			continue
		}
		when := "▶️"
		if !r.tr.NowPlaying() {
			when = bot.Rel(time.Unix(r.tr.Timestamp(), 0))
		}
		lines = append(lines, fmt.Sprintf("**%s** · %s by %s · %s", name, bot.Link(bot.Trunc(r.tr.Name, 50), c.B.TrackURL(r.tr.Artist.String(), r.tr.Name)),
			bot.Esc(bot.Trunc(r.tr.Artist.String(), 40)), when))
	}
	tmpl := discord.NewEmbed().WithColor(bot.Accent).WithTitle("Last songs for " + c.AuthorName + "'s friends").
		WithFooterText(bot.Plural(len(names), "friend"))
	return c.Paginate(bot.Pages(tmpl, lines, 12))
}

func editFriends(c *bot.Ctx, add bool) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	existing, err := c.B.Store.Friends(c, u.DiscordID)
	if err != nil {
		return err
	}
	var done, failed []string
	for _, tok := range strings.Fields(c.Str("users")) {
		t, err := c.ResolveUser(tok)
		if err != nil {
			failed = append(failed, tok)
			continue
		}
		if strings.EqualFold(t.Username, u.Username) {
			failed = append(failed, tok+" (that's you)")
			continue
		}
		if add {
			if len(existing)+len(done) >= maxFriends {
				failed = append(failed, tok+fmt.Sprintf(" (max %d friends)", maxFriends))
				continue
			}
			ok, err := c.B.Store.AddFriend(c, u.DiscordID, t.Username)
			if err != nil {
				return err
			}
			if ok {
				done = append(done, t.Username)
			} else {
				failed = append(failed, t.Username+" (already a friend)")
			}
		} else {
			ok, err := c.B.Store.RemoveFriend(c, u.DiscordID, t.Username)
			if err != nil {
				return err
			}
			if ok {
				done = append(done, t.Username)
			} else {
				failed = append(failed, t.Username+" (not a friend)")
			}
		}
	}
	var parts []string
	verb := "Added"
	if !add {
		verb = "Removed"
	}
	if len(done) > 0 {
		parts = append(parts, fmt.Sprintf("✅ %s %s", verb, bot.Esc(strings.Join(done, ", "))))
	}
	if len(failed) > 0 {
		parts = append(parts, "⚠️ Couldn't process: "+bot.Esc(strings.Join(failed, ", ")))
	}
	if len(parts) == 0 {
		return bot.Errorf("Please provide one or more usernames.")
	}
	return c.Say("%s", strings.Join(parts, "\n"))
}
