package store

import (
	"context"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWhoKnowsAndCrowns(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const guild = 1
	users := map[int64]int{10: 50, 11: 120, 12: 5, 13: 999} // 13 is not in the guild
	for id, plays := range users {
		if _, err := s.UpsertUser(ctx, id, "user"+string(rune('a'+id-10)), ""); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceIndex(ctx, id, []IndexedArtist{{"Glass Harbor", plays}}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if id != 13 {
			if err := s.TouchGuildUser(ctx, guild, id, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows, err := s.WhoKnowsArtist(ctx, "glass harbor", Scope{GuildID: guild}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].UserID != 11 || rows[0].Playcount != 120 {
		t.Fatalf("server rows = %+v", rows)
	}
	global, _ := s.WhoKnowsArtist(ctx, "GLASS HARBOR", Scope{Global: true}, 10)
	if len(global) != 4 || global[0].UserID != 13 {
		t.Fatalf("global rows = %+v", global)
	}

	// First evaluation claims the crown for the top listener.
	res, err := s.EvaluateCrown(ctx, guild, "Glass Harbor", rows[0], 30)
	if err != nil || !res.Claimed || res.Holder.UserID != 11 {
		t.Fatalf("claim = %+v, %v", res, err)
	}
	// Re-evaluating with the same holder doesn't create a new crown.
	res, _ = s.EvaluateCrown(ctx, guild, "Glass Harbor", rows[0], 30)
	if res.Claimed {
		t.Fatal("unexpected re-claim")
	}
	// User 10 overtakes and steals it.
	if err := s.SetArtistPlays(ctx, 10, "Glass Harbor", 200); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.WhoKnowsArtist(ctx, "glass harbor", Scope{GuildID: guild}, 10)
	res, _ = s.EvaluateCrown(ctx, guild, "Glass Harbor", rows[0], 30)
	if !res.Claimed || res.PrevHolder != 11 || res.Holder.UserID != 10 {
		t.Fatalf("steal = %+v", res)
	}
	stolen, _ := s.StolenCrowns(ctx, guild, 11)
	if len(stolen) != 1 {
		t.Fatalf("stolen = %+v", stolen)
	}
	lb, _ := s.CrownLeaderboard(ctx, guild, 10)
	if len(lb) != 1 || lb[0].UserID != 10 {
		t.Fatalf("leaderboard = %+v", lb)
	}

	// Blocked users are excluded from server WhoKnows.
	s.SetGuildUserFlag(ctx, guild, 10, "blocked", true)
	rows, _ = s.WhoKnowsArtist(ctx, "glass harbor", Scope{GuildID: guild}, 10)
	for _, r := range rows {
		if r.UserID == 10 {
			t.Fatal("blocked user listed")
		}
	}

	// Leaving the guild deactivates crowns.
	if err := s.RemoveGuildUser(ctx, guild, 10); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.ActiveCrown(ctx, guild, "Glass Harbor"); c != nil {
		t.Fatalf("crown should be inactive, got %+v", c)
	}
}

func TestBelowThresholdNoCrown(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	s.UpsertUser(ctx, 1, "a", "")
	res, err := s.EvaluateCrown(ctx, 1, "X", WhoKnowsRow{UserID: 1, Playcount: 5}, 30)
	if err != nil || res.Claimed || res.Holder != nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestAccountSwitchClearsIndex(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	s.UpsertUser(ctx, 1, "old", "")
	s.ReplaceIndex(ctx, 1, []IndexedArtist{{"A", 3}}, nil, nil)
	changed, err := s.UpsertUser(ctx, 1, "new", "")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if a, _ := s.UserArtists(ctx, 1, 10); len(a) != 0 {
		t.Fatalf("index not cleared: %+v", a)
	}
	// Same name again (session refresh) keeps the index.
	s.ReplaceIndex(ctx, 1, []IndexedArtist{{"A", 3}}, nil, nil)
	changed, _ = s.UpsertUser(ctx, 1, "NEW", "sk")
	if changed {
		t.Fatal("case-only change should not count as an account switch")
	}
	if a, _ := s.UserArtists(ctx, 1, 10); len(a) != 1 {
		t.Fatal("index should be kept")
	}
}

func TestServerTopAndDelete(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for id := int64(1); id <= 2; id++ {
		s.UpsertUser(ctx, id, "u", "")
		s.TouchGuildUser(ctx, 9, id, "")
		s.ReplaceIndex(ctx, id, []IndexedArtist{{"A", 10}, {"B", int(id)}},
			[]IndexedAlbum{{Artist: "A", Name: "Alb", Playcount: 4}},
			[]IndexedTrack{{Artist: "A", Name: "T", Playcount: 2}})
	}
	top, err := s.ServerTop(ctx, 9, "artists", 10)
	if err != nil || len(top) != 2 || top[0].Name != "A" || top[0].Playcount != 20 || top[0].Listeners != 2 {
		t.Fatalf("top = %+v err = %v", top, err)
	}
	if err := s.DeleteUser(ctx, 1); err != nil {
		t.Fatal(err)
	}
	top, _ = s.ServerTop(ctx, 9, "artists", 10)
	if top[0].Playcount != 10 {
		t.Fatalf("after delete = %+v", top)
	}
}
