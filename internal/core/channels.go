package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/events"
	"github.com/ucgeorge/switchboard/internal/ids"
)

// Channel statuses.
const (
	ChannelOnline   = "online"
	ChannelOffline  = "offline"
	ChannelDraining = "draining"
)

// ManualChannelName is the channel used when a human answers from the CLI or
// dashboard.
const ManualChannelName = "manual"

// Channel errors.
var (
	ErrChannelNotFound = errors.New("channel not found")
	ErrChannelClosed   = errors.New("channel is closed")
	ErrChannelNotOwned = errors.New("channel belongs to a different agent token")
)

// ChannelInput holds the editable fields of a channel.
type ChannelInput struct {
	Name        string
	Description string
	Models      []string
	Concurrency int64
}

func (in *ChannelInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("name is required")
	}
	if len(in.Name) > 80 {
		return errors.New("name must be 80 characters or fewer")
	}
	if len(in.Models) == 0 {
		in.Models = []string{"*"}
	}
	if in.Concurrency <= 0 {
		in.Concurrency = 1
	}
	if in.Concurrency > 256 {
		in.Concurrency = 256
	}
	return nil
}

// OpenChannel registers an agent instance. If an open channel with the same
// name exists it is reused when it is offline or owned by the same token;
// otherwise a suffixed name is allocated so instances never collide.
func (s *Service) OpenChannel(ctx context.Context, tokenID string, in ChannelInput) (sqlcgen.Channel, bool, error) {
	if err := in.normalize(); err != nil {
		return sqlcgen.Channel{}, false, err
	}
	now := nowMs()
	existing, err := s.Q.GetOpenChannelByName(ctx, in.Name)
	if err == nil {
		sameOwner := deref(existing.AgentTokenID) == tokenID
		if sameOwner {
			if err := s.Q.ReopenChannel(ctx, sqlcgen.ReopenChannelParams{
				LastSeenAt:   now,
				AgentTokenID: nilIfEmpty(tokenID),
				Models:       JoinList(in.Models),
				Concurrency:  in.Concurrency,
				Description:  in.Description,
				ID:           existing.ID,
			}); err != nil {
				return sqlcgen.Channel{}, false, err
			}
			ch, err := s.Q.GetChannel(ctx, existing.ID)
			if err != nil {
				return sqlcgen.Channel{}, false, err
			}
			s.publish(events.Event{Kind: events.ChannelOnline, ChannelID: ch.ID, Message: fmt.Sprintf("Channel %q reconnected", ch.Name), Data: map[string]any{"models": in.Models, "concurrency": in.Concurrency}})
			s.signalQueue()
			return ch, false, nil
		}
		// Another live agent owns this name: allocate a unique one.
		in.Name = in.Name + "-" + ids.Random(4)
	} else if !isNoRows(err) {
		return sqlcgen.Channel{}, false, err
	}
	ch, err := s.Q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		ID:           ids.New("ch"),
		Name:         in.Name,
		Description:  in.Description,
		AgentTokenID: nilIfEmpty(tokenID),
		Status:       ChannelOnline,
		Models:       JoinList(in.Models),
		Concurrency:  in.Concurrency,
		LastSeenAt:   now,
		CreatedAt:    now,
	})
	if err != nil {
		return sqlcgen.Channel{}, false, err
	}
	s.publish(events.Event{Kind: events.ChannelOpened, ChannelID: ch.ID, Message: fmt.Sprintf("Channel %q opened", ch.Name), Data: map[string]any{"models": in.Models, "concurrency": in.Concurrency, "token_id": tokenID}})
	s.signalQueue()
	return ch, true, nil
}

// GetChannel fetches a channel.
func (s *Service) GetChannel(ctx context.Context, id string) (sqlcgen.Channel, error) {
	ch, err := s.Q.GetChannel(ctx, id)
	if isNoRows(err) {
		return ch, ErrChannelNotFound
	}
	return ch, err
}

// ListChannels returns every channel, open ones first.
func (s *Service) ListChannels(ctx context.Context) ([]sqlcgen.Channel, error) {
	return s.Q.ListChannels(ctx)
}

// ListOpenChannels returns channels that have not been closed.
func (s *Service) ListOpenChannels(ctx context.Context) ([]sqlcgen.Channel, error) {
	return s.Q.ListOpenChannels(ctx)
}

// ListOnlineChannels returns channels currently online.
func (s *Service) ListOnlineChannels(ctx context.Context) ([]sqlcgen.Channel, error) {
	return s.Q.ListOnlineChannels(ctx)
}

// ownedChannel loads a channel and verifies it is open and driven by tokenID
// (an empty tokenID skips the ownership check, for dashboard/CLI use).
func (s *Service) ownedChannel(ctx context.Context, q *sqlcgen.Queries, id, tokenID string) (sqlcgen.Channel, error) {
	ch, err := q.GetChannel(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return ch, ErrChannelNotFound
		}
		return ch, err
	}
	if ch.ClosedAt != nil {
		return ch, ErrChannelClosed
	}
	if tokenID != "" && deref(ch.AgentTokenID) != tokenID {
		return ch, ErrChannelNotOwned
	}
	return ch, nil
}

// Heartbeat marks a channel as seen and returns its current record.
func (s *Service) Heartbeat(ctx context.Context, id, tokenID string) (sqlcgen.Channel, error) {
	ch, err := s.ownedChannel(ctx, s.Q, id, tokenID)
	if err != nil {
		return ch, err
	}
	wasOffline := ch.Status == ChannelOffline
	if err := s.Q.TouchChannel(ctx, sqlcgen.TouchChannelParams{LastSeenAt: nowMs(), ID: id}); err != nil {
		return ch, err
	}
	if wasOffline {
		s.publish(events.Event{Kind: events.ChannelOnline, ChannelID: id, Message: fmt.Sprintf("Channel %q back online", ch.Name)})
		s.signalQueue()
	}
	return s.Q.GetChannel(ctx, id)
}

// CloseChannel takes a channel out of service and returns its in-flight
// requests to the queue.
func (s *Service) CloseChannel(ctx context.Context, id, tokenID, reason string) error {
	ch, err := s.ownedChannel(ctx, s.Q, id, tokenID)
	if err != nil {
		return err
	}
	if reason == "" {
		reason = "channel closed"
	}
	if _, err := s.Q.CloseChannel(ctx, sqlcgen.CloseChannelParams{ClosedAt: ptr(nowMs()), ID: id}); err != nil {
		return err
	}
	s.publish(events.Event{Kind: events.ChannelClosed, ChannelID: id, Message: fmt.Sprintf("Channel %q closed", ch.Name), Data: map[string]any{"reason": reason}})
	return s.releaseChannelRequests(ctx, ch, reason)
}

// SetChannelDraining stops new claims while in-flight work finishes.
func (s *Service) SetChannelDraining(ctx context.Context, id string, draining bool) error {
	ch, err := s.GetChannel(ctx, id)
	if err != nil {
		return err
	}
	status := ChannelOnline
	if draining {
		status = ChannelDraining
	}
	if err := s.Q.SetChannelStatus(ctx, sqlcgen.SetChannelStatusParams{Status: status, ID: id}); err != nil {
		return err
	}
	msg := fmt.Sprintf("Channel %q draining", ch.Name)
	if !draining {
		msg = fmt.Sprintf("Channel %q resumed", ch.Name)
		s.signalQueue()
	}
	s.publish(events.Event{Kind: events.ChannelOnline, ChannelID: id, Message: msg, Data: map[string]any{"status": status}})
	return nil
}

// UpdateChannel edits channel configuration from the dashboard.
func (s *Service) UpdateChannel(ctx context.Context, id string, in ChannelInput) error {
	if err := in.normalize(); err != nil {
		return err
	}
	if _, err := s.GetChannel(ctx, id); err != nil {
		return err
	}
	if err := s.Q.UpdateChannelConfig(ctx, sqlcgen.UpdateChannelConfigParams{
		Name: in.Name, Description: in.Description, Models: JoinList(in.Models), Concurrency: in.Concurrency, ID: id,
	}); err != nil {
		return err
	}
	s.signalQueue()
	return nil
}

// DeleteChannel removes a closed channel record.
func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	ch, err := s.GetChannel(ctx, id)
	if err != nil {
		return err
	}
	if ch.ClosedAt == nil {
		if err := s.CloseChannel(ctx, id, "", "channel deleted"); err != nil {
			return err
		}
	}
	_, err = s.Q.DeleteChannel(ctx, id)
	return err
}

// ChannelModels returns the model patterns a channel serves.
func ChannelModels(c sqlcgen.Channel) []string {
	m := SplitList(c.Models)
	if len(m) == 0 {
		return []string{"*"}
	}
	return m
}

// ChannelServesModel reports whether a channel accepts a model.
func ChannelServesModel(c sqlcgen.Channel, model string) bool {
	return MatchModel(ChannelModels(c), model)
}

// ChannelInFlight counts requests currently held by a channel.
func (s *Service) ChannelInFlight(ctx context.Context, id string) (int64, error) {
	return s.Q.CountChannelInFlight(ctx, strp(id))
}

// AvgLatencyMs is the mean end-to-end latency of requests a channel served.
func AvgLatencyMs(c sqlcgen.Channel) int64 {
	if c.ServedCount == 0 {
		return 0
	}
	return c.TotalLatencyMs / c.ServedCount
}

// manualChannel returns (creating if needed) the channel used for human
// answers from the CLI or dashboard.
func (s *Service) manualChannel(ctx context.Context) (sqlcgen.Channel, error) {
	ch, err := s.Q.GetOpenChannelByName(ctx, ManualChannelName)
	if err == nil {
		_ = s.Q.TouchChannel(ctx, sqlcgen.TouchChannelParams{LastSeenAt: nowMs(), ID: ch.ID})
		return ch, nil
	}
	if !isNoRows(err) {
		return ch, err
	}
	ch, _, err = s.OpenChannel(ctx, "", ChannelInput{
		Name:        ManualChannelName,
		Description: "Human answers submitted from the dashboard or CLI",
		Models:      []string{"*"},
		Concurrency: 256,
	})
	return ch, err
}

// releaseChannelRequests requeues (or fails) everything a channel holds.
func (s *Service) releaseChannelRequests(ctx context.Context, ch sqlcgen.Channel, reason string) error {
	inflight, err := s.Q.ListInFlightForChannel(ctx, strp(ch.ID))
	if err != nil {
		return err
	}
	for _, r := range inflight {
		s.requeueOrFail(ctx, r, reason, true)
	}
	if len(inflight) > 0 {
		s.signalQueue()
	}
	return nil
}

// markStaleChannels flips silent channels offline and releases their work.
func (s *Service) markStaleChannels(ctx context.Context) {
	cutoff := nowMs() - s.Settings().ChannelStale.Milliseconds()
	stale, err := s.Q.ListStaleOnlineChannels(ctx, cutoff)
	if err != nil {
		s.Log.Warn("list stale channels", "err", err)
		return
	}
	for _, ch := range stale {
		if ch.Name == ManualChannelName {
			continue
		}
		if err := s.Q.SetChannelStatus(ctx, sqlcgen.SetChannelStatusParams{Status: ChannelOffline, ID: ch.ID}); err != nil {
			continue
		}
		s.publish(events.Event{Kind: events.ChannelOffline, Level: events.LevelWarn, ChannelID: ch.ID,
			Message: fmt.Sprintf("Channel %q went offline (no activity for %s)", ch.Name, s.Settings().ChannelStale)})
		_ = s.releaseChannelRequests(ctx, ch, "channel went offline")
	}
}
