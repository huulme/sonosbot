package bot

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

type pager struct {
	pages   []discord.Embed
	owner   snowflake.ID
	expires time.Time
}

type pageStore struct {
	mu   sync.Mutex
	m    map[string]*pager
	next atomic.Int64
}

func newPageStore() *pageStore { return &pageStore{m: map[string]*pager{}} }

func (s *pageStore) put(p *pager) string {
	id := strconv.FormatInt(s.next.Add(1), 36) + strconv.FormatInt(time.Now().UnixNano()%46656, 36)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[id] = p
	return id
}

func (s *pageStore) get(id string) *pager {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	if p == nil || time.Now().After(p.expires) {
		return nil
	}
	return p
}

const pageTTL = 15 * time.Minute

func pageButtons(id string, i, n int) []discord.LayoutComponent {
	if n <= 1 {
		return nil
	}
	btn := func(label, action string, disabled bool) discord.InteractiveComponent {
		return discord.NewSecondaryButton(label, "pg:"+id+":"+action).WithDisabled(disabled)
	}
	return []discord.LayoutComponent{discord.NewActionRow(
		btn("⏮", "first", i == 0),
		btn("◀", "prev", i == 0),
		discord.NewSecondaryButton(fmt.Sprintf("%d/%d", i+1, n), "pg:"+id+":noop").WithDisabled(true),
		btn("▶", "next", i >= n-1),
		btn("⏭", "last", i >= n-1),
	)}
}

// Paginate sends a list of embeds with navigation buttons.
func (c *Ctx) Paginate(pages []discord.Embed) error {
	if len(pages) == 0 {
		return c.Say("Nothing to show.")
	}
	for i := range pages {
		if pages[i].Color == 0 {
			pages[i].Color = Accent
		}
		if len(pages) > 1 {
			footer := fmt.Sprintf("Page %d/%d", i+1, len(pages))
			if pages[i].Footer != nil && pages[i].Footer.Text != "" {
				footer = pages[i].Footer.Text + " · " + footer
			}
			icon := ""
			if pages[i].Footer != nil {
				icon = pages[i].Footer.IconURL
			}
			pages[i] = pages[i].WithFooter(footer, icon)
		}
	}
	id := c.B.pages.put(&pager{pages: pages, owner: c.Author.ID, expires: time.Now().Add(pageTTL)})
	_, err := c.Reply(Response{Embeds: pages[:1], Components: pageButtons(id, 0, len(pages))})
	return err
}

// Pages chunks lines into embeds based on a template embed.
func Pages(tmpl discord.Embed, lines []string, perPage int) []discord.Embed {
	if len(lines) == 0 {
		return []discord.Embed{tmpl.WithDescription("Nothing to show.")}
	}
	var out []discord.Embed
	for i := 0; i < len(lines); i += perPage {
		end := min(i+perPage, len(lines))
		desc := strings.Join(lines[i:end], "\n")
		if tmpl.Description != "" {
			desc = tmpl.Description + "\n\n" + desc
		}
		e := tmpl
		e.Fields = append([]discord.EmbedField(nil), tmpl.Fields...)
		out = append(out, e.WithDescription(Trunc(desc, 4096)))
	}
	return out
}

func (b *Bot) registerPager() {
	b.OnComponent("pg", func(e *events.ComponentInteractionCreate, args []string) {
		if len(args) < 2 {
			return
		}
		p := b.pages.get(args[0])
		if p == nil {
			_ = e.UpdateMessage(discord.NewMessageUpdate().WithComponents())
			return
		}
		if e.User().ID != p.owner {
			_ = e.CreateMessage(discord.NewMessageCreate().WithContent("Only the person who ran this command can change pages.").WithEphemeral(true))
			return
		}
		cur := 0
		if f := e.Message.Embeds; len(f) > 0 && f[0].Footer != nil {
			// Footer ends with "Page i/n".
			txt := f[0].Footer.Text
			if idx := strings.LastIndex(txt, "Page "); idx >= 0 {
				fmt.Sscanf(txt[idx:], "Page %d/", &cur)
				cur--
			}
		}
		n := len(p.pages)
		switch args[1] {
		case "first":
			cur = 0
		case "prev":
			cur--
		case "next":
			cur++
		case "last":
			cur = n - 1
		}
		cur = max(0, min(cur, n-1))
		p.expires = time.Now().Add(pageTTL)
		_ = e.UpdateMessage(discord.NewMessageUpdate().WithEmbeds(p.pages[cur]).WithComponents(pageButtons(args[0], cur, n)...))
	})
}
