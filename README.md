# sonosbot

A free Discord bot for [Sonosphere](https://sonosphere.fm), written in Go with [disgo](https://github.com/disgoorg/disgo).
It's modelled on .fmbot (the docs in [docs/](docs/)), but it uses the Sonosphere API instead of Last.fm, and **every feature is free**. There are no supporter tiers or premium servers.

* Every command works both as a **slash command** (`/fm`) and as a **text command** (`.fm`, with a configurable prefix).
* **WhoKnows, crowns and server charts** read from a local index of each user's top artists, albums and tracks, so they respond quickly even in large servers.
* **Image charts** (`.chart 5x5 monthly`) are rendered in-process. There's no external renderer.
* All data lives in a single **SQLite** file through a pure-Go driver, so you don't need cgo or a database server.

## How it uses the Sonosphere API

| Sonosphere API | Used for |
|---|---|
| Last.fm-compatible `/2.0/` (API key + shared secret) | Login (desktop token flow), profiles, recent/top lists, artist/album/track info with your play counts, tags, search, love/unlove/scrobble |
| ListenBrainz-compatible `/1/` (no key needed) | `year` (calendar-year stats), the weekly graph on `profile`, and the activity page on `recap` |
| Public now-playing (`/api/user/nowplaying`) | Exposed in the client for future use |

Requests are signed as the [developer docs](https://sonosphere.fm/developers) describe. Read responses are cached briefly, and the client backs off on `429` responses.

## Setup

1. **Create a Discord application** at <https://discord.com/developers/applications>, add a bot, and copy its token.
   On the *Bot* page, enable the privileged intents:
   * **Message Content**: needed for text commands and games. Set `SONOSBOT_TEXT_COMMANDS=false` to run slash-only.
   * **Server Members**: needed for `/refreshmembers` and for removing members who leave. Set `SONOSBOT_MEMBERS_INTENT=false` to turn it off.
2. **Create a Sonosphere application** under *Settings → Applications* on sonosphere.fm. Copy the API key and shared secret.
3. Copy `.env.example` to `.env` and fill it in.
4. Run it:

   ```sh
   go run ./cmd/sonosbot
   # or
   go build -o sonosbot ./cmd/sonosbot && ./sonosbot
   ```

5. Invite the bot with the `bot` and `applications.commands` scopes and these permissions: Send Messages, Embed Links, Attach Files, Add Reactions, Read Message History.
   Slash commands are registered globally on startup, which can take a while to appear. Set `SONOSBOT_DEV_GUILD_ID` while developing to register them to one server instantly.

### Configuration

| Variable | Default | Description |
|---|---|---|
| `DISCORD_TOKEN` | (required) | Bot token |
| `SONOSPHERE_API_KEY` | (required) | Sonosphere API key |
| `SONOSPHERE_SHARED_SECRET` | (required) | Sonosphere shared secret (used for signing) |
| `SONOSBOT_PREFIX` | `.` | Default text command prefix (servers can change theirs with `.prefix`) |
| `SONOSBOT_DB` | `sonosbot.db` | SQLite database path |
| `SONOSBOT_TEXT_COMMANDS` | `true` | Enable text commands (needs the Message Content intent) |
| `SONOSBOT_MEMBERS_INTENT` | `true` | Request the Server Members intent |
| `SONOSBOT_DEV_GUILD_ID` | | Register slash commands to this guild only |
| `SONOSBOT_OWNER_IDS` | | Comma-separated Discord IDs treated as admins everywhere |
| `SONOSBOT_INDEX_MAX_AGE_HOURS` | `48` | How often the background indexer refreshes each user's library |
| `SONOSPHERE_BASE_URL` | `https://sonosphere.fm` | API root (handy for testing) |
| `SONOSBOT_DEBUG` | `false` | Debug logging |

### Docker

```sh
docker build -t sonosbot .
docker run -d --env-file .env -v sonosbot-data:/data sonosbot
```

## Using the bot

* `/login` connects your Sonosphere account. You authorize the bot on sonosphere.fm and it picks up the session automatically.
* Commands that take an artist, album or track default to **what you're listening to now**. Search with `Artist - Title` for albums and tracks.
* Pick another user with a mention, a Discord ID, or `sp:username` (`lfm:username` also works).
* Time periods: `weekly`, `monthly`, `quarterly`, `half`, `yearly`, `alltime` (`w`, `m`, `q`, `h`, `y`, `a`).
* Add `help` after any text command for details, e.g. `.chart help`.

### How WhoKnows works

Each user's top artists (up to 5,000), albums and tracks (3,000 each) are indexed at login and then refreshed in the background every 48 hours. `.update` refreshes yours right away.
When you run a WhoKnows-style command, your own count for that item is fetched live first, so your numbers are always current.
Members are tracked per server when they chat or run commands. Admins can run `/refreshmembers` to sync the full list.

Crowns go to the #1 listener of an artist in a server once they pass the threshold (30 plays by default, change it with `.crownthreshold`). You claim or steal them by running `.whoknows` or `.crown`.

## Commands

🔧 = needs the *Manage Server* permission.

### Account

| Command | Aliases | Description |
|---|---|---|
| `login` |  | Connect your Sonosphere account to the bot |
| `remove` |  | Delete all your data from the bot (friends, settings, crowns) |
| `profile` | `stats` | Shows your or someone else's profile with listening statistics |
| `sonosphere` | `sp`, `lastfm`, `lfm` | Shows a link to your or someone else's Sonosphere profile |
| `settings` |  | Shows an overview of your bot settings |
| `mode` | `md`, `fmmode` | Change how the fm command looks for you |
| `update` | `u`, `index` | Refresh your stored library (used by WhoKnows, crowns and server charts) |
| `refreshmembers` | `refreshserver` | Refresh which members of this server use the bot |
| `help` | `h`, `commands` | Shows all commands, or details for one command |
| `info` | `status`, `about` | Shows information about the bot |
| `shortcuts` (text only) | `sc` | Manage personal text command shortcuts |
| `userreactions` | `ur` | Set up to 5 emoji the bot reacts with on your fm command (empty to clear) |

### Plays

| Command | Aliases | Description |
|---|---|---|
| `fm` | `np`, `nowplaying` | Shows what you or someone else is listening to |
| `recent` | `r`, `recenttracks`, `rt` | Shows your latest scrobbles |
| `plays` | `p`, `scrobbles` | Shows your total scrobble count for a time period |
| `streak` | `str`, `combo` | Shows your current artist, album and track listening streak |
| `pace` | `pc` | Estimates when you'll reach a scrobble goal at your current pace |
| `milestone` | `ms`, `mspl` | Shows a milestone scrobble from your history, e.g. your 10,000th |
| `overview` | `o`, `ov` | Shows a daily overview of your recent listening |
| `year` | `yr`, `lastyear` | Shows your top artists, albums and tracks of this year (or last year) |
| `recap` | `rc`, `wrapped` | A multi-page recap of your listening in a time period |

### Artists

| Command | Aliases | Description |
|---|---|---|
| `artist` | `a`, `ai`, `artistinfo` | Shows information about an artist and your listening |
| `artistplays` | `ap` | Shows how many times you've listened to an artist |
| `artisttracks` | `at`, `att` | Shows your top tracks for an artist |
| `artistalbums` | `aa`, `aab` | Shows your top albums for an artist |
| `topartists` | `ta`, `artists` | Shows your top artists for a time period |
| `artistchart` | `ac`, `achart` | Generates an image chart of your top artists |
| `similar` | `sim`, `similarartists` | Shows artists similar to an artist, marking ones you already know |
| `taste` | `t`, `compare` | Compares your top artists with another user |
| `affinity` | `aff`, `neighbors`, `n` | Shows which members of this server have the most similar taste to you |
| `whoknows` | `wk`, `w` | Shows who in this server listens to an artist |
| `globalwhoknows` | `gwk`, `gw` | Shows who of all bot users listens to an artist |
| `serverartists` | `sa`, `sta` | Shows the top artists of this server |

### Albums

| Command | Aliases | Description |
|---|---|---|
| `album` | `ab`, `abi`, `albuminfo` | Shows information about an album and your listening |
| `albumplays` | `abp` | Shows how many times you've listened to an album |
| `topalbums` | `tab`, `albums`, `tl` | Shows your top albums for a time period |
| `albumtracks` | `abt`, `abtracks` | Shows an album's tracklist with your plays per track |
| `cover` | `co`, `art`, `albumcover` | Shows the cover of an album |
| `chart` | `c`, `albumchart` | Generates an image chart of your top albums |
| `whoknowsalbum` | `wkab`, `wa`, `wkalbum` | Shows who in this server listens to an album |
| `globalwhoknowsalbum` | `gwka`, `gwa`, `gwkalbum` | Shows who of all bot users listens to an album |
| `serveralbums` | `sab`, `stab` | Shows the top albums of this server |

### Tracks

| Command | Aliases | Description |
|---|---|---|
| `track` | `tr`, `ti`, `trackinfo` | Shows information about a track and your listening |
| `trackplays` | `tp` | Shows how many times you've listened to a track |
| `toptracks` | `tt`, `tracks` | Shows your top tracks for a time period |
| `whoknowstrack` | `wktr`, `wt`, `wktrack` | Shows who in this server listens to a track |
| `globalwhoknowstrack` | `gwkt`, `gwt`, `gwktrack` | Shows who of all bot users listens to a track |
| `servertracks` | `st`, `stt` | Shows the top tracks of this server |
| `love` | `l`, `heart` | Loves a track on Sonosphere (defaults to your current track) |
| `unlove` | `ul`, `unheart` | Removes a track from your loved tracks |
| `loved` | `lt`, `lovedtracks` | Shows your loved tracks |
| `scrobble` | `sb` | Scrobbles a track to your Sonosphere account |

### Genres

| Command | Aliases | Description |
|---|---|---|
| `topgenres` | `tg`, `genres`, `toptags` | Shows your top genres, based on the tags of your top artists |
| `genre` | `g`, `tag` | Shows info about a genre and your top artists in it (defaults to your current artist's genres) |
| `whoknowsgenre` | `wg`, `wkg`, `wkgenre` | Shows who in this server listens to a genre the most |

### Friends

| Command | Aliases | Description |
|---|---|---|
| `friends` | `f`, `friendsfm` | Shows what your friends are listening to |
| `addfriends` | `addfriend`, `af` | Adds one or more friends |
| `removefriends` | `removefriend`, `rf`, `deletefriend` | Removes one or more friends |
| `removeallfriends` (text only) | `clearfriends` | Removes all your friends |
| `friendwhoknows` | `fwk`, `fw` | Shows who of your friends listens to an artist |
| `friendwhoknowsalbum` | `fwkab`, `fwa`, `fwkalbum` | Shows who of your friends listens to an album |
| `friendwhoknowstrack` | `fwkt`, `fwt`, `fwktrack` | Shows who of your friends listens to a track |

### Crowns

| Command | Aliases | Description |
|---|---|---|
| `crowns` | `cws` | Shows your crowns in this server (or someone else's) |
| `crown` | `cw` | Shows the crown holder for an artist and the crown history |
| `crownleaderboard` | `cwlb`, `crownlb` | Shows who has the most crowns in this server |
| `togglecrowns` 🔧 |  | Enables or disables crowns in this server |
| `crownthreshold` 🔧 | `cwt` | Sets the minimum plays needed to claim a crown (default 30) |
| `killcrown` 🔧 | `kc` | Removes the crown and history for an artist |
| `killallcrowns` 🔧 |  | Removes every crown in this server |
| `removeusercrowns` 🔧 |  | Removes all crowns of a user in this server |
| `crownblock` 🔧 | `cb` | Blocks a user from getting crowns (they still show in WhoKnows) |

### Games

| Command | Aliases | Description |
|---|---|---|
| `jumble` | `j` | Guess the artist from your library whose name has been jumbled |
| `pixel` | `px`, `pixeljumble` | Guess the album from your library from a pixelated cover |

### Server

| Command | Aliases | Description |
|---|---|---|
| `serversettings` 🔧 | `ss`, `guildsettings` | Shows this server's bot settings |
| `prefix` 🔧 |  | Changes the text command prefix for this server (empty to reset) |
| `block` 🔧 |  | Blocks a user from WhoKnows, server charts and crowns in this server |
| `unblock` 🔧 |  | Unblocks a user from WhoKnows and crowns |
| `blockedmembers` 🔧 | `blocked`, `blockedusers` | Lists blocked members |
| `members` | `mb`, `users` | Lists members of this server who use the bot |
| `togglecommand` 🔧 | `tc`, `disablecommand` | Enables or disables a command in this server (admins can still use it) |
| `serverreactions` 🔧 | `sr` | Sets emoji reactions added to every fm command in this server (empty to clear) |

## Differences from .fmbot

* **No paywalls.** Former supporter and premium-server features are free for everyone: shortcuts, custom reactions, full-size friend lists, and all admin settings.
* **Not included:** features that depend on third-party services or data Sonosphere doesn't provide.
  * Spotify, Apple Music, YouTube, Genius and Discogs integrations
  * Country and Eurovision commands
  * `.judge` (AI)
  * Imports, webhooks and the featured rotation
  * `discoveries`, `gaps`, `lastlistened`, `iceberg` and other commands that need a user's full play-by-play history
* `fm` supports the `embedmini`, `embedfull`, `embedtiny`, `textmini`, `textfull` and `oneline` modes. There are no per-user footer or button customizations yet.

## Development

```sh
go test ./...                                                             # offline tests
SONOSPHERE_LIVE_USER=someuser go test ./internal/sonosphere -run Live -v  # live API smoke test
```

Add `SONOSPHERE_API_KEY` (and the secret) to the live test to also check the `/2.0/` endpoints. That includes `artist.getInfo` returning per-user play counts, which WhoKnows depends on.

```
cmd/sonosbot/         entrypoint & config
internal/sonosphere/  API client (2.0 + ListenBrainz-compatible + now-playing)
internal/store/       SQLite persistence: users, guilds, play index, crowns, friends
internal/bot/         command framework: slash/text parsing, replies, pagination, indexer
internal/commands/    all commands
internal/chart/       image chart & pixel-game rendering
```

Commands are declared once (`bot.Command` with typed `Option`s). The same declaration produces the slash definition and drives the free-form text parser.
