package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/huulme/sonosbot/internal/store"
)

// Ctx is the per-invocation context passed to command handlers. It embeds a
// context.Context with a timeout so it can be passed straight to API calls.
type Ctx struct {
	context.Context
	cancel context.CancelFunc

	B          *Bot
	Cmd        *Command
	Author     discord.User
	AuthorName string
	GuildID    snowflake.ID // 0 in DMs
	ChannelID  snowflake.ID
	Slash      bool
	Prefix     string // text prefix in use ("/" for slash)

	args   map[string]any
	member *discord.Member
	perms  *discord.Permissions

	ev       *events.ApplicationCommandInteractionCreate
	msg      *discord.Message
	deferred bool
	replied  bool
	origID   snowflake.ID // the original interaction response message
}

func (b *Bot) newSlashCtx(e *events.ApplicationCommandInteractionCreate, cmd *Command, data discord.SlashCommandInteractionData) *Ctx {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	c := &Ctx{
		Context: ctx, cancel: cancel, B: b, Cmd: cmd, Author: e.User(), ChannelID: e.Channel().ID(),
		Slash: true, Prefix: "/", ev: e, args: map[string]any{},
	}
	c.AuthorName = c.Author.EffectiveName()
	if gid := e.GuildID(); gid != nil {
		c.GuildID = *gid
	}
	if m := e.Member(); m != nil {
		c.member = &m.Member
		p := m.Permissions
		c.perms = &p
		if m.Nick != nil {
			c.AuthorName = *m.Nick
		}
	}
	for _, o := range cmd.Options {
		switch o.Kind {
		case OptFlag:
			if v, ok := data.OptBool(o.Name); ok {
				c.args[o.Name] = v
			}
		case OptInt:
			if v, ok := data.OptInt(o.Name); ok {
				c.args[o.Name] = v
			}
		default:
			if v, ok := data.OptString(o.Name); ok && strings.TrimSpace(v) != "" {
				c.args[o.Name] = strings.TrimSpace(v)
			}
		}
	}
	return c
}

func (b *Bot) newTextCtx(e *events.MessageCreate, cmd *Command, raw string) *Ctx {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m := e.Message
	c := &Ctx{
		Context: ctx, cancel: cancel, B: b, Cmd: cmd, Author: m.Author, AuthorName: m.Author.EffectiveName(),
		ChannelID: e.ChannelID, Prefix: b.Cfg.Prefix, msg: &m, member: m.Member,
	}
	if e.GuildID != nil {
		c.GuildID = *e.GuildID
		c.Prefix = b.PrefixFor(c.GuildID)
	}
	if m.Member != nil && m.Member.Nick != nil {
		c.AuthorName = *m.Member.Nick
	}
	c.args = cmd.parseText(strings.TrimSpace(raw))
	return c
}

// ---------------- arguments ----------------

func (c *Ctx) Has(name string) bool { _, ok := c.args[name]; return ok }

func (c *Ctx) Str(name string) string {
	switch v := c.args[name].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

func (c *Ctx) Flag(name string) bool { v, _ := c.args[name].(bool); return v }

func (c *Ctx) Int(name string, def int) int {
	switch v := c.args[name].(type) {
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Period returns the selected time period or the default.
func (c *Ctx) Period(def string) Period {
	for _, o := range c.Cmd.Options {
		if o.Kind == OptPeriod {
			if v := c.Str(o.Name); v != "" {
				if p, ok := PeriodByKeyword(v); ok {
					return p
				}
				return PeriodByName(v)
			}
		}
	}
	return PeriodByName(def)
}

// Size returns a WxH chart size.
func (c *Ctx) Size(defW, defH int) (int, int) {
	for _, o := range c.Cmd.Options {
		if o.Kind == OptSize {
			if m := sizeRe.FindStringSubmatch(strings.ToLower(c.Str(o.Name))); m != nil {
				w, _ := strconv.Atoi(m[1])
				h, _ := strconv.Atoi(m[2])
				return w, h
			}
		}
	}
	return defW, defH
}

// ---------------- users ----------------

// Self returns the invoking user's account or ErrNotRegistered.
func (c *Ctx) Self() (*store.User, error) {
	u, err := c.B.Store.User(c, int64(c.Author.ID))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotRegistered
	}
	return u, err
}

// Target is the user a command operates on.
type Target struct {
	Username    string // Sonosphere username
	DiscordID   int64  // 0 if not a bot user
	DisplayName string
	User        *store.User // nil if not a bot user
	IsSelf      bool
}

// Target resolves the "user" option, falling back to the invoker.
func (c *Ctx) Target() (*Target, error) {
	raw := ""
	for _, o := range c.Cmd.Options {
		if o.Kind == OptUser {
			raw = c.Str(o.Name)
			break
		}
	}
	if raw == "" {
		self, err := c.Self()
		if err != nil {
			return nil, err
		}
		return &Target{Username: self.Username, DiscordID: self.DiscordID, DisplayName: c.AuthorName, User: self, IsSelf: true}, nil
	}
	return c.ResolveUser(raw)
}

// ResolveUser turns a mention, Discord ID, or Sonosphere username into a Target.
func (c *Ctx) ResolveUser(raw string) (*Target, error) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "sp:") || strings.HasPrefix(low, "lfm:") {
		_, name, _ := strings.Cut(raw, ":")
		return c.targetByUsername(name)
	}
	idStr := raw
	if m := mentionRe.FindStringSubmatch(raw); m != nil {
		idStr = m[1]
	}
	if idRe.MatchString(idStr) {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		u, err := c.B.Store.User(c, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, Errorf("That user hasn't connected their Sonosphere account to the bot yet.")
		}
		if err != nil {
			return nil, err
		}
		t := &Target{Username: u.Username, DiscordID: id, DisplayName: u.Username, User: u, IsSelf: id == int64(c.Author.ID)}
		if du, err := c.B.Client.Rest.GetUser(snowflake.ID(id)); err == nil {
			t.DisplayName = du.EffectiveName()
		}
		return t, nil
	}
	return c.targetByUsername(raw)
}

func (c *Ctx) targetByUsername(name string) (*Target, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, Errorf("Please provide a username.")
	}
	if u, err := c.B.Store.UserByUsername(c, name); err == nil {
		return &Target{Username: u.Username, DiscordID: u.DiscordID, DisplayName: u.Username, User: u, IsSelf: u.DiscordID == int64(c.Author.ID)}, nil
	}
	info, err := c.B.API.UserInfo(c, name)
	if err != nil {
		return nil, Errorf("Couldn't find a Sonosphere user named **%s**.", Esc(name))
	}
	return &Target{Username: info.Name, DisplayName: info.Name}, nil
}

// IsAdmin reports whether the invoker can manage the server (or is a bot owner).
func (c *Ctx) IsAdmin() bool {
	for _, id := range c.B.Cfg.OwnerIDs {
		if id == c.Author.ID {
			return true
		}
	}
	if c.GuildID == 0 {
		return false
	}
	if c.perms != nil {
		return c.perms.Has(discord.PermissionManageGuild) || c.perms.Has(discord.PermissionAdministrator)
	}
	if c.member != nil {
		m := *c.member
		m.GuildID = c.GuildID
		m.User = c.Author
		p := c.B.Client.Caches.MemberPermissions(m)
		return p.Has(discord.PermissionManageGuild) || p.Has(discord.PermissionAdministrator)
	}
	return false
}

// ---------------- responses ----------------

type Response struct {
	Content    string
	Embeds     []discord.Embed
	Files      []*discord.File
	Components []discord.LayoutComponent
}

var noMentions = &discord.AllowedMentions{RepliedUser: false}

// Reply sends (or, for the first slash response, fills in) the command response.
func (c *Ctx) Reply(r Response) (*discord.Message, error) {
	if c.Slash {
		if c.deferred && !c.replied {
			c.replied = true
			upd := discord.NewMessageUpdate().WithContent(r.Content).WithEmbeds(r.Embeds...).
				WithComponents(r.Components...).WithAllowedMentions(noMentions)
			if len(r.Files) > 0 {
				upd = upd.WithFiles(r.Files...)
			}
			m, err := c.B.Client.Rest.UpdateInteractionResponse(c.B.Client.ApplicationID, c.ev.Token(), upd)
			if m != nil {
				c.origID = m.ID
			}
			return m, err
		}
		mc := discord.NewMessageCreate().WithContent(r.Content).WithEmbeds(r.Embeds...).
			WithComponents(r.Components...).WithFiles(r.Files...).WithAllowedMentions(noMentions).WithEphemeral(c.Cmd.Ephemeral)
		return c.B.Client.Rest.CreateFollowupMessage(c.B.Client.ApplicationID, c.ev.Token(), mc)
	}
	mc := discord.NewMessageCreate().WithContent(r.Content).WithEmbeds(r.Embeds...).
		WithComponents(r.Components...).WithFiles(r.Files...).WithAllowedMentions(noMentions)
	if !c.replied && c.msg != nil {
		mc = mc.WithMessageReference(&discord.MessageReference{MessageID: &c.msg.ID, ChannelID: &c.ChannelID, FailIfNotExists: false})
	}
	c.replied = true
	return c.B.Client.Rest.CreateMessage(c.ChannelID, mc)
}

// Edit updates a message previously sent with Reply.
func (c *Ctx) Edit(m *discord.Message, r Response) error {
	upd := discord.NewMessageUpdate().WithContent(r.Content).WithEmbeds(r.Embeds...).WithComponents(r.Components...)
	switch {
	case c.Slash && (m == nil || m.ID == c.origID):
		_, err := c.B.Client.Rest.UpdateInteractionResponse(c.B.Client.ApplicationID, c.ev.Token(), upd)
		return err
	case c.Slash:
		_, err := c.B.Client.Rest.UpdateFollowupMessage(c.B.Client.ApplicationID, c.ev.Token(), m.ID, upd)
		return err
	case m == nil:
		return errors.New("no message to edit")
	}
	_, err := c.B.Client.Rest.UpdateMessage(m.ChannelID, m.ID, upd)
	return err
}

func (c *Ctx) Embed(e discord.Embed) error {
	if e.Color == 0 {
		e.Color = Accent
	}
	_, err := c.Reply(Response{Embeds: []discord.Embed{e}})
	return err
}

func (c *Ctx) Say(format string, a ...any) error {
	return c.Embed(discord.NewEmbed().WithDescription(fmt.Sprintf(format, a...)))
}

func (c *Ctx) Success(format string, a ...any) error {
	return c.Embed(discord.NewEmbed().WithColor(ColorOK).WithDescription(fmt.Sprintf(format, a...)))
}

func (c *Ctx) Error(msg string) {
	_, err := c.Reply(Response{Embeds: []discord.Embed{discord.NewEmbed().WithColor(ColorError).WithDescription(msg)}})
	if err != nil {
		c.B.Log.Warn("sending error reply failed", "err", err)
	}
}

func (c *Ctx) NotRegistered() {
	c.Error(fmt.Sprintf("You haven't connected your Sonosphere account yet.\nUse `%slogin` to get started. Don't have an account? Sign up at %s/join",
		c.Prefix, c.B.API.BaseURL))
}

// Cmdf formats a command reference using the active prefix.
func (c *Ctx) Cmdf(name string) string { return "`" + c.Prefix + name + "`" }

// UserLink renders a target's name linked to their Sonosphere profile.
func (c *Ctx) UserLink(t *Target) string {
	return Link(t.DisplayName, c.B.API.UserURL(t.Username))
}

// AuthorFor builds an embed author line for a target.
func (c *Ctx) AuthorFor(e discord.Embed, t *Target, suffix string) discord.Embed {
	name := t.DisplayName
	if !t.IsSelf && !strings.EqualFold(t.DisplayName, t.Username) {
		name = t.DisplayName + " (" + t.Username + ")"
	}
	icon := ""
	if t.IsSelf {
		icon = c.Author.EffectiveAvatarURL()
	}
	return e.WithAuthor(Trunc(name+suffix, 256), c.B.API.UserURL(t.Username), icon)
}
