// Package sonosphere is a client for the Sonosphere API (https://sonosphere.fm/developers).
//
// Sonosphere serves a Last.fm 2.0 compatible API at /2.0/ (used for most reads and
// all authenticated writes), a ListenBrainz compatible API at /1/ (used for range
// based statistics) and a public now-playing endpoint.
package sonosphere

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "https://sonosphere.fm"

// Error codes returned by the 2.0 API.
const (
	ErrInvalidMethod     = 3
	ErrAuthFailed        = 4
	ErrInvalidParameters = 6
	ErrInvalidSession    = 9
	ErrInvalidAPIKey     = 10
	ErrInvalidSignature  = 13
	ErrUnauthorizedToken = 14
)

type APIError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("sonosphere: error %d: %s", e.Code, e.Message)
}

// IsNotFound reports whether err represents an unknown user/artist/album/track.
func IsNotFound(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == ErrInvalidParameters || ae.Status == http.StatusNotFound
	}
	return false
}

func IsCode(err error, code int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

type Client struct {
	BaseURL string
	APIKey  string
	Secret  string
	HTTP    *http.Client
	UA      string

	sem   chan struct{}
	cache *ttlCache
}

func New(apiKey, secret string) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		APIKey:  apiKey,
		Secret:  secret,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		UA:      "sonosbot/1.0 (Discord bot)",
		sem:     make(chan struct{}, 12),
		cache:   newTTLCache(),
	}
}

// AuthURL is where a user authorizes a desktop-flow token.
func (c *Client) AuthURL(token string) string {
	return fmt.Sprintf("%s/api/auth?api_key=%s&token=%s", c.BaseURL, url.QueryEscape(c.APIKey), url.QueryEscape(token))
}

func (c *Client) UserURL(username string) string {
	return c.BaseURL + "/user/" + url.PathEscape(username)
}

func (c *Client) sign(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(params.Get(k))
	}
	sb.WriteString(c.Secret)
	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// call performs a 2.0 API request and decodes the JSON response into out.
// Writes (post=true) are form-encoded POSTs; signed requests get an api_sig.
func (c *Client) call(ctx context.Context, method string, params map[string]string, signed, post bool, ttl time.Duration, out any) error {
	v := url.Values{}
	for k, val := range params {
		if val != "" {
			v.Set(k, val)
		}
	}
	v.Set("method", method)
	v.Set("api_key", c.APIKey)
	if signed {
		v.Set("api_sig", c.sign(v))
	}
	v.Set("format", "json")

	endpoint := c.BaseURL + "/2.0/"
	cacheKey := ""
	if !post && ttl > 0 {
		cacheKey = v.Encode()
		if b, ok := c.cache.get(cacheKey); ok {
			return json.Unmarshal(b, out)
		}
	}

	var req *http.Request
	var err error
	if post {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(v.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+v.Encode(), nil)
	}
	if err != nil {
		return err
	}
	body, status, err := c.do(req)
	if err != nil {
		return err
	}
	var apiErr APIError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
		apiErr.Status = status
		return &apiErr
	}
	if status >= 400 {
		return &APIError{Code: -1, Message: http.StatusText(status), Status: status}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("sonosphere: decoding %s: %w", method, err)
	}
	if cacheKey != "" {
		c.cache.set(cacheKey, body, ttl)
	}
	return nil
}

func (c *Client) do(req *http.Request) ([]byte, int, error) {
	select {
	case c.sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, 0, req.Context().Err()
	}
	defer func() { <-c.sem }()

	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json")
	for attempt := 0; ; attempt++ {
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, 0, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			wait := 2 * time.Second
			if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && ra > 0 && ra < 30 {
				wait = time.Duration(ra) * time.Second
			} else if ri, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Reset-In")); err == nil && ri > 0 && ri < 30 {
				wait = time.Duration(ri) * time.Second
			}
			select {
			case <-time.After(wait):
			case <-req.Context().Done():
				return nil, 0, req.Context().Err()
			}
			if req.GetBody != nil {
				req.Body, _ = req.GetBody()
			}
			continue
		}
		return body, resp.StatusCode, nil
	}
}

// ---------------- Auth ----------------

func (c *Client) GetToken(ctx context.Context) (string, error) {
	var r struct {
		Token string `json:"token"`
	}
	err := c.call(ctx, "auth.getToken", nil, true, false, 0, &r)
	return r.Token, err
}

type Session struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

func (c *Client) GetSession(ctx context.Context, token string) (*Session, error) {
	var r struct {
		Session Session `json:"session"`
	}
	if err := c.call(ctx, "auth.getSession", map[string]string{"token": token}, true, false, 0, &r); err != nil {
		return nil, err
	}
	return &r.Session, nil
}

// ---------------- User ----------------

// Period values accepted by user.getTop* methods.
const (
	PeriodOverall = "overall"
	Period7Day    = "7day"
	Period1Month  = "1month"
	Period3Month  = "3month"
	Period6Month  = "6month"
	Period12Month = "12month"
)

func (c *Client) UserInfo(ctx context.Context, user string) (*UserInfo, error) {
	var r struct {
		User UserInfo `json:"user"`
	}
	if err := c.call(ctx, "user.getInfo", map[string]string{"user": user}, false, false, 2*time.Minute, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

type RecentOpts struct {
	Limit    int
	Page     int
	From, To int64
	Extended bool
	NoCache  bool
}

func (c *Client) RecentTracks(ctx context.Context, user string, o RecentOpts) (*RecentTracks, error) {
	p := map[string]string{"user": user}
	if o.Limit > 0 {
		p["limit"] = strconv.Itoa(o.Limit)
	}
	if o.Page > 0 {
		p["page"] = strconv.Itoa(o.Page)
	}
	if o.From > 0 {
		p["from"] = strconv.FormatInt(o.From, 10)
	}
	if o.To > 0 {
		p["to"] = strconv.FormatInt(o.To, 10)
	}
	if o.Extended {
		p["extended"] = "1"
	}
	ttl := 15 * time.Second
	if o.NoCache {
		ttl = 0
	}
	var r struct {
		RecentTracks RecentTracks `json:"recenttracks"`
	}
	if err := c.call(ctx, "user.getRecentTracks", p, false, false, ttl, &r); err != nil {
		return nil, err
	}
	return &r.RecentTracks, nil
}

func topParams(user, period string, limit, page int) map[string]string {
	p := map[string]string{"user": user, "period": period, "limit": strconv.Itoa(limit)}
	if page > 1 {
		p["page"] = strconv.Itoa(page)
	}
	return p
}

func (c *Client) TopArtists(ctx context.Context, user, period string, limit, page int) (*TopArtists, error) {
	var r struct {
		TopArtists TopArtists `json:"topartists"`
	}
	err := c.call(ctx, "user.getTopArtists", topParams(user, period, limit, page), false, false, 2*time.Minute, &r)
	return &r.TopArtists, err
}

func (c *Client) TopAlbums(ctx context.Context, user, period string, limit, page int) (*TopAlbums, error) {
	var r struct {
		TopAlbums TopAlbums `json:"topalbums"`
	}
	err := c.call(ctx, "user.getTopAlbums", topParams(user, period, limit, page), false, false, 2*time.Minute, &r)
	return &r.TopAlbums, err
}

func (c *Client) TopTracks(ctx context.Context, user, period string, limit, page int) (*TopTracks, error) {
	var r struct {
		TopTracks TopTracks `json:"toptracks"`
	}
	err := c.call(ctx, "user.getTopTracks", topParams(user, period, limit, page), false, false, 2*time.Minute, &r)
	return &r.TopTracks, err
}

func (c *Client) LovedTracks(ctx context.Context, user string, limit, page int) (*TopTracks, error) {
	var r struct {
		LovedTracks TopTracks `json:"lovedtracks"`
	}
	p := map[string]string{"user": user, "limit": strconv.Itoa(limit), "page": strconv.Itoa(max(page, 1))}
	err := c.call(ctx, "user.getLovedTracks", p, false, false, time.Minute, &r)
	return &r.LovedTracks, err
}

func (c *Client) UserTopTags(ctx context.Context, user string, limit int) ([]Tag, error) {
	var r struct {
		TopTags Tags `json:"toptags"`
	}
	err := c.call(ctx, "user.getTopTags", map[string]string{"user": user, "limit": strconv.Itoa(limit)}, false, false, 10*time.Minute, &r)
	return r.TopTags.Tag, err
}

func (c *Client) Friends(ctx context.Context, user string, limit int) ([]UserInfo, error) {
	var r struct {
		Friends struct {
			User List[UserInfo] `json:"user"`
		} `json:"friends"`
	}
	err := c.call(ctx, "user.getFriends", map[string]string{"user": user, "limit": strconv.Itoa(limit)}, false, false, 10*time.Minute, &r)
	return r.Friends.User, err
}

// ---------------- Artist / Album / Track ----------------

func (c *Client) ArtistInfo(ctx context.Context, artist, username string) (*ArtistInfo, error) {
	var r struct {
		Artist ArtistInfo `json:"artist"`
	}
	p := map[string]string{"artist": artist, "autocorrect": "1", "username": username}
	if err := c.call(ctx, "artist.getInfo", p, false, false, time.Minute, &r); err != nil {
		return nil, err
	}
	if r.Artist.Name == "" {
		return nil, &APIError{Code: ErrInvalidParameters, Message: "artist not found"}
	}
	return &r.Artist, nil
}

func (c *Client) ArtistTopTracks(ctx context.Context, artist string, limit int) ([]TopTrack, error) {
	var r struct {
		TopTracks TopTracks `json:"toptracks"`
	}
	err := c.call(ctx, "artist.getTopTracks", map[string]string{"artist": artist, "autocorrect": "1", "limit": strconv.Itoa(limit)}, false, false, 30*time.Minute, &r)
	return r.TopTracks.Track, err
}

func (c *Client) ArtistTopAlbums(ctx context.Context, artist string, limit int) ([]TopAlbum, error) {
	var r struct {
		TopAlbums TopAlbums `json:"topalbums"`
	}
	err := c.call(ctx, "artist.getTopAlbums", map[string]string{"artist": artist, "autocorrect": "1", "limit": strconv.Itoa(limit)}, false, false, 30*time.Minute, &r)
	return r.TopAlbums.Album, err
}

func (c *Client) ArtistTopTags(ctx context.Context, artist string) ([]Tag, error) {
	var r struct {
		TopTags Tags `json:"toptags"`
	}
	err := c.call(ctx, "artist.getTopTags", map[string]string{"artist": artist, "autocorrect": "1"}, false, false, 24*time.Hour, &r)
	return r.TopTags.Tag, err
}

func (c *Client) ArtistSimilar(ctx context.Context, artist string, limit int) ([]TopArtist, error) {
	var r struct {
		SimilarArtists struct {
			Artist List[TopArtist] `json:"artist"`
		} `json:"similarartists"`
	}
	err := c.call(ctx, "artist.getSimilar", map[string]string{"artist": artist, "autocorrect": "1", "limit": strconv.Itoa(limit)}, false, false, time.Hour, &r)
	return r.SimilarArtists.Artist, err
}

func (c *Client) AlbumInfo(ctx context.Context, artist, album, username string) (*AlbumInfo, error) {
	var r struct {
		Album AlbumInfo `json:"album"`
	}
	p := map[string]string{"artist": artist, "album": album, "autocorrect": "1", "username": username}
	if err := c.call(ctx, "album.getInfo", p, false, false, time.Minute, &r); err != nil {
		return nil, err
	}
	if r.Album.Name == "" {
		return nil, &APIError{Code: ErrInvalidParameters, Message: "album not found"}
	}
	return &r.Album, nil
}

func (c *Client) TrackInfo(ctx context.Context, artist, track, username string) (*TrackInfo, error) {
	var r struct {
		Track TrackInfo `json:"track"`
	}
	p := map[string]string{"artist": artist, "track": track, "autocorrect": "1", "username": username}
	if err := c.call(ctx, "track.getInfo", p, false, false, time.Minute, &r); err != nil {
		return nil, err
	}
	if r.Track.Name == "" {
		return nil, &APIError{Code: ErrInvalidParameters, Message: "track not found"}
	}
	return &r.Track, nil
}

func (c *Client) SearchArtist(ctx context.Context, q string, limit int) ([]SearchArtist, error) {
	var r struct {
		Results struct {
			Matches struct {
				Artist List[SearchArtist] `json:"artist"`
			} `json:"artistmatches"`
		} `json:"results"`
	}
	err := c.call(ctx, "artist.search", map[string]string{"artist": q, "limit": strconv.Itoa(limit)}, false, false, 10*time.Minute, &r)
	return r.Results.Matches.Artist, err
}

func (c *Client) SearchAlbum(ctx context.Context, q string, limit int) ([]SearchAlbum, error) {
	var r struct {
		Results struct {
			Matches struct {
				Album List[SearchAlbum] `json:"album"`
			} `json:"albummatches"`
		} `json:"results"`
	}
	err := c.call(ctx, "album.search", map[string]string{"album": q, "limit": strconv.Itoa(limit)}, false, false, 10*time.Minute, &r)
	return r.Results.Matches.Album, err
}

func (c *Client) SearchTrack(ctx context.Context, q string, limit int) ([]SearchTrack, error) {
	var r struct {
		Results struct {
			Matches struct {
				Track List[SearchTrack] `json:"track"`
			} `json:"trackmatches"`
		} `json:"results"`
	}
	err := c.call(ctx, "track.search", map[string]string{"track": q, "limit": strconv.Itoa(limit)}, false, false, 10*time.Minute, &r)
	return r.Results.Matches.Track, err
}

// ---------------- Tags / charts ----------------

func (c *Client) TagInfo(ctx context.Context, tag string) (*TagInfo, error) {
	var r struct {
		Tag TagInfo `json:"tag"`
	}
	if err := c.call(ctx, "tag.getInfo", map[string]string{"tag": tag}, false, false, time.Hour, &r); err != nil {
		return nil, err
	}
	return &r.Tag, nil
}

func (c *Client) TagTopArtists(ctx context.Context, tag string, limit int) ([]TopArtist, error) {
	var r struct {
		TopArtists TopArtists `json:"topartists"`
	}
	err := c.call(ctx, "tag.getTopArtists", map[string]string{"tag": tag, "limit": strconv.Itoa(limit)}, false, false, time.Hour, &r)
	return r.TopArtists.Artist, err
}

func (c *Client) ChartTopArtists(ctx context.Context, limit int) ([]TopArtist, error) {
	var r struct {
		Artists TopArtists `json:"artists"`
	}
	err := c.call(ctx, "chart.getTopArtists", map[string]string{"limit": strconv.Itoa(limit)}, false, false, 30*time.Minute, &r)
	return r.Artists.Artist, err
}

func (c *Client) ChartTopTracks(ctx context.Context, limit int) ([]TopTrack, error) {
	var r struct {
		Tracks TopTracks `json:"tracks"`
	}
	err := c.call(ctx, "chart.getTopTracks", map[string]string{"limit": strconv.Itoa(limit)}, false, false, 30*time.Minute, &r)
	return r.Tracks.Track, err
}

// ---------------- Writes (require a session key) ----------------

func (c *Client) Love(ctx context.Context, sk, artist, track string) error {
	var r json.RawMessage
	return c.call(ctx, "track.love", map[string]string{"sk": sk, "artist": artist, "track": track}, true, true, 0, &r)
}

func (c *Client) Unlove(ctx context.Context, sk, artist, track string) error {
	var r json.RawMessage
	return c.call(ctx, "track.unlove", map[string]string{"sk": sk, "artist": artist, "track": track}, true, true, 0, &r)
}

func (c *Client) Scrobble(ctx context.Context, sk, artist, track, album string, ts int64) error {
	var r struct {
		Scrobbles struct {
			Attr struct {
				Accepted Num `json:"accepted"`
				Ignored  Num `json:"ignored"`
			} `json:"@attr"`
		} `json:"scrobbles"`
	}
	p := map[string]string{"sk": sk, "artist": artist, "track": track, "album": album, "timestamp": strconv.FormatInt(ts, 10)}
	if err := c.call(ctx, "track.scrobble", p, true, true, 0, &r); err != nil {
		return err
	}
	if r.Scrobbles.Attr.Ignored > 0 && r.Scrobbles.Attr.Accepted == 0 {
		return errors.New("the scrobble was ignored by Sonosphere")
	}
	return nil
}

// ---------------- tiny TTL cache ----------------

type cacheEntry struct {
	b   []byte
	exp time.Time
}

type ttlCache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
	n  int
}

func newTTLCache() *ttlCache { return &ttlCache{m: map[string]cacheEntry{}} }

func (c *ttlCache) get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.b, true
}

func (c *ttlCache) set(k string, b []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = cacheEntry{b: b, exp: time.Now().Add(ttl)}
	c.n++
	if c.n%500 == 0 { // periodic sweep
		now := time.Now()
		for key, e := range c.m {
			if now.After(e.exp) {
				delete(c.m, key)
			}
		}
	}
}
