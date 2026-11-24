package sonosphere

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive hits the real Sonosphere API. Run with:
//
//	SONOSPHERE_LIVE_USER=<username> go test ./internal/sonosphere -run Live -v
//
// The keyed 2.0 checks also need SONOSPHERE_API_KEY.
func TestLive(t *testing.T) {
	user := os.Getenv("SONOSPHERE_LIVE_USER")
	if user == "" {
		t.Skip("set SONOSPHERE_LIVE_USER to run live API tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := New(os.Getenv("SONOSPHERE_API_KEY"), os.Getenv("SONOSPHERE_SHARED_SECRET"))

	arts, err := c.UserStatsArtists(ctx, user, RangeAllTime, 5)
	if err != nil {
		t.Fatalf("stats artists: %v", err)
	}
	if arts == nil || len(arts.Artists) == 0 || arts.Artists[0].ArtistName == "" {
		t.Fatalf("no artists decoded: %+v", arts)
	}
	t.Logf("top artist: %s (%d)", arts.Artists[0].ArtistName, arts.Artists[0].ListenCount)

	rg, err := c.UserStatsReleaseGroups(ctx, user, RangeAllTime, 3)
	if err != nil || rg == nil || len(rg.ReleaseGroups) == 0 {
		t.Fatalf("release groups: %+v %v", rg, err)
	}
	rc, err := c.UserStatsRecordings(ctx, user, RangeAllTime, 3)
	if err != nil || rc == nil || len(rc.Recordings) == 0 {
		t.Fatalf("recordings: %+v %v", rc, err)
	}
	act, err := c.UserListeningActivity(ctx, user, RangeWeek)
	if err != nil || len(act) == 0 {
		t.Fatalf("activity: %v %v", act, err)
	}
	if _, err := c.NowPlaying(ctx, user); err != nil {
		t.Fatalf("now playing: %v", err)
	}

	if c.APIKey == "" {
		t.Log("SONOSPHERE_API_KEY not set; skipping 2.0 checks")
		return
	}
	info, err := c.UserInfo(ctx, user)
	if err != nil || info.Playcount == 0 {
		t.Fatalf("user.getInfo: %+v %v", info, err)
	}
	rt, err := c.RecentTracks(ctx, user, RecentOpts{Limit: 2})
	if err != nil || len(rt.Track) == 0 || rt.Attr.Total == 0 {
		t.Fatalf("recent: %+v %v", rt, err)
	}
	ta, err := c.TopArtists(ctx, user, PeriodOverall, 5, 1)
	if err != nil || len(ta.Artist) == 0 {
		t.Fatalf("top artists: %+v %v", ta, err)
	}
	ai, err := c.ArtistInfo(ctx, ta.Artist[0].Name, user)
	if err != nil || ai.UserPlays() == 0 {
		t.Fatalf("artist.getInfo userplaycount missing: %+v %v", ai, err)
	}
	t.Logf("2.0 OK: %s has %d plays of %s", user, ai.UserPlays(), ai.Name)
}
