package bot

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/huulme/sonosbot/internal/sonosphere"
	"github.com/huulme/sonosbot/internal/store"
)

// Index caps: pages of 1000 items fetched per category.
const (
	indexArtistPages = 5
	indexAlbumPages  = 3
	indexTrackPages  = 3
	indexPageSize    = 1000
)

var ErrIndexing = errors.New("already indexing")

// IndexUser refreshes the stored top artists/albums/tracks for a user.
func (b *Bot) IndexUser(ctx context.Context, u *store.User) error {
	if _, busy := b.indexing.LoadOrStore(u.DiscordID, struct{}{}); busy {
		return ErrIndexing
	}
	defer b.indexing.Delete(u.DiscordID)

	var (
		artists []store.IndexedArtist
		albums  []store.IndexedAlbum
		tracks  []store.IndexedTrack
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		for page := 1; page <= indexArtistPages; page++ {
			r, err := b.API.TopArtists(gctx, u.Username, sonosphere.PeriodOverall, indexPageSize, page)
			if err != nil {
				return err
			}
			for _, a := range r.Artist {
				artists = append(artists, store.IndexedArtist{Name: a.Name, Playcount: a.Playcount.Int()})
			}
			if len(r.Artist) < indexPageSize || page >= r.Attr.TotalPages.Int() {
				break
			}
		}
		return nil
	})
	g.Go(func() error {
		for page := 1; page <= indexAlbumPages; page++ {
			r, err := b.API.TopAlbums(gctx, u.Username, sonosphere.PeriodOverall, indexPageSize, page)
			if err != nil {
				return err
			}
			for _, a := range r.Album {
				albums = append(albums, store.IndexedAlbum{Artist: a.Artist.String(), Name: a.Name, Playcount: a.Playcount.Int(), Image: a.Image.Best()})
			}
			if len(r.Album) < indexPageSize || page >= r.Attr.TotalPages.Int() {
				break
			}
		}
		return nil
	})
	g.Go(func() error {
		for page := 1; page <= indexTrackPages; page++ {
			r, err := b.API.TopTracks(gctx, u.Username, sonosphere.PeriodOverall, indexPageSize, page)
			if err != nil {
				return err
			}
			for _, t := range r.Track {
				tracks = append(tracks, store.IndexedTrack{Artist: t.Artist.String(), Name: t.Name, Playcount: t.Playcount.Int()})
			}
			if len(r.Track) < indexPageSize || page >= r.Attr.TotalPages.Int() {
				break
			}
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return err
	}
	return b.Store.ReplaceIndex(ctx, u.DiscordID, artists, albums, tracks)
}

// RunIndexer periodically refreshes stale user indexes until ctx is done.
func (b *Bot) RunIndexer(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		users, err := b.Store.StaleUsers(ctx, b.Cfg.IndexMaxAge, 25)
		if err != nil {
			b.Log.Warn("indexer: listing stale users", "err", err)
		}
		for _, u := range users {
			if ctx.Err() != nil {
				return
			}
			uctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			err := b.IndexUser(uctx, &u)
			cancel()
			if err != nil && !errors.Is(err, ErrIndexing) {
				b.Log.Warn("indexer: user failed", "user", u.Username, "err", err)
				if sonosphere.IsNotFound(err) {
					// Mark as indexed so a deleted account doesn't block the queue.
					_ = b.Store.ReplaceIndex(ctx, u.DiscordID, nil, nil, nil)
				}
			}
			time.Sleep(2 * time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
