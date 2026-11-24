package sonosphere

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestLastFMQuirks(t *testing.T) {
	// Numbers as strings, a single track as an object, and "#text" artist objects.
	raw := `{"recenttracks":{"track":{"name":"When the Echoes Come","artist":{"#text":"Glass Harbor","mbid":""},
		"album":{"#text":"Night Signals"},"image":[{"#text":"s.jpg","size":"small"},{"#text":"xl.jpg","size":"extralarge"}],
		"@attr":{"nowplaying":"true"}},"@attr":{"total":"1234","page":"1","totalPages":"62"}}}`
	var r struct {
		RecentTracks RecentTracks `json:"recenttracks"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	rt := r.RecentTracks
	if len(rt.Track) != 1 || rt.Track[0].Artist != "Glass Harbor" || rt.Track[0].Album != "Night Signals" {
		t.Fatalf("bad decode: %+v", rt.Track)
	}
	if !rt.Track[0].NowPlaying() {
		t.Error("expected now playing")
	}
	if rt.Attr.Total != 1234 || rt.Attr.TotalPages != 62 {
		t.Errorf("attr = %+v", rt.Attr)
	}
	if got := rt.Track[0].Image.Best(); got != "xl.jpg" {
		t.Errorf("best image = %q", got)
	}
}

func TestEmptyListAndMissingUserPlaycount(t *testing.T) {
	raw := `{"artist":{"name":"X","stats":{"listeners":"10","playcount":5},"tags":{"tag":""},"similar":{"artist":[]}}}`
	var r struct {
		Artist ArtistInfo `json:"artist"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	if r.Artist.UserPlays() != 0 || r.Artist.Stats.Listeners != 10 || len(r.Artist.Tags.Tag) != 0 {
		t.Errorf("bad decode: %+v", r.Artist)
	}
}

// TestSignatureMatchesDocs reproduces the signing example from sonosphere.fm/developers.
func TestSignatureMatchesDocs(t *testing.T) {
	c := New("KEY", "SECRET")
	v := url.Values{}
	v.Set("method", "track.scrobble")
	v.Set("api_key", "KEY")
	v.Set("sk", "SK")
	v.Set("timestamp", "1700000000")
	v.Set("artist", "Glass Harbor")
	v.Set("track", "When the Echoes Come")
	v.Set("format", "json") // excluded from the signature
	want := md5.Sum([]byte(fmt.Sprintf("api_key%sartistGlass Harbormethodtrack.scrobblesk%stimestamp%strackWhen the Echoes Come%s",
		"KEY", "SK", "1700000000", "SECRET")))
	if got := c.sign(v); got != hex.EncodeToString(want[:]) {
		t.Errorf("signature = %s want %x", got, want)
	}
}

func TestErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":6,"message":"User not found"}`))
	}))
	defer srv.Close()
	c := New("k", "s")
	c.BaseURL = srv.URL
	_, err := c.UserInfo(context.Background(), "nobody")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestWritesArePostedAndSigned(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		r.ParseForm()
		got = r.PostForm
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New("k", "s")
	c.BaseURL = srv.URL
	if err := c.Love(context.Background(), "sess", "Artist", "Track"); err != nil {
		t.Fatal(err)
	}
	if got.Get("method") != "track.love" || got.Get("sk") != "sess" || got.Get("api_sig") == "" {
		t.Errorf("form = %v", got)
	}
}
