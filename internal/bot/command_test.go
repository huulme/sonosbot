package bot

import (
	"testing"
)

func testCmd() *Command {
	return &Command{
		Name: "chart",
		Options: []Option{
			{Name: "size", Kind: OptSize},
			{Name: "time-period", Kind: OptPeriod},
			{Name: "user", Kind: OptUser},
			{Name: "notitles", Kind: OptFlag, Aliases: []string{"nt"}},
		},
	}
}

func TestParseTextChart(t *testing.T) {
	args := testCmd().parseText("4x5 monthly nt <@123456789012345678>")
	if args["size"] != "4x5" {
		t.Errorf("size = %v", args["size"])
	}
	if args["time-period"] != "monthly" {
		t.Errorf("period = %v", args["time-period"])
	}
	if args["notitles"] != true {
		t.Errorf("notitles = %v", args["notitles"])
	}
	if args["user"] != "<@123456789012345678>" {
		t.Errorf("user = %v", args["user"])
	}
}

func TestParseTextBareUsernameWithoutQuery(t *testing.T) {
	args := testCmd().parseText("weekly someuser")
	if args["user"] != "someuser" {
		t.Errorf("user = %v, want someuser", args["user"])
	}
	if args["time-period"] != "weekly" {
		t.Errorf("period = %v", args["time-period"])
	}
}

func TestParseTextQueryKeepsWords(t *testing.T) {
	c := &Command{Name: "whoknows", Options: []Option{{Name: "artist", Kind: OptQuery}, {Name: "user", Kind: OptUser}}}
	args := c.parseText("the beatles sp:bob")
	if args["artist"] != "the beatles" {
		t.Errorf("artist = %q", args["artist"])
	}
	if args["user"] != "sp:bob" {
		t.Errorf("user = %v", args["user"])
	}
}

func TestParseTextIntOnlyWithoutQuery(t *testing.T) {
	ms := &Command{Name: "milestone", Options: []Option{{Name: "user", Kind: OptUser}, {Name: "number", Kind: OptInt}}}
	if got := ms.parseText("10,000")["number"]; got != 10000 {
		t.Errorf("number = %v", got)
	}
	// With a query, numbers stay part of the query ("blink 182").
	q := &Command{Name: "artist", Options: []Option{{Name: "artist", Kind: OptQuery}, {Name: "n", Kind: OptInt}}}
	if got := q.parseText("blink 182")["artist"]; got != "blink 182" {
		t.Errorf("artist = %v", got)
	}
}

func TestPeriodKeywords(t *testing.T) {
	for kw, want := range map[string]string{"w": "weekly", "MONTHLY": "monthly", "q": "quarterly", "h": "half", "1y": "yearly", "overall": "alltime"} {
		p, ok := PeriodByKeyword(kw)
		if !ok || p.Name != want {
			t.Errorf("PeriodByKeyword(%q) = %v,%v want %s", kw, p.Name, ok, want)
		}
	}
	if _, ok := PeriodByKeyword("beatles"); ok {
		t.Error("beatles should not be a period")
	}
}

func TestNumberFormat(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := N(in); got != want {
			t.Errorf("N(%d) = %q want %q", in, got, want)
		}
	}
}

func TestURLEscapeMatchesSite(t *testing.T) {
	// The site encodes spaces as + and double-encodes % and +.
	for in, want := range map[string]string{
		"Glass Harbor":           "Glass+Harbor",
		"AC/DC":                  "AC%2FDC",
		"Florence + the Machine": "Florence+%252B+the+Machine",
		"100%":                   "100%2525",
	} {
		if got := urlEsc(in); got != want {
			t.Errorf("urlEsc(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSlashDefinitionsValid(t *testing.T) {
	c := testCmd()
	c.Description = "Generates a chart"
	sc := c.SlashCreate()
	if sc.Name != "chart" || len(sc.Options) != 4 {
		t.Fatalf("unexpected slash definition: %+v", sc)
	}
}
