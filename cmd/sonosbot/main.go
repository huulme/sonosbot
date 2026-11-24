// Command sonosbot runs the Sonosphere Discord bot.
package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/huulme/sonosbot/internal/bot"
	"github.com/huulme/sonosbot/internal/commands"
	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

func main() {
	loadDotEnv(".env")

	level := slog.LevelInfo
	if envBool("SONOSBOT_DEBUG", false) {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	token := os.Getenv("DISCORD_TOKEN")
	apiKey := os.Getenv("SONOSPHERE_API_KEY")
	secret := os.Getenv("SONOSPHERE_SHARED_SECRET")
	if token == "" || apiKey == "" || secret == "" {
		log.Error("DISCORD_TOKEN, SONOSPHERE_API_KEY and SONOSPHERE_SHARED_SECRET must be set (see .env.example)")
		os.Exit(1)
	}

	st, err := store.Open(envStr("SONOSBOT_DB", "sonosbot.db"))
	if err != nil {
		log.Error("opening database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	api := sonosphere.New(apiKey, secret)
	if base := os.Getenv("SONOSPHERE_BASE_URL"); base != "" {
		api.BaseURL = strings.TrimRight(base, "/")
	}

	cfg := bot.Config{
		Token:         token,
		Prefix:        envStr("SONOSBOT_PREFIX", "."),
		TextCommands:  envBool("SONOSBOT_TEXT_COMMANDS", true),
		MembersIntent: envBool("SONOSBOT_MEMBERS_INTENT", true),
		IndexMaxAge:   time.Duration(envInt("SONOSBOT_INDEX_MAX_AGE_HOURS", 48)) * time.Hour,
	}
	if id := os.Getenv("SONOSBOT_DEV_GUILD_ID"); id != "" {
		cfg.DevGuildID = snowflake.MustParse(id)
	}
	for _, id := range strings.FieldsFunc(os.Getenv("SONOSBOT_OWNER_IDS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		if sf, err := snowflake.Parse(id); err == nil {
			cfg.OwnerIDs = append(cfg.OwnerIDs, sf)
		}
	}

	b := bot.New(cfg, api, st, log)
	commands.All(b)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := b.Open(ctx); err != nil {
		log.Error("connecting to Discord", "err", err)
		os.Exit(1)
	}
	defer b.Close(context.Background())

	go b.RunIndexer(ctx)

	log.Info("bot is running, press Ctrl+C to stop", "commands", len(b.Commands()), "prefix", cfg.Prefix)
	<-ctx.Done()
	log.Info("shutting down")
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(k))
	if err != nil {
		return def
	}
	return v
}

func envInt(k string, def int) int {
	v, err := strconv.Atoi(os.Getenv(k))
	if err != nil {
		return def
	}
	return v
}

// loadDotEnv reads KEY=VALUE lines from a file without overriding the environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
