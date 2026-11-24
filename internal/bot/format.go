package bot

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/huulme/sonosbot/internal/sonosphere"
)

// Accent is the default embed colour (Sonosphere blue).
const (
	Accent      = 0x2438D6
	ColorError  = 0xD7263D
	ColorOK     = 0x2BB673
	ColorGold   = 0xFFCF33
	ColorNotice = 0xF4A261
)

type Period struct {
	Name    string // canonical name, used as slash option value
	Label   string
	LastFM  string // user.getTop* period
	LBRange string // ListenBrainz stats range
	Days    int    // 0 = all time
}

var Periods = []Period{
	{"weekly", "Weekly", sonosphere.Period7Day, sonosphere.RangeWeek, 7},
	{"monthly", "Monthly", sonosphere.Period1Month, sonosphere.RangeMonth, 30},
	{"quarterly", "Quarterly", sonosphere.Period3Month, sonosphere.RangeQuarter, 90},
	{"half", "Half-yearly", sonosphere.Period6Month, sonosphere.RangeHalfYear, 180},
	{"yearly", "Yearly", sonosphere.Period12Month, sonosphere.RangeYear, 365},
	{"alltime", "All-time", sonosphere.PeriodOverall, sonosphere.RangeAllTime, 0},
}

var periodKeywords = map[string]string{
	"weekly": "weekly", "week": "weekly", "w": "weekly", "7d": "weekly", "7day": "weekly",
	"monthly": "monthly", "month": "monthly", "m": "monthly", "30d": "monthly", "1month": "monthly",
	"quarterly": "quarterly", "quarter": "quarterly", "q": "quarterly", "3month": "quarterly", "3m": "quarterly",
	"half": "half", "halfyear": "half", "half-yearly": "half", "h": "half", "6month": "half", "6m": "half",
	"yearly": "yearly", "year": "yearly", "y": "yearly", "12month": "yearly", "1y": "yearly",
	"alltime": "alltime", "overall": "alltime", "all": "alltime", "a": "alltime", "at": "alltime",
}

func PeriodByKeyword(s string) (Period, bool) {
	name, ok := periodKeywords[strings.ToLower(s)]
	if !ok {
		return Period{}, false
	}
	return PeriodByName(name), true
}

func PeriodByName(name string) Period {
	for _, p := range Periods {
		if p.Name == name {
			return p
		}
	}
	return Periods[len(Periods)-1]
}

// From returns the unix start of the period, or 0 for all time.
func (p Period) From() int64 {
	if p.Days == 0 {
		return 0
	}
	return time.Now().AddDate(0, 0, -p.Days).Unix()
}

// N formats an integer with thousands separators.
func N(n int) string {
	s := strconv.Itoa(abs(n))
	var b strings.Builder
	if n < 0 {
		b.WriteByte('-')
	}
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 && !(n < 0 && b.Len() == 1) {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Plural returns "1 play" / "2 plays".
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return N(n) + " " + word + "s"
}

// Esc escapes Discord markdown.
func Esc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`, "`", "\\`", `~`, `\~`, `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`)
	return r.Replace(s)
}

func Trunc(s string, n int) string { return truncate(s, n) }

// Rel returns a Discord relative timestamp.
func Rel(t time.Time) string { return fmt.Sprintf("<t:%d:R>", t.Unix()) }

// Date returns a Discord long-date timestamp.
func Date(t time.Time) string { return fmt.Sprintf("<t:%d:D>", t.Unix()) }

func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// LongDuration renders e.g. "3 days, 4 hours".
func LongDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%s, %s", Plural(days, "day"), Plural(hours, "hour"))
	case hours > 0:
		return fmt.Sprintf("%s, %s", Plural(hours, "hour"), Plural(mins, "minute"))
	default:
		return Plural(mins, "minute")
	}
}

// Bar renders a simple text progress bar.
func Bar(v, maxV, width int) string {
	if maxV <= 0 {
		maxV = 1
	}
	filled := v * width / maxV
	if v > 0 && filled == 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// Medal returns a rank marker for list output.
func Medal(i int) string {
	switch i {
	case 0:
		return "🥇"
	case 1:
		return "🥈"
	case 2:
		return "🥉"
	}
	return fmt.Sprintf("%d.", i+1)
}

// ArtistURL / AlbumURL / TrackURL build Sonosphere links (Last.fm style paths).
func (b *Bot) ArtistURL(artist string) string {
	return b.API.BaseURL + "/music/" + urlEsc(artist)
}

func (b *Bot) AlbumURL(artist, album string) string {
	return b.API.BaseURL + "/music/" + urlEsc(artist) + "/" + urlEsc(album)
}

func (b *Bot) TrackURL(artist, track string) string {
	return b.API.BaseURL + "/music/" + urlEsc(artist) + "/_/" + urlEsc(track)
}

func (b *Bot) TagURL(tag string) string {
	return b.API.BaseURL + "/tag/" + urlEsc(tag)
}

// urlEsc mirrors the site's encoder: % and + are double-encoded, spaces become +.
func urlEsc(s string) string {
	s = strings.NewReplacer("%", "%25", "+", "%2B").Replace(s)
	return url.QueryEscape(s) // QueryEscape already turns spaces into +
}

// Link renders a markdown link with escaped text.
func Link(text, url string) string {
	if url == "" {
		return Esc(text)
	}
	return "[" + Esc(text) + "](" + strings.ReplaceAll(url, ")", "%29") + ")"
}
