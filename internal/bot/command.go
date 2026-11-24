package bot

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"
)

type OptKind int

const (
	// OptQuery is free text. In text commands it is everything left over after
	// all other options have been recognised.
	OptQuery OptKind = iota
	// OptUser selects another user: mention, Discord ID, or Sonosphere username (sp:name).
	OptUser
	// OptPeriod is a time period (weekly, monthly, ...).
	OptPeriod
	// OptSize is a chart size such as 3x3.
	OptSize
	// OptFlag is a boolean switch; in text commands it is enabled by its name or an alias.
	OptFlag
	// OptInt is a number. In text commands it is only parsed when the command has no OptQuery.
	OptInt
	// OptChoice is one of a fixed set of values.
	OptChoice
)

type Option struct {
	Name        string
	Description string
	Kind        OptKind
	Required    bool
	Aliases     []string // OptFlag: text keywords
	Choices     []string // OptChoice
	Min, Max    int      // OptInt
}

type Command struct {
	Name        string
	Aliases     []string
	Description string
	Category    string
	Usage       []string // text examples, without prefix
	Options     []Option
	// SlashOnly / TextOnly restrict where the command is available.
	SlashOnly, TextOnly bool
	// Ephemeral makes slash responses only visible to the invoker.
	Ephemeral bool
	// GuildOnly commands refuse to run in DMs.
	GuildOnly bool
	// Admin commands require the Manage Server permission.
	Admin bool
	Run   func(c *Ctx) error
}

func (c *Command) slashName() string { return strings.ToLower(c.Name) }

func (c *Command) hasKind(k OptKind) bool {
	for _, o := range c.Options {
		if o.Kind == k {
			return true
		}
	}
	return false
}

// SlashCreate builds the slash command definition.
func (c *Command) SlashCreate() discord.SlashCommandCreate {
	var opts []discord.ApplicationCommandOption
	// Discord requires required options first.
	ordered := make([]Option, 0, len(c.Options))
	for _, o := range c.Options {
		if o.Required {
			ordered = append(ordered, o)
		}
	}
	for _, o := range c.Options {
		if !o.Required {
			ordered = append(ordered, o)
		}
	}
	for _, o := range ordered {
		desc := truncate(o.Description, 100)
		switch o.Kind {
		case OptQuery:
			opts = append(opts, discord.ApplicationCommandOptionString{Name: o.Name, Description: desc, Required: o.Required})
		case OptUser:
			opts = append(opts, discord.ApplicationCommandOptionString{Name: o.Name, Description: desc, Required: o.Required})
		case OptPeriod:
			var ch []discord.ApplicationCommandOptionChoiceString
			for _, p := range Periods {
				ch = append(ch, discord.ApplicationCommandOptionChoiceString{Name: p.Label, Value: p.Name})
			}
			opts = append(opts, discord.ApplicationCommandOptionString{Name: o.Name, Description: desc, Required: o.Required, Choices: ch})
		case OptSize:
			opts = append(opts, discord.ApplicationCommandOptionString{Name: o.Name, Description: desc, Required: o.Required})
		case OptFlag:
			opts = append(opts, discord.ApplicationCommandOptionBool{Name: o.Name, Description: desc, Required: o.Required})
		case OptInt:
			io := discord.ApplicationCommandOptionInt{Name: o.Name, Description: desc, Required: o.Required}
			if o.Min != 0 || o.Max != 0 {
				mn, mx := o.Min, o.Max
				io.MinValue, io.MaxValue = &mn, &mx
			}
			opts = append(opts, io)
		case OptChoice:
			var ch []discord.ApplicationCommandOptionChoiceString
			for _, v := range o.Choices {
				ch = append(ch, discord.ApplicationCommandOptionChoiceString{Name: v, Value: v})
			}
			opts = append(opts, discord.ApplicationCommandOptionString{Name: o.Name, Description: desc, Required: o.Required, Choices: ch})
		}
	}
	sc := discord.SlashCommandCreate{
		Name:        c.slashName(),
		Description: truncate(c.Description, 100),
		Options:     opts,
		IntegrationTypes: []discord.ApplicationIntegrationType{
			discord.ApplicationIntegrationTypeGuildInstall,
			discord.ApplicationIntegrationTypeUserInstall,
		},
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
			discord.InteractionContextTypeBotDM,
			discord.InteractionContextTypePrivateChannel,
		},
	}
	if c.GuildOnly || c.Admin {
		sc.IntegrationTypes = []discord.ApplicationIntegrationType{discord.ApplicationIntegrationTypeGuildInstall}
		sc.Contexts = []discord.InteractionContextType{discord.InteractionContextTypeGuild}
	}
	if c.Admin {
		perm := discord.PermissionManageGuild
		sc.DefaultMemberPermissions = omit.NewPtr(perm)
	}
	return sc
}

var (
	mentionRe = regexp.MustCompile(`^<@!?(\d{15,21})>$`)
	idRe      = regexp.MustCompile(`^\d{15,21}$`)
	sizeRe    = regexp.MustCompile(`^(\d{1,2})x(\d{1,2})$`)
)

// parseText splits free-form text arguments into typed options.
func (c *Command) parseText(raw string) map[string]any {
	args := map[string]any{}
	hasQuery := c.hasKind(OptQuery)
	var rest []string
	for _, tok := range strings.Fields(raw) {
		low := strings.ToLower(tok)
		if c.matchToken(tok, low, hasQuery, args) {
			continue
		}
		rest = append(rest, tok)
	}
	if len(rest) > 0 {
		q := strings.Join(rest, " ")
		for _, o := range c.Options {
			if o.Kind == OptQuery {
				args[o.Name] = q
				break
			}
		}
		// Commands without a query but with a user option accept a bare username.
		if !hasQuery {
			for _, o := range c.Options {
				if o.Kind == OptUser {
					if _, set := args[o.Name]; !set {
						args[o.Name] = q
					}
				}
			}
		}
	}
	return args
}

func (c *Command) matchToken(tok, low string, hasQuery bool, args map[string]any) bool {
	for _, o := range c.Options {
		if _, set := args[o.Name]; set && o.Kind != OptFlag {
			continue
		}
		switch o.Kind {
		case OptUser:
			if mentionRe.MatchString(tok) || idRe.MatchString(tok) ||
				strings.HasPrefix(low, "sp:") || strings.HasPrefix(low, "lfm:") {
				args[o.Name] = tok
				return true
			}
		case OptPeriod:
			if p, ok := PeriodByKeyword(low); ok {
				args[o.Name] = p.Name
				return true
			}
		case OptSize:
			if sizeRe.MatchString(low) {
				args[o.Name] = low
				return true
			}
		case OptFlag:
			if low == strings.ToLower(o.Name) || contains(o.Aliases, low) {
				args[o.Name] = true
				return true
			}
		case OptChoice:
			for _, ch := range o.Choices {
				if strings.EqualFold(ch, low) {
					args[o.Name] = ch
					return true
				}
			}
		case OptInt:
			if !hasQuery {
				if n, err := strconv.Atoi(strings.ReplaceAll(low, ",", "")); err == nil {
					args[o.Name] = n
					return true
				}
			}
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
