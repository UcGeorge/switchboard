package core

import (
	"context"
	"sort"
	"time"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
)

// Bucket is one time slice of throughput.
type Bucket struct {
	Start     time.Time
	Total     int64
	Completed int64
	Failed    int64
}

// Overview is the dashboard's headline metrics for a time window.
type Overview struct {
	Window         time.Duration
	Queued         int64
	InFlight       int64
	ChannelsOnline int64
	ChannelsOpen   int64
	KeysActive     int64

	Total            int64
	Completed        int64
	Failed           int64
	Dropped          int64
	PromptTokens     int64
	CompletionTokens int64
	SuccessRate      float64 // percent of finished requests that completed
	RPM              float64 // requests per minute over the window

	P50Ms, P95Ms, P99Ms    int64
	TTFTP50Ms, TTFTP95Ms   int64
	QueueP50Ms, QueueP95Ms int64
	LatencySamples         int
	Buckets                []Bucket
	Models                 []sqlcgen.ModelUsageSinceRow
	Keys                   []sqlcgen.KeyUsageSinceRow
	ChannelUsage           []sqlcgen.ChannelUsageSinceRow
}

// Percentile returns the p-th percentile (0-100) of samples, 0 if empty.
func Percentile(samples []int64, p float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p / 100)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// QuickCounts returns the live gauges (for the TUI and header).
func (s *Service) QuickCounts(ctx context.Context) (queued, inFlight, online int64, err error) {
	if queued, err = s.Q.CountQueued(ctx); err != nil {
		return
	}
	if inFlight, err = s.Q.CountInFlight(ctx); err != nil {
		return
	}
	online, err = s.Q.CountChannelsByStatus(ctx, ChannelOnline)
	return
}

// Overview computes the dashboard metrics for the trailing window.
func (s *Service) Overview(ctx context.Context, window time.Duration) (Overview, error) {
	if window <= 0 {
		window = time.Hour
	}
	o := Overview{Window: window}
	var err error
	if o.Queued, o.InFlight, o.ChannelsOnline, err = s.QuickCounts(ctx); err != nil {
		return o, err
	}
	open, err := s.Q.ListOpenChannels(ctx)
	if err != nil {
		return o, err
	}
	o.ChannelsOpen = int64(len(open))
	if o.KeysActive, err = s.Q.CountActiveAPIKeys(ctx); err != nil {
		return o, err
	}
	since := time.Now().Add(-window).UnixMilli()
	st, err := s.Q.RequestStatsSince(ctx, since)
	if err != nil {
		return o, err
	}
	o.Total, o.Completed, o.Failed, o.Dropped = st.Total, st.Completed, st.Failed, st.Dropped
	o.PromptTokens, o.CompletionTokens = st.PromptTokens, st.CompletionTokens
	if finished := o.Completed + o.Failed + o.Dropped; finished > 0 {
		o.SuccessRate = float64(o.Completed) / float64(finished) * 100
	}
	o.RPM = float64(o.Total) / window.Minutes()

	samples, err := s.Q.LatencySamplesSince(ctx, since)
	if err != nil {
		return o, err
	}
	lat := make([]int64, 0, len(samples))
	ttft := make([]int64, 0, len(samples))
	queue := make([]int64, 0, len(samples))
	for _, r := range samples {
		lat = append(lat, r.LatencyMs)
		ttft = append(ttft, r.TtftMs)
		queue = append(queue, r.QueueMs)
	}
	o.LatencySamples = len(lat)
	o.P50Ms, o.P95Ms, o.P99Ms = Percentile(lat, 50), Percentile(lat, 95), Percentile(lat, 99)
	o.TTFTP50Ms, o.TTFTP95Ms = Percentile(ttft, 50), Percentile(ttft, 95)
	o.QueueP50Ms, o.QueueP95Ms = Percentile(queue, 50), Percentile(queue, 95)

	bucket := bucketFor(window)
	if o.Buckets, err = s.Throughput(ctx, window, bucket); err != nil {
		return o, err
	}
	if o.Models, err = s.Q.ModelUsageSince(ctx, since); err != nil {
		return o, err
	}
	if o.Keys, err = s.Q.KeyUsageSince(ctx, since); err != nil {
		return o, err
	}
	if o.ChannelUsage, err = s.Q.ChannelUsageSince(ctx, since); err != nil {
		return o, err
	}
	return o, nil
}

func bucketFor(window time.Duration) time.Duration {
	switch {
	case window <= 15*time.Minute:
		return 30 * time.Second
	case window <= time.Hour:
		return time.Minute
	case window <= 6*time.Hour:
		return 5 * time.Minute
	case window <= 24*time.Hour:
		return 30 * time.Minute
	case window <= 7*24*time.Hour:
		return 3 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// Throughput returns gap-filled request counts per bucket over the window.
func (s *Service) Throughput(ctx context.Context, window, bucket time.Duration) ([]Bucket, error) {
	now := time.Now()
	since := now.Add(-window)
	rows, err := s.Q.ThroughputBuckets(ctx, sqlcgen.ThroughputBucketsParams{BucketMs: bucket.Milliseconds(), Since: since.UnixMilli()})
	if err != nil {
		return nil, err
	}
	byBucket := make(map[int64]sqlcgen.ThroughputBucketsRow, len(rows))
	for _, r := range rows {
		byBucket[r.Bucket] = r
	}
	first := since.UnixMilli() / bucket.Milliseconds()
	last := now.UnixMilli() / bucket.Milliseconds()
	out := make([]Bucket, 0, last-first+1)
	for b := first; b <= last; b++ {
		row := byBucket[b]
		out = append(out, Bucket{Start: time.UnixMilli(b * bucket.Milliseconds()), Total: row.Total, Completed: row.Completed, Failed: row.Failed})
	}
	return out, nil
}
