package commands

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/huulme/sonosbot/internal/bot"
)

var slashNameRe = regexp.MustCompile(`^[-_\p{Ll}\p{N}]{1,32}$`)

func TestCommandDefinitions(t *testing.T) {
	b := bot.New(bot.Config{Prefix: "."}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	All(b) // panics on duplicate slash names

	slash := 0
	textNames := map[string]string{}
	for _, c := range b.Commands() {
		if c.Run == nil {
			t.Errorf("%s has no handler", c.Name)
		}
		if c.Category == "" || c.Description == "" {
			t.Errorf("%s is missing category or description", c.Name)
		}
		if !c.SlashOnly {
			for _, n := range append([]string{c.Name}, c.Aliases...) {
				n = strings.ToLower(n)
				if prev, dup := textNames[n]; dup {
					t.Errorf("text name %q used by both %s and %s", n, prev, c.Name)
				}
				textNames[n] = c.Name
			}
		}
		if c.TextOnly {
			continue
		}
		slash++
		sc := c.SlashCreate()
		if !slashNameRe.MatchString(sc.Name) {
			t.Errorf("invalid slash name %q", sc.Name)
		}
		if l := len([]rune(sc.Description)); l < 1 || l > 100 {
			t.Errorf("%s: description length %d", sc.Name, l)
		}
		if len(sc.Options) > 25 {
			t.Errorf("%s: too many options", sc.Name)
		}
		seenOptional := false
		names := map[string]bool{}
		for _, o := range sc.Options {
			var name, desc string
			var required bool
			switch v := o.(type) {
			case discord.ApplicationCommandOptionString:
				name, desc, required = v.Name, v.Description, v.Required
				if len(v.Choices) > 25 {
					t.Errorf("%s/%s: too many choices", sc.Name, name)
				}
			case discord.ApplicationCommandOptionBool:
				name, desc, required = v.Name, v.Description, v.Required
			case discord.ApplicationCommandOptionInt:
				name, desc, required = v.Name, v.Description, v.Required
			default:
				t.Errorf("%s: unexpected option type %T", sc.Name, o)
				continue
			}
			if !slashNameRe.MatchString(name) || names[name] {
				t.Errorf("%s: invalid or duplicate option name %q", sc.Name, name)
			}
			names[name] = true
			if l := len([]rune(desc)); l < 1 || l > 100 {
				t.Errorf("%s/%s: description length %d", sc.Name, name, l)
			}
			if required && seenOptional {
				t.Errorf("%s: required option %s after optional", sc.Name, name)
			}
			if !required {
				seenOptional = true
			}
		}
	}
	if slash > 100 {
		t.Errorf("%d slash commands; Discord allows at most 100 global commands", slash)
	}
	t.Logf("%d commands (%d slash), %d text names", len(b.Commands()), slash, len(textNames))
}

func TestAcceptedAnswers(t *testing.T) {
	ans := acceptedAnswers("The Beatles")
	want := map[string]bool{"thebeatles": true, "beatles": true}
	for _, a := range ans {
		delete(want, a)
	}
	if len(want) != 0 {
		t.Errorf("missing answers %v in %v", want, ans)
	}
	ans = acceptedAnswers("OK Computer (Deluxe Edition)")
	found := false
	for _, a := range ans {
		if a == "okcomputer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected okcomputer in %v", ans)
	}
	if normalize("AC/DC") != "acdc" || normalize("Simon & Garfunkel") != "simonandgarfunkel" {
		t.Error("normalize mismatch")
	}
}

func TestSplitPair(t *testing.T) {
	a, b, ok := splitPair("Radiohead - OK Computer")
	if !ok || a != "Radiohead" || b != "OK Computer" {
		t.Errorf("got %q %q %v", a, b, ok)
	}
	if _, _, ok := splitPair("Jay-Z"); ok {
		t.Error("hyphen without spaces should not split")
	}
}
