package sonosphere

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Ranges supported by the ListenBrainz-compatible stats endpoints.
const (
	RangeThisWeek  = "this_week"
	RangeThisMonth = "this_month"
	RangeThisYear  = "this_year"
	RangeWeek      = "week"
	RangeMonth     = "month"
	RangeQuarter   = "quarter"
	RangeHalfYear  = "half_yearly"
	RangeYear      = "year"
	RangeAllTime   = "all_time"
)

func (c *Client) lbGet(ctx context.Context, path string, q url.Values, out any) (int, error) {
	u := c.BaseURL + "/1/" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if b, ok := c.cache.get(u); ok {
		return http.StatusOK, json.Unmarshal(b, out)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	body, status, err := c.do(req)
	if err != nil {
		return 0, err
	}
	if status == http.StatusNoContent {
		return status, nil
	}
	if status >= 400 {
		var e struct {
			Code  int    `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return status, &APIError{Code: -1, Message: e.Error, Status: status}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return status, fmt.Errorf("sonosphere: decoding %s: %w", path, err)
	}
	c.cache.set(u, body, 60_000_000_000) // 1 minute
	return status, nil
}

type LBArtistStat struct {
	ArtistName  string `json:"artist_name"`
	ArtistMBID  string `json:"artist_mbid"`
	ListenCount int    `json:"listen_count"`
}

type LBReleaseGroupStat struct {
	ArtistName       string `json:"artist_name"`
	ReleaseGroupName string `json:"release_group_name"`
	ListenCount      int    `json:"listen_count"`
}

type LBRecordingStat struct {
	ArtistName  string `json:"artist_name"`
	TrackName   string `json:"track_name"`
	ReleaseName string `json:"release_name"`
	ListenCount int    `json:"listen_count"`
}

type LBArtists struct {
	Artists          []LBArtistStat `json:"artists"`
	TotalArtistCount int            `json:"total_artist_count"`
	FromTS           int64          `json:"from_ts"`
	ToTS             int64          `json:"to_ts"`
}

type LBReleaseGroups struct {
	ReleaseGroups []LBReleaseGroupStat `json:"release_groups"`
	Total         int                  `json:"total_release_group_count"`
}

type LBRecordings struct {
	Recordings []LBRecordingStat `json:"recordings"`
	Total      int               `json:"total_recording_count"`
}

func statsQuery(rng string, count, offset int) url.Values {
	q := url.Values{"range": {rng}, "count": {strconv.Itoa(count)}}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return q
}

// UserStatsArtists returns a user's top artists for a range. A nil result with no
// error means there is nothing to show for that range.
func (c *Client) UserStatsArtists(ctx context.Context, user, rng string, count int) (*LBArtists, error) {
	var r struct {
		Payload LBArtists `json:"payload"`
	}
	status, err := c.lbGet(ctx, "stats/user/"+url.PathEscape(user)+"/artists", statsQuery(rng, count, 0), &r)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return &r.Payload, nil
}

func (c *Client) UserStatsReleaseGroups(ctx context.Context, user, rng string, count int) (*LBReleaseGroups, error) {
	var r struct {
		Payload LBReleaseGroups `json:"payload"`
	}
	status, err := c.lbGet(ctx, "stats/user/"+url.PathEscape(user)+"/release-groups", statsQuery(rng, count, 0), &r)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return &r.Payload, nil
}

func (c *Client) UserStatsRecordings(ctx context.Context, user, rng string, count int) (*LBRecordings, error) {
	var r struct {
		Payload LBRecordings `json:"payload"`
	}
	status, err := c.lbGet(ctx, "stats/user/"+url.PathEscape(user)+"/recordings", statsQuery(rng, count, 0), &r)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return &r.Payload, nil
}

type ListeningActivity struct {
	FromTS      int64  `json:"from_ts"`
	ToTS        int64  `json:"to_ts"`
	TimeRange   string `json:"time_range"`
	ListenCount int    `json:"listen_count"`
}

func (c *Client) UserListeningActivity(ctx context.Context, user, rng string) ([]ListeningActivity, error) {
	var r struct {
		Payload struct {
			ListeningActivity []ListeningActivity `json:"listening_activity"`
		} `json:"payload"`
	}
	_, err := c.lbGet(ctx, "stats/user/"+url.PathEscape(user)+"/listening-activity", url.Values{"range": {rng}}, &r)
	return r.Payload.ListeningActivity, err
}

type HourCount struct {
	Hour        int `json:"hour"`
	ListenCount int `json:"listen_count"`
}

// UserDailyActivity returns listen counts per weekday ("Monday"…) per UTC hour.
func (c *Client) UserDailyActivity(ctx context.Context, user, rng string) (map[string][]HourCount, error) {
	var r struct {
		Payload struct {
			DailyActivity map[string][]HourCount `json:"daily_activity"`
		} `json:"payload"`
	}
	_, err := c.lbGet(ctx, "stats/user/"+url.PathEscape(user)+"/daily-activity", url.Values{"range": {rng}}, &r)
	return r.Payload.DailyActivity, err
}

func (c *Client) SitewideArtists(ctx context.Context, rng string, count int) (*LBArtists, error) {
	var r struct {
		Payload LBArtists `json:"payload"`
	}
	status, err := c.lbGet(ctx, "stats/sitewide/artists", statsQuery(rng, count, 0), &r)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return &r.Payload, nil
}

// ---------------- Public now-playing ----------------

type NowPlaying struct {
	TrackID   int64   `json:"trackId"`
	Title     string  `json:"title"`
	Artist    string  `json:"artist"`
	Album     *string `json:"album"`
	Duration  int     `json:"duration"`
	StartedAt int64   `json:"startedAt"`
	ImageURL  *string `json:"imageUrl"`
}

// NowPlaying returns the user's current track, or nil when nothing is playing.
func (c *Client) NowPlaying(ctx context.Context, user string) (*NowPlaying, error) {
	u := c.BaseURL + "/api/user/nowplaying?name=" + url.QueryEscape(user)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	body, status, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, &APIError{Code: -1, Message: http.StatusText(status), Status: status}
	}
	var r struct {
		NowPlaying *NowPlaying `json:"nowPlaying"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return r.NowPlaying, nil
}
