package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/openai"
)

// Counts are the live gauges shown in the sidebar.
type Counts struct{ Queued, InFlight, Online int64 }

// Flash is a one-shot message shown at the top of the next page.
type Flash struct{ Kind, Message string }

// Page is embedded by every page view.
type Page struct {
	Title    string
	Nav      string
	Instance string
	Version  string
	CSRF     string
	Path     string
	Flash    *Flash
	Counts   Counts
	Now      time.Time
}

// RequestView decorates a request row for templates.
type RequestView struct {
	sqlcgen.Request
	KeyName       string
	ChannelName   string
	Prompt        string
	Answer        string
	LatencyMs     int64
	QueueMs       int64
	TTFTMs        int64
	ToolCallCount int
}

func requestView(r sqlcgen.Request, names nameMaps) RequestView {
	v := RequestView{Request: r, KeyName: names.keys[r.ApiKeyID]}
	if r.ChannelID != nil {
		v.ChannelName = names.channels[*r.ChannelID]
	}
	if req, err := core.ParseRequestBody(r); err == nil {
		last := openai.LastMessage(req.Messages)
		v.Prompt = oneLine(last.Text(), 160)
		if v.Prompt == "" && len(last.ToolCalls) > 0 {
			v.Prompt = "[tool calls]"
		}
		if last.Role == "tool" {
			v.Prompt = "[tool result] " + v.Prompt
		}
	}
	if r.ResponseText != nil {
		v.Answer = oneLine(*r.ResponseText, 160)
	}
	if a, ok := core.ParseAnswer(r); ok {
		v.ToolCallCount = len(a.ToolCalls)
		if v.Answer == "" && len(a.ToolCalls) > 0 {
			names := make([]string, 0, len(a.ToolCalls))
			for _, tc := range a.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			v.Answer = "→ " + strings.Join(names, ", ")
		}
	}
	if r.CompletedAt != nil {
		v.LatencyMs = *r.CompletedAt - r.CreatedAt
	}
	if r.ClaimedAt != nil {
		v.QueueMs = *r.ClaimedAt - r.CreatedAt
	}
	if r.FirstTokenAt != nil {
		v.TTFTMs = *r.FirstTokenAt - r.CreatedAt
	}
	return v
}

// ChannelView decorates a channel.
type ChannelView struct {
	sqlcgen.Channel
	InFlight     int64
	ModelList    []string
	AvgLatencyMs int64
	TokenName    string
	Closed       bool
}

// EventView decorates an event.
type EventView struct {
	sqlcgen.Event
	Payload    map[string]any
	DataPretty string
}

func eventView(e sqlcgen.Event) EventView {
	v := EventView{Event: e}
	if e.Data != "" && e.Data != "{}" {
		_ = json.Unmarshal([]byte(e.Data), &v.Payload)
		if len(v.Payload) > 0 {
			parts := make([]string, 0, len(v.Payload))
			for k, val := range v.Payload {
				parts = append(parts, fmt.Sprintf("%s=%v", k, val))
			}
			sortStrings(parts)
			v.DataPretty = strings.Join(parts, "  ")
		}
	}
	return v
}

// MessageView is one chat message.
type MessageView struct {
	Role       string
	Text       string
	Name       string
	ToolCallID string
	ToolCalls  []openai.ToolCall
	Index      int
}

func messageViews(msgs []openai.Message) []MessageView {
	out := make([]MessageView, 0, len(msgs))
	for i, m := range msgs {
		out = append(out, MessageView{Role: m.Role, Text: m.Text(), Name: m.Name, ToolCallID: m.ToolCallID, ToolCalls: m.ToolCalls, Index: i})
	}
	return out
}

// TurnView is one exchange in a reconstructed conversation.
type TurnView struct {
	Request   RequestView
	Messages  []MessageView
	Answer    *openai.Answer
	HasAnswer bool
}

// Pager paginates a list. Base is the path plus query (without page=),
// ending in '?' or '&'.
type Pager struct {
	Page    int
	Pages   int
	Total   int64
	PerPage int
	Base    string
}

func newPager(path string, q url.Values, page, perPage int, total int64) Pager {
	q = cloneValues(q)
	q.Del("page")
	base := path + "?"
	if enc := q.Encode(); enc != "" {
		base += enc + "&"
	}
	pages := int((total + int64(perPage) - 1) / int64(perPage))
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	return Pager{Page: page, Pages: pages, Total: total, PerPage: perPage, Base: base}
}

// URL links to page n.
func (p Pager) URL(n int) string { return p.Base + "page=" + strconv.Itoa(n) }

// Prev and Next are the neighbouring pages.
func (p Pager) Prev() int { return max(p.Page-1, 1) }
func (p Pager) Next() int { return min(p.Page+1, p.Pages) }

// Range is a window of page numbers around the current one.
func (p Pager) Range() []int {
	start := max(p.Page-3, 1)
	end := min(start+6, p.Pages)
	start = max(end-6, 1)
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

// Offset is the row offset for the current page.
func (p Pager) Offset() int64 { return int64((p.Page - 1) * p.PerPage) }

// RequestTable is the data for the request-table partial.
type RequestTable struct {
	Rows             []RequestView
	Pager            Pager
	ID               string
	URL              string
	ShowKey          bool
	ShowChannel      bool
	ShowConversation bool
	EmptyText        string
}

// EventTable is the data for the event-table partial.
type EventTable struct {
	Rows  []EventView
	Pager Pager
	ID    string
	URL   string
}

// ChartBar is one bucket in the throughput chart.
type ChartBar struct {
	Label                    string
	Total, Completed, Failed int64
	X, W, Y, H               float64
	YOk, HOk, YFail, HFail   float64
}

// ChartLabel is an x-axis label.
type ChartLabel struct {
	X    float64
	Text string
}

// ChartView renders buckets as an SVG bar chart.
type ChartView struct {
	Bars   []ChartBar
	Labels []ChartLabel
	W, H   int
	Max    int64
}

func chartView(buckets []core.Bucket, window time.Duration) ChartView {
	const W, H, pad = 720, 150, 18
	cv := ChartView{W: W, H: H}
	if len(buckets) == 0 {
		return cv
	}
	for _, b := range buckets {
		if b.Total > cv.Max {
			cv.Max = b.Total
		}
	}
	plotH := float64(H - pad - 6)
	slot := float64(W) / float64(len(buckets))
	barW := slot * 0.72
	labelEvery := max(len(buckets)/6, 1)
	tf := "15:04"
	if window > 24*time.Hour {
		tf = "Jan 2"
	}
	for i, b := range buckets {
		x := float64(i)*slot + (slot-barW)/2
		bar := ChartBar{Label: b.Start.Format("Jan 2 15:04"), Total: b.Total, Completed: b.Completed, Failed: b.Failed, X: x, W: barW}
		scale := func(n int64) float64 {
			if cv.Max == 0 {
				return 0
			}
			return plotH * float64(n) / float64(cv.Max)
		}
		bar.H = scale(b.Total)
		if bar.H < 2 && b.Total > 0 {
			bar.H = 2
		}
		bar.Y = 6 + plotH - bar.H
		bar.HOk = scale(b.Completed)
		bar.YOk = 6 + plotH - bar.HOk
		bar.HFail = scale(b.Failed)
		bar.YFail = bar.YOk - bar.HFail
		cv.Bars = append(cv.Bars, bar)
		// Centre labels within their group so the first is not clipped at
		// the chart's left edge.
		if i%labelEvery == labelEvery/2 {
			cv.Labels = append(cv.Labels, ChartLabel{X: x + barW/2, Text: b.Start.Format(tf)})
		}
	}
	return cv
}

type nameMaps struct {
	keys     map[string]string
	channels map[string]string
	tokens   map[string]string
}

// template helpers

func funcMap() template.FuncMap {
	return template.FuncMap{
		"ago":     ago,
		"ts":      func(ms int64) string { return time.UnixMilli(ms).Format("Jan 2, 2006 15:04:05") },
		"tsShort": func(ms int64) string { return time.UnixMilli(ms).Format("15:04:05") },
		"tsp": func(p *int64) string {
			if p == nil {
				return "—"
			}
			return time.UnixMilli(*p).Format("15:04:05.000")
		},
		"dur": durStr,
		"durp": func(p *int64) string {
			if p == nil {
				return "—"
			}
			return durStr(*p)
		},
		"n":   thousands,
		"pct": func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "%" },
		"f":   func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
		"f2":  func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) },
		"deref": func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		},
		"derefI": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"trunc":      oneLine,
		"dict":       dict,
		"json":       prettyJSON,
		"lower":      strings.ToLower,
		"add":        func(a, b any) int64 { return toInt64(a) + toInt64(b) },
		"sub":        func(a, b any) int64 { return toInt64(a) - toInt64(b) },
		"mul":        func(a, b any) int64 { return toInt64(a) * toInt64(b) },
		"isActive":   core.IsActive,
		"isTerminal": core.IsTerminal,
		"trimReq":    func(id string) string { return shortID(id, "req_") },
		"trimConv":   func(id string) string { return shortID(id, "conv_") },
		"roleAbbr": func(role string) string {
			switch role {
			case "user":
				return "U"
			case "assistant":
				return "A"
			case "system":
				return "S"
			case "developer":
				return "D"
			case "tool":
				return "T"
			}
			return "?"
		},
		"has": func(list []string, s string) bool {
			for _, v := range list {
				if v == s {
					return true
				}
			}
			return false
		},
		"joinList":  strings.Join,
		"splitList": core.SplitList,
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"safe":        func(s string) template.HTML { return template.HTML(s) },
		"attr":        func(s string) template.HTMLAttr { return template.HTMLAttr(s) },
		"windowLabel": windowLabel,
		"statusEmoji": func(s string) string { return s },
	}
}

func ago(ms int64) string {
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func durStr(ms int64) string {
	switch {
	case ms < 0:
		return "—"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 10_000:
		return fmt.Sprintf("%.2fs", float64(ms)/1000)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	case ms < 3_600_000:
		return fmt.Sprintf("%dm %ds", ms/60_000, (ms%60_000)/1000)
	default:
		return fmt.Sprintf("%dh %dm", ms/3_600_000, (ms%3_600_000)/60_000)
	}
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func shortID(id, prefix string) string {
	rest := strings.TrimPrefix(id, prefix)
	if len(rest) > 10 {
		rest = "…" + rest[len(rest)-8:]
	}
	return prefix + rest
}

func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		m[k] = kv[i+1]
	}
	return m
}

func prettyJSON(v any) string {
	switch t := v.(type) {
	case string:
		var raw any
		if json.Unmarshal([]byte(t), &raw) == nil {
			b, _ := json.MarshalIndent(raw, "", "  ")
			return string(b)
		}
		return t
	case *string:
		if t == nil {
			return ""
		}
		return prettyJSON(*t)
	default:
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case *int64:
		if t == nil {
			return 0
		}
		return *t
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && rv.CanInt() {
		return rv.Int()
	}
	return 0
}

func windowLabel(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func cloneValues(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
