package commands

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/chart"
	"github.com/huulme/sonosbot/internal/sonosphere"
)

type game struct {
	key      string
	answers  []string // normalised accepted answers
	answer   string   // display answer
	scramble func() string
	winner   chan string
	giveUp   chan struct{}
}

var (
	gamesMu sync.Mutex
	games   = map[string]*game{}
)

func gameCommands(b *bot.Bot) []*bot.Command {
	b.OnComponent("jb", jumbleButton)
	return []*bot.Command{
		{
			Name: "jumble", Aliases: []string{"j"}, Category: "Games",
			Description: "Guess the artist from your library whose name has been jumbled",
			Usage:       []string{"jumble"},
			Run:         jumbleCmd,
		},
		{
			Name: "pixel", Aliases: []string{"px", "pixeljumble"}, Category: "Games",
			Description: "Guess the album from your library from a pixelated cover",
			Usage:       []string{"pixel"},
			Run:         pixelCmd,
		},
	}
}

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalize(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", "and"))
	return nonAlnum.ReplaceAllString(s, "")
}

var parenRe = regexp.MustCompile(`\s*[\(\[].*?[\)\]]\s*`)

func acceptedAnswers(name string) []string {
	set := map[string]bool{normalize(name): true}
	if stripped := strings.TrimSpace(parenRe.ReplaceAllString(name, " ")); stripped != "" {
		set[normalize(stripped)] = true
	}
	if a, _, ok := strings.Cut(name, " - "); ok {
		set[normalize(a)] = true
	}
	if strings.HasPrefix(strings.ToLower(name), "the ") {
		set[normalize(name[4:])] = true
	}
	var out []string
	for k := range set {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// startGame registers a game for the channel; it fails if one is already running.
func startGame(c *bot.Ctx, answer string) (*game, error) {
	key := c.ChannelID.String()
	gamesMu.Lock()
	defer gamesMu.Unlock()
	if _, busy := games[key]; busy {
		return nil, bot.Errorf("A game is already running in this channel.")
	}
	g := &game{key: key, answer: answer, answers: acceptedAnswers(answer), winner: make(chan string, 1), giveUp: make(chan struct{}, 1)}
	games[key] = g
	if !c.B.Cfg.TextCommands {
		// Without message content we can't read answers.
		delete(games, key)
		return nil, bot.Errorf("Games need the Message Content intent, which is disabled on this bot.")
	}
	c.B.AddHook("game:"+key, func(e *events.MessageCreate) bool {
		if e.ChannelID.String() != key {
			return false
		}
		guess := normalize(e.Message.Content)
		for _, a := range g.answers {
			if guess == a {
				_ = c.B.Client.Rest.AddReaction(e.ChannelID, e.MessageID, "✅")
				name := e.Message.Author.EffectiveName()
				if e.Message.Member != nil && e.Message.Member.Nick != nil {
					name = *e.Message.Member.Nick
				}
				select {
				case g.winner <- name:
				default:
				}
				return true
			}
		}
		return false
	})
	return g, nil
}

func endGame(c *bot.Ctx, g *game) {
	c.B.RemoveHook("game:" + g.key)
	gamesMu.Lock()
	delete(games, g.key)
	gamesMu.Unlock()
}

func shuffleName(name string) string {
	words := strings.Fields(strings.ToUpper(name))
	for i, w := range words {
		r := []rune(w)
		for tries := 0; tries < 5; tries++ {
			rand.Shuffle(len(r), func(a, b int) { r[a], r[b] = r[b], r[a] })
			if string(r) != w || len(r) < 2 {
				break
			}
		}
		words[i] = string(r)
	}
	rand.Shuffle(len(words), func(a, b int) { words[a], words[b] = words[b], words[a] })
	return strings.Join(words, " ")
}

func hasLetters(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n >= 3
}

func jumbleCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	type cand struct {
		name  string
		plays int
	}
	var pool []cand
	if arts, err := c.B.Store.UserArtists(c, u.DiscordID, 250); err == nil {
		for _, a := range arts {
			pool = append(pool, cand{a.Name, a.Playcount})
		}
	}
	if len(pool) < 10 {
		r, err := c.B.API.TopArtists(c, u.Username, sonosphere.PeriodOverall, 250, 1)
		if err != nil {
			return err
		}
		pool = pool[:0]
		for _, a := range r.Artist {
			pool = append(pool, cand{a.Name, a.Playcount.Int()})
		}
	}
	var filtered []cand
	for _, p := range pool {
		if hasLetters(p.name) && len([]rune(p.name)) <= 40 {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) < 5 {
		return bot.Errorf("You need to listen to a few more artists before you can play jumble.")
	}
	pick := filtered[rand.IntN(len(filtered))]
	g, err := startGame(c, pick.name)
	if err != nil {
		return err
	}
	defer endGame(c, g)
	g.scramble = func() string { return shuffleName(pick.name) }

	var hints []string
	hints = append(hints, fmt.Sprintf("You have **%s** for this artist", bot.Plural(pick.plays, "play")))
	if info, err := c.B.API.ArtistInfo(c, pick.name, ""); err == nil {
		hints = append(hints, fmt.Sprintf("They have **%s** listeners on Sonosphere", bot.N(info.Stats.Listeners.Int())))
		var tags []string
		for i, t := range info.Tags.Tag {
			if i >= 3 {
				break
			}
			tags = append(tags, strings.ToLower(t.Name))
		}
		if len(tags) > 0 {
			hints = append(hints, "Tags: *"+bot.Esc(strings.Join(tags, ", "))+"*")
		}
		if len(info.Similar.Artist) > 0 {
			hints = append(hints, "Similar to **"+bot.Esc(info.Similar.Artist[0].Name)+"**")
		}
	}
	rand.Shuffle(len(hints), func(a, b int) { hints[a], hints[b] = hints[b], hints[a] })

	const seconds = 30
	deadline := time.Now().Add(seconds * time.Second)
	render := func(scrambled string) discord.Embed {
		return discord.NewEmbed().WithColor(bot.Accent).WithTitle("🔀 Jumble: guess the artist").
			WithDescription(fmt.Sprintf("```\n%s\n```\n%s\n\nType your answer in the chat. Ends %s.", scrambled, "• "+strings.Join(hints, "\n• "), bot.Rel(deadline)))
	}
	buttons := []discord.LayoutComponent{discord.NewActionRow(
		discord.NewSecondaryButton("🔀 Reshuffle", "jb:"+g.key+":shuffle"),
		discord.NewDangerButton("Give up", "jb:"+g.key+":giveup"),
	)}
	msg, err := c.Reply(bot.Response{Embeds: []discord.Embed{render(g.scramble())}, Components: buttons})
	if err != nil {
		return err
	}
	return awaitResult(c, g, msg, seconds*time.Second, func(result string) discord.Embed {
		return discord.NewEmbed().WithColor(bot.ColorOK).WithTitle("🔀 Jumble").
			WithDescription(fmt.Sprintf("The artist was **%s**.\n%s", bot.Link(pick.name, c.B.ArtistURL(pick.name)), result))
	})
}

// awaitResult waits for a winner, give-up or timeout and updates the game message.
func awaitResult(c *bot.Ctx, g *game, msg *discord.Message, d time.Duration, final func(string) discord.Embed) error {
	var result string
	won := false
	select {
	case w := <-g.winner:
		result, won = fmt.Sprintf("🎉 **%s** got it right!", bot.Esc(w)), true
	case <-g.giveUp:
		result = "🏳️ Someone gave up."
	case <-time.After(d):
		result = "⏰ Time's up, nobody guessed it."
	case <-c.Done():
		result = "The game was cancelled."
	}
	e := final(result)
	if !won {
		e = e.WithColor(bot.ColorNotice)
	}
	return c.Edit(msg, bot.Response{Embeds: []discord.Embed{e}})
}

func jumbleButton(e *events.ComponentInteractionCreate, args []string) {
	if len(args) < 2 {
		return
	}
	gamesMu.Lock()
	g := games[args[0]]
	gamesMu.Unlock()
	if g == nil {
		_ = e.UpdateMessage(discord.NewMessageUpdate().WithComponents())
		return
	}
	switch args[1] {
	case "shuffle":
		if len(e.Message.Embeds) == 0 || g.scramble == nil {
			_ = e.DeferUpdateMessage()
			return
		}
		em := e.Message.Embeds[0]
		desc := em.Description
		if start := strings.Index(desc, "```\n"); start >= 0 {
			if end := strings.Index(desc[start+4:], "\n```"); end >= 0 {
				desc = desc[:start+4] + g.scramble() + desc[start+4+end:]
			}
		}
		_ = e.UpdateMessage(discord.NewMessageUpdate().WithEmbeds(em.WithDescription(desc)))
	case "giveup":
		select {
		case g.giveUp <- struct{}{}:
		default:
		}
		_ = e.DeferUpdateMessage()
	}
}

func pixelCmd(c *bot.Ctx) error {
	u, err := c.Self()
	if err != nil {
		return err
	}
	r, err := c.B.API.TopAlbums(c, u.Username, sonosphere.PeriodOverall, 250, 1)
	if err != nil {
		return err
	}
	var pool []sonosphere.TopAlbum
	for _, a := range r.Album {
		if a.Image.Best() != "" && hasLetters(a.Name) {
			pool = append(pool, a)
		}
	}
	if len(pool) < 5 {
		return bot.Errorf("You need more albums with cover art before you can play pixel.")
	}
	pick := pool[rand.IntN(len(pool))]
	g, err := startGame(c, pick.Name)
	if err != nil {
		return err
	}
	defer endGame(c, g)

	stages := []int{5, 10, 20, 40}
	const stageDur = 10 * time.Second
	send := func(i int) (*discord.Message, error) {
		img, err := chart.Pixelate(c, pick.Image.Best(), stages[i])
		if err != nil {
			return nil, err
		}
		e := discord.NewEmbed().WithColor(bot.Accent).WithTitle("🖼️ Pixel: guess the album").
			WithDescription(fmt.Sprintf("Type the album name in the chat. Stage %d/%d, it gets clearer every %d seconds.\n-# You have %s on this album.",
				i+1, len(stages), int(stageDur.Seconds()), bot.Plural(pick.Playcount.Int(), "play"))).
			WithImage("attachment://pixel.jpg")
		if i >= 2 {
			e = e.AddField("Hint", "By **"+bot.Esc(pick.Artist.String())+"**", false)
		}
		return c.Reply(bot.Response{Embeds: []discord.Embed{e}, Files: []*discord.File{discord.NewFile("pixel.jpg", "", bytes.NewReader(img))}})
	}
	msg, err := send(0)
	if err != nil {
		return bot.Errorf("Couldn't load the album cover, try again.")
	}
	var winner string
	for stage := 0; stage < len(stages) && winner == ""; stage++ {
		if stage > 0 {
			if m, err := send(stage); err == nil {
				msg = m
			}
		}
		select {
		case w := <-g.winner:
			winner = w
		case <-time.After(stageDur):
		case <-c.Done():
			return nil
		}
	}
	result := "⏰ Time's up, nobody guessed it."
	color := bot.ColorNotice
	if winner != "" {
		result = fmt.Sprintf("🎉 **%s** got it right!", bot.Esc(winner))
		color = bot.ColorOK
	}
	_ = msg
	e := discord.NewEmbed().WithColor(color).WithTitle("🖼️ Pixel").
		WithDescription(fmt.Sprintf("The album was **%s** by **%s**.\n%s",
			bot.Link(pick.Name, c.B.AlbumURL(pick.Artist.String(), pick.Name)), bot.Esc(pick.Artist.String()), result)).
		WithThumbnail(pick.Image.Best())
	return c.Embed(e)
}
