package sonosphere

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Num decodes numbers that the Last.fm 2.0 protocol may send either as JSON
// numbers or as strings ("123"). Empty strings decode as 0.
type Num int64

func (n *Num) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*n = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			*n = 0
			return nil
		}
		*n = Num(v)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = Num(f)
	return nil
}

func (n Num) Int() int { return int(n) }

// Bool decodes "0"/"1"/true/false.
type Bool bool

func (v *Bool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*v = Bool(s == "1" || strings.EqualFold(s, "true"))
	return nil
}

// List decodes a JSON value that may be an array, a single object, or empty.
type List[T any] []T

func (l *List[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" || string(b) == `""` {
		*l = nil
		return nil
	}
	if b[0] == '[' {
		var arr []T
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*l = arr
		return nil
	}
	if b[0] == '{' {
		var one T
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*l = []T{one}
		return nil
	}
	*l = nil
	return nil
}

// Text decodes either a plain string or an object with a "#text" (or "name") field.
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = Text(s)
		return nil
	}
	if b[0] == '{' {
		var o struct {
			Text  string `json:"#text"`
			Name  string `json:"name"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(b, &o); err != nil {
			return err
		}
		switch {
		case o.Text != "":
			*t = Text(o.Text)
		case o.Name != "":
			*t = Text(o.Name)
		default:
			*t = Text(o.Title)
		}
	}
	return nil
}

func (t Text) String() string { return string(t) }

type Image struct {
	URL  string `json:"#text"`
	Size string `json:"size"`
}

// Images is a list of sized images; Best returns the largest available URL.
type Images List[Image]

func (im *Images) UnmarshalJSON(b []byte) error {
	var l List[Image]
	if err := l.UnmarshalJSON(b); err != nil {
		// Some implementations send a bare URL string.
		var s string
		if json.Unmarshal(b, &s) == nil && s != "" {
			*im = Images{{URL: s, Size: "extralarge"}}
			return nil
		}
		return nil
	}
	*im = Images(l)
	return nil
}

var sizeRank = map[string]int{"small": 1, "medium": 2, "large": 3, "extralarge": 4, "mega": 5, "": 0}

func (im Images) Best() string {
	best, rank := "", -1
	for _, i := range im {
		if i.URL == "" {
			continue
		}
		if r := sizeRank[i.Size]; r > rank {
			best, rank = i.URL, r
		}
	}
	return best
}

type Attr struct {
	Page       Num    `json:"page"`
	PerPage    Num    `json:"perPage"`
	TotalPages Num    `json:"totalPages"`
	Total      Num    `json:"total"`
	User       string `json:"user"`
	Rank       Num    `json:"rank"`
	NowPlaying Bool   `json:"nowplaying"`
}

type Tag struct {
	Name  string `json:"name"`
	Count Num    `json:"count"`
	URL   string `json:"url"`
}

type Tags struct {
	Tag List[Tag] `json:"tag"`
}

// ---- user ----

type UserInfo struct {
	Name        string `json:"name"`
	RealName    string `json:"realname"`
	URL         string `json:"url"`
	Image       Images `json:"image"`
	Country     string `json:"country"`
	Playcount   Num    `json:"playcount"`
	ArtistCount Num    `json:"artist_count"`
	AlbumCount  Num    `json:"album_count"`
	TrackCount  Num    `json:"track_count"`
	Registered  struct {
		Unixtime Num `json:"unixtime"`
	} `json:"registered"`
}

type RecentTrack struct {
	Name   string `json:"name"`
	Artist Text   `json:"artist"`
	Album  Text   `json:"album"`
	Image  Images `json:"image"`
	URL    string `json:"url"`
	Loved  Bool   `json:"loved"`
	Date   *struct {
		UTS Num `json:"uts"`
	} `json:"date"`
	Attr *Attr `json:"@attr"`
}

func (t RecentTrack) NowPlaying() bool { return t.Attr != nil && bool(t.Attr.NowPlaying) }
func (t RecentTrack) Timestamp() int64 {
	if t.Date == nil {
		return 0
	}
	return int64(t.Date.UTS)
}

type RecentTracks struct {
	Track List[RecentTrack] `json:"track"`
	Attr  Attr              `json:"@attr"`
}

type TopArtist struct {
	Name      string `json:"name"`
	Playcount Num    `json:"playcount"`
	URL       string `json:"url"`
	MBID      string `json:"mbid"`
	Image     Images `json:"image"`
	Attr      *Attr  `json:"@attr"`
}

type TopArtists struct {
	Artist List[TopArtist] `json:"artist"`
	Attr   Attr            `json:"@attr"`
}

type TopAlbum struct {
	Name      string `json:"name"`
	Playcount Num    `json:"playcount"`
	Artist    Text   `json:"artist"`
	URL       string `json:"url"`
	Image     Images `json:"image"`
}

type TopAlbums struct {
	Album List[TopAlbum] `json:"album"`
	Attr  Attr           `json:"@attr"`
}

type TopTrack struct {
	Name      string `json:"name"`
	Playcount Num    `json:"playcount"`
	Duration  Num    `json:"duration"`
	Artist    Text   `json:"artist"`
	URL       string `json:"url"`
	Image     Images `json:"image"`
	Date      *struct {
		UTS Num `json:"uts"`
	} `json:"date"`
}

type TopTracks struct {
	Track List[TopTrack] `json:"track"`
	Attr  Attr           `json:"@attr"`
}

// ---- artist / album / track ----

type Stats struct {
	Listeners     Num  `json:"listeners"`
	Playcount     Num  `json:"playcount"`
	UserPlaycount *Num `json:"userplaycount"`
}

type ArtistInfo struct {
	Name  string `json:"name"`
	MBID  string `json:"mbid"`
	URL   string `json:"url"`
	Image Images `json:"image"`
	Stats Stats  `json:"stats"`
	Tags  Tags   `json:"tags"`
	Bio   struct {
		Summary string `json:"summary"`
	} `json:"bio"`
	Similar struct {
		Artist List[TopArtist] `json:"artist"`
	} `json:"similar"`
}

func (a ArtistInfo) UserPlays() int {
	if a.Stats.UserPlaycount == nil {
		return 0
	}
	return int(*a.Stats.UserPlaycount)
}

type AlbumTrack struct {
	Name     string `json:"name"`
	Duration Num    `json:"duration"`
	Attr     *Attr  `json:"@attr"`
}

type AlbumInfo struct {
	Name          string `json:"name"`
	Artist        Text   `json:"artist"`
	URL           string `json:"url"`
	Image         Images `json:"image"`
	Listeners     Num    `json:"listeners"`
	Playcount     Num    `json:"playcount"`
	UserPlaycount *Num   `json:"userplaycount"`
	Tags          Tags   `json:"tags"`
	Tracks        struct {
		Track List[AlbumTrack] `json:"track"`
	} `json:"tracks"`
	Wiki struct {
		Published string `json:"published"`
		Summary   string `json:"summary"`
	} `json:"wiki"`
}

func (a AlbumInfo) UserPlays() int {
	if a.UserPlaycount == nil {
		return 0
	}
	return int(*a.UserPlaycount)
}

type TrackInfo struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Duration      Num    `json:"duration"` // milliseconds
	Listeners     Num    `json:"listeners"`
	Playcount     Num    `json:"playcount"`
	UserPlaycount *Num   `json:"userplaycount"`
	UserLoved     Bool   `json:"userloved"`
	Artist        Text   `json:"artist"`
	Album         *struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Image  Images `json:"image"`
	} `json:"album"`
	TopTags Tags `json:"toptags"`
	Wiki    struct {
		Summary string `json:"summary"`
	} `json:"wiki"`
}

func (t TrackInfo) UserPlays() int {
	if t.UserPlaycount == nil {
		return 0
	}
	return int(*t.UserPlaycount)
}

type SearchTrack struct {
	Name      string `json:"name"`
	Artist    Text   `json:"artist"`
	Listeners Num    `json:"listeners"`
	Image     Images `json:"image"`
}

type SearchAlbum struct {
	Name   string `json:"name"`
	Artist Text   `json:"artist"`
	Image  Images `json:"image"`
}

type SearchArtist struct {
	Name      string `json:"name"`
	Listeners Num    `json:"listeners"`
	Image     Images `json:"image"`
}

type TagInfo struct {
	Name  string `json:"name"`
	Total Num    `json:"total"`
	Reach Num    `json:"reach"`
	Wiki  struct {
		Summary string `json:"summary"`
	} `json:"wiki"`
}
