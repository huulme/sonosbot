package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

type Config struct {
	Token         string
	Prefix        string
	DevGuildID    snowflake.ID // register slash commands to this guild only (instant updates)
	TextCommands  bool         // requires the Message Content privileged intent
	MembersIntent bool         // requires the Server Members privileged intent
	OwnerIDs      []snowflake.ID
	IndexMaxAge   time.Duration
}

// ComponentHandler handles button presses whose custom ID starts with a registered prefix.
type ComponentHandler func(e *events.ComponentInteractionCreate, args []string)

// MessageHook sees every non-bot message first; returning true stops further handling.
type MessageHook func(e *events.MessageCreate) bool

type Bot struct {
	Client *bot.Client
	API    *sonosphere.Client
	Store  *store.Store
	Cfg    Config
	Log    *slog.Logger
	Start  time.Time

	commands []*Command
	byName   map[string]*Command // names + aliases (text)
	bySlash  map[string]*Command

	components map[string]ComponentHandler
	hooksMu    sync.RWMutex
	hooks      map[string]MessageHook

	pages *pageStore

	registered sync.Map // discord user id (int64) -> struct{}
	touched    sync.Map // "guild:user" -> time.Time
	prefixes   sync.Map // guild id -> cachedPrefix

	indexing sync.Map // user id -> struct{} (in-progress index runs)
}

func New(cfg Config, api *sonosphere.Client, st *store.Store, log *slog.Logger) *Bot {
	if cfg.Prefix == "" {
		cfg.Prefix = "."
	}
	if cfg.IndexMaxAge == 0 {
		cfg.IndexMaxAge = 48 * time.Hour
	}
	b := &Bot{
		API: api, Store: st, Cfg: cfg, Log: log, Start: time.Now(),
		byName:     map[string]*Command{},
		bySlash:    map[string]*Command{},
		components: map[string]ComponentHandler{},
		hooks:      map[string]MessageHook{},
		pages:      newPageStore(),
	}
	b.registerPager()
	return b
}

// Register adds commands to the registry.
func (b *Bot) Register(cmds ...*Command) {
	for _, c := range cmds {
		b.commands = append(b.commands, c)
		if !c.SlashOnly {
			b.byName[strings.ToLower(c.Name)] = c
			for _, a := range c.Aliases {
				b.byName[strings.ToLower(a)] = c
			}
		}
		if !c.TextOnly {
			if _, dup := b.bySlash[c.slashName()]; dup {
				panic("duplicate slash command " + c.slashName())
			}
			b.bySlash[c.slashName()] = c
		}
	}
}

func (b *Bot) Commands() []*Command { return b.commands }

func (b *Bot) Lookup(name string) (*Command, bool) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(name, b.Cfg.Prefix), "/"))
	if c, ok := b.byName[name]; ok {
		return c, true
	}
	c, ok := b.bySlash[name]
	return c, ok
}

// OnComponent registers a button handler for custom IDs "prefix:arg1:arg2".
func (b *Bot) OnComponent(prefix string, h ComponentHandler) { b.components[prefix] = h }

// AddHook installs a message hook under a key (e.g. a channel ID for a game).
func (b *Bot) AddHook(key string, h MessageHook) {
	b.hooksMu.Lock()
	b.hooks[key] = h
	b.hooksMu.Unlock()
}

func (b *Bot) RemoveHook(key string) {
	b.hooksMu.Lock()
	delete(b.hooks, key)
	b.hooksMu.Unlock()
}

func (b *Bot) HasHook(key string) bool {
	b.hooksMu.RLock()
	defer b.hooksMu.RUnlock()
	_, ok := b.hooks[key]
	return ok
}

// Open connects to Discord and registers slash commands.
func (b *Bot) Open(ctx context.Context) error {
	intents := []gateway.Intents{gateway.IntentGuilds, gateway.IntentGuildMessages, gateway.IntentDirectMessages}
	if b.Cfg.TextCommands {
		intents = append(intents, gateway.IntentMessageContent)
	}
	if b.Cfg.MembersIntent {
		intents = append(intents, gateway.IntentGuildMembers)
	}
	client, err := disgo.New(b.Cfg.Token,
		bot.WithLogger(b.Log),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(intents...),
			gateway.WithPresenceOpts(gateway.WithListeningActivity("/help · Sonosphere")),
		),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagRoles, cache.FlagChannels, cache.FlagMembers)),
		bot.WithEventListenerFunc(b.onSlash),
		bot.WithEventListenerFunc(b.onComponent),
		bot.WithEventListenerFunc(b.onMessage),
		bot.WithEventListenerFunc(b.onMemberLeave),
		bot.WithEventListenerFunc(func(e *events.Ready) {
			b.Log.Info("connected", "user", e.User.Username, "guilds", len(e.Guilds))
		}),
	)
	if err != nil {
		return err
	}
	b.Client = client

	if err := b.loadRegistered(ctx); err != nil {
		return err
	}
	if err := b.syncCommands(); err != nil {
		b.Log.Error("registering slash commands failed", "err", err)
	}
	return client.OpenGateway(ctx)
}

func (b *Bot) Close(ctx context.Context) {
	if b.Client != nil {
		b.Client.Close(ctx)
	}
}

func (b *Bot) syncCommands() error {
	var creates []discord.ApplicationCommandCreate
	for _, c := range b.commands {
		if c.TextOnly {
			continue
		}
		creates = append(creates, c.SlashCreate())
	}
	sort.Slice(creates, func(i, j int) bool { return creates[i].CommandName() < creates[j].CommandName() })
	if b.Cfg.DevGuildID != 0 {
		_, err := b.Client.Rest.SetGuildCommands(b.Client.ApplicationID, b.Cfg.DevGuildID, creates)
		return err
	}
	_, err := b.Client.Rest.SetGlobalCommands(b.Client.ApplicationID, creates)
	return err
}

func (b *Bot) loadRegistered(ctx context.Context) error {
	rows, err := b.Store.DB.QueryContext(ctx, `SELECT discord_id FROM users`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		b.registered.Store(id, struct{}{})
	}
	return rows.Err()
}

func (b *Bot) MarkRegistered(id int64, ok bool) {
	if ok {
		b.registered.Store(id, struct{}{})
	} else {
		b.registered.Delete(id)
	}
}

func (b *Bot) IsRegistered(id int64) bool {
	_, ok := b.registered.Load(id)
	return ok
}

// touch records that a registered user is a member of a guild (rate limited per pair).
func (b *Bot) touch(guildID snowflake.ID, user discord.User, member *discord.Member) {
	if guildID == 0 || user.Bot || !b.IsRegistered(int64(user.ID)) {
		return
	}
	key := guildID.String() + ":" + user.ID.String()
	if v, ok := b.touched.Load(key); ok && time.Since(v.(time.Time)) < time.Hour {
		return
	}
	b.touched.Store(key, time.Now())
	name := user.EffectiveName()
	if member != nil && member.Nick != nil {
		name = *member.Nick
	}
	if err := b.Store.TouchGuildUser(context.Background(), int64(guildID), int64(user.ID), name); err != nil {
		b.Log.Warn("touch guild user", "err", err)
	}
}

// ---------------- event handlers ----------------

func (b *Bot) onSlash(e *events.ApplicationCommandInteractionCreate) {
	if e.Data.Type() != discord.ApplicationCommandTypeSlash {
		return
	}
	data := e.SlashCommandInteractionData()
	cmd, ok := b.bySlash[data.CommandName()]
	if !ok {
		return
	}
	c := b.newSlashCtx(e, cmd, data)
	go b.run(c)
}

func (b *Bot) onMessage(e *events.MessageCreate) {
	m := e.Message
	if m.Author.Bot || m.WebhookID != nil {
		return
	}
	var gid snowflake.ID
	if e.GuildID != nil {
		gid = *e.GuildID
		b.touch(gid, m.Author, m.Member)
	}

	b.hooksMu.RLock()
	hooks := make([]MessageHook, 0, len(b.hooks))
	for _, h := range b.hooks {
		hooks = append(hooks, h)
	}
	b.hooksMu.RUnlock()
	for _, h := range hooks {
		if h(e) {
			return
		}
	}

	if !b.Cfg.TextCommands {
		return
	}
	prefix := b.PrefixFor(gid)
	content := strings.TrimSpace(m.Content)
	// Mentioning the bot works as a prefix too.
	selfMention := fmt.Sprintf("<@%s>", b.Client.ApplicationID)
	switch {
	case strings.HasPrefix(content, selfMention):
		content = strings.TrimSpace(strings.TrimPrefix(content, selfMention))
		if content == "" {
			content = "help"
		}
	case strings.HasPrefix(content, prefix) && len(content) > len(prefix):
		content = content[len(prefix):]
	default:
		return
	}
	name, rest, _ := strings.Cut(content, " ")
	name = strings.ToLower(name)

	cmd := b.byName[name]
	if cmd == nil {
		cmd, rest = b.expandShortcut(m.Author.ID, name, rest)
		if cmd == nil {
			return
		}
	}
	if strings.EqualFold(strings.TrimSpace(rest), "help") {
		if h, ok := b.byName["help"]; ok {
			c := b.newTextCtx(e, h, name)
			go b.run(c)
			return
		}
	}
	c := b.newTextCtx(e, cmd, rest)
	go b.run(c)
}

// expandShortcut resolves a user's personal shortcut into a command and arguments.
func (b *Bot) expandShortcut(userID snowflake.ID, name, rest string) (*Command, string) {
	if !b.IsRegistered(int64(userID)) {
		return nil, rest
	}
	sc, err := b.Store.Shortcuts(context.Background(), int64(userID))
	if err != nil {
		return nil, rest
	}
	out, ok := sc[name]
	if !ok {
		return nil, rest
	}
	target, args, _ := strings.Cut(out, " ")
	cmd := b.byName[strings.ToLower(target)]
	return cmd, strings.TrimSpace(args + " " + rest)
}

type cachedPrefix struct {
	prefix string
	exp    time.Time
}

// PrefixFor returns the text prefix for a guild (cached for a minute).
func (b *Bot) PrefixFor(guildID snowflake.ID) string {
	if guildID == 0 {
		return b.Cfg.Prefix
	}
	if v, ok := b.prefixes.Load(guildID); ok && time.Now().Before(v.(cachedPrefix).exp) {
		return v.(cachedPrefix).prefix
	}
	p := b.Cfg.Prefix
	if g, err := b.Store.Guild(context.Background(), int64(guildID)); err == nil && g.Prefix != "" {
		p = g.Prefix
	}
	b.prefixes.Store(guildID, cachedPrefix{p, time.Now().Add(time.Minute)})
	return p
}

// InvalidatePrefix drops the cached prefix after it changes.
func (b *Bot) InvalidatePrefix(guildID snowflake.ID) { b.prefixes.Delete(guildID) }

func (b *Bot) onComponent(e *events.ComponentInteractionCreate) {
	id := e.Data.CustomID()
	parts := strings.Split(id, ":")
	h, ok := b.components[parts[0]]
	if !ok {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.Log.Error("component panic", "id", id, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		h(e, parts[1:])
	}()
}

func (b *Bot) onMemberLeave(e *events.GuildMemberLeave) {
	if err := b.Store.RemoveGuildUser(context.Background(), int64(e.GuildID), int64(e.User.ID)); err != nil {
		b.Log.Warn("remove guild user", "err", err)
	}
	b.touched.Delete(e.GuildID.String() + ":" + e.User.ID.String())
}

// run executes a command with panic recovery and uniform error reporting.
func (b *Bot) run(c *Ctx) {
	defer c.cancel()
	defer func() {
		if r := recover(); r != nil {
			b.Log.Error("command panic", "cmd", c.Cmd.Name, "panic", r, "stack", string(debug.Stack()))
			c.Error("Something went wrong while running this command.")
		}
	}()
	if c.Slash {
		if err := c.ev.DeferCreateMessage(c.Cmd.Ephemeral); err != nil {
			b.Log.Warn("defer failed", "err", err)
			return
		}
		c.deferred = true
	} else {
		_ = b.Client.Rest.SendTyping(c.ChannelID)
	}
	if c.GuildID != 0 {
		var member *discord.Member
		if c.member != nil {
			member = c.member
		}
		b.touch(c.GuildID, c.Author, member)
	}
	if (c.Cmd.GuildOnly || c.Cmd.Admin) && c.GuildID == 0 {
		c.Error("This command can only be used in a server.")
		return
	}
	if c.GuildID != 0 {
		if g, err := b.Store.Guild(c, int64(c.GuildID)); err == nil && contains(g.DisabledCmds, c.Cmd.Name) && !c.IsAdmin() {
			c.Error("This command has been disabled in this server.")
			return
		}
	}
	if c.Cmd.Admin && !c.IsAdmin() {
		c.Error("You need the **Manage Server** permission to use this command.")
		return
	}
	start := time.Now()
	err := c.Cmd.Run(c)
	b.Log.Debug("command", "cmd", c.Cmd.Name, "slash", c.Slash, "user", c.Author.ID, "took", time.Since(start), "err", err)
	if err == nil {
		return
	}
	var ue *UserError
	switch {
	case errors.As(err, &ue):
		c.Error(ue.Msg)
	case errors.Is(err, ErrNotRegistered):
		c.NotRegistered()
	case errors.Is(err, context.DeadlineExceeded):
		c.Error("Sonosphere took too long to respond. Please try again in a moment.")
	case sonosphere.IsCode(err, sonosphere.ErrInvalidAPIKey):
		b.Log.Error("sonosphere rejected the API key", "err", err)
		c.Error("The bot's Sonosphere API key is invalid. Please contact the bot owner.")
	case sonosphere.IsNotFound(err):
		c.Error("Sonosphere couldn't find that. Check the spelling and try again.")
	default:
		b.Log.Error("command failed", "cmd", c.Cmd.Name, "err", err)
		c.Error("Something went wrong while talking to Sonosphere. Please try again later.")
	}
}

// UserError is shown verbatim to the user.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func Errorf(format string, a ...any) error { return &UserError{Msg: fmt.Sprintf(format, a...)} }

var ErrNotRegistered = errors.New("not registered")
