package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/easyavr/easyavr/internal/model"
)

// Dispatch fans an event out to the legacy notification rules and the AI event
// alert policies (tiered distribution). It never blocks the caller.
func (s *Service) Dispatch(ev model.AIEvent) {
	s.Notify(ev)
	s.Alert(ev)
}

// Alert evaluates enabled alert policies and delivers the immediate (delay 0)
// tiers for the matching event, recording every dispatch.
func (s *Service) Alert(ev model.AIEvent) {
	var policies []model.AlertPolicy
	s.db.Where("enabled = ?", true).Order("priority DESC, id ASC").Find(&policies)
	for _, p := range policies {
		if !s.matchPolicy(p, ev) {
			continue
		}
		if s.inCooldown(p) {
			continue
		}
		var tiers []model.AlertPolicyTier
		s.db.Where("policy_id = ? AND delay_sec = 0", p.ID).Order("tier ASC").Find(&tiers)
		for _, t := range tiers {
			if !tierApplies(t, ev) {
				continue
			}
			s.dispatchTier(p, t, ev, "immediate")
		}
	}
}

func (s *Service) matchPolicy(p model.AlertPolicy, ev model.AIEvent) bool {
	if p.Kind != "" && p.Kind != ev.Kind {
		return false
	}
	if p.EventType != "" && p.EventType != ev.EventType {
		return false
	}
	if p.ChannelID != 0 && p.ChannelID != ev.ChannelID {
		return false
	}
	if p.MinLevel != "" && levelRank[ev.Level] < levelRank[p.MinLevel] {
		return false
	}
	if kws := splitList(p.Keywords); len(kws) > 0 {
		hit := false
		low := strings.ToLower(ev.Summary + " " + ev.EventType)
		for _, kw := range kws {
			if strings.Contains(low, strings.ToLower(kw)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func tierApplies(t model.AlertPolicyTier, ev model.AIEvent) bool {
	if t.MinLevel == "" {
		return true
	}
	return levelRank[ev.Level] >= levelRank[t.MinLevel]
}

func (s *Service) inCooldown(p model.AlertPolicy) bool {
	if p.CooldownSec <= 0 {
		return false
	}
	var count int64
	cutoff := time.Now().Add(-time.Duration(p.CooldownSec) * time.Second)
	s.db.Model(&model.AlertDelivery{}).
		Where("policy_id = ? AND created_at >= ? AND reason != ?", p.ID, cutoff, "test").
		Count(&count)
	return count > 0
}

// dispatchTier delivers an event to every enabled target of a tier and records
// an AlertDelivery row per channel. It returns the number of successful sends.
func (s *Service) dispatchTier(p model.AlertPolicy, t model.AlertPolicyTier, ev model.AIEvent, reason string) int {
	delivered := 0
	for _, id := range parseIDs(t.TargetIDs) {
		var ch model.NotificationChannel
		if err := s.db.First(&ch, id).Error; err != nil || !ch.Enabled {
			continue
		}
		err := s.Deliver(ch, ev)
		rec := model.AlertDelivery{
			PolicyID: p.ID, PolicyName: p.Name, Tier: t.Tier, EventID: ev.ID,
			ChannelID: ch.ID, ChannelName: ch.Name, Reason: reason, Status: "success",
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
			log.Printf("[alert] policy %q tier %d -> %s failed: %v", p.Name, t.Tier, ch.Name, err)
		} else {
			delivered++
		}
		s.db.Create(&rec)
	}
	return delivered
}

// Escalate delivers overdue escalation tiers for unacknowledged events.
// It is safe to call repeatedly: a (policy, tier, event) delivery is recorded
// once, and acknowledged events stop escalating when AckRequired is set.
func (s *Service) Escalate(now time.Time) int {
	var policies []model.AlertPolicy
	s.db.Where("enabled = ?", true).Order("priority DESC, id ASC").Find(&policies)

	const lookback = time.Hour
	total := 0
	for _, p := range policies {
		var tiers []model.AlertPolicyTier
		s.db.Where("policy_id = ? AND delay_sec > 0", p.ID).Order("tier ASC").Find(&tiers)
		if len(tiers) == 0 {
			continue
		}
		if s.inCooldown(p) {
			continue
		}
		for _, t := range tiers {
			from := now.Add(-time.Duration(t.DelaySec) * time.Second).Add(-lookback)
			to := now.Add(-time.Duration(t.DelaySec) * time.Second)
			q := s.db.Model(&model.AIEvent{}).
				Where("occurred_at >= ? AND occurred_at <= ?", from, to)
			if p.AckRequired {
				q = q.Where("acked = ?", false)
			}
			if p.ChannelID != 0 {
				q = q.Where("channel_id = ?", p.ChannelID)
			}
			var events []model.AIEvent
			q.Order("occurred_at ASC").Limit(200).Find(&events)
			for _, ev := range events {
				if !s.matchPolicy(p, ev) || !tierApplies(t, ev) {
					continue
				}
				if s.hasDelivery(p.ID, t.Tier, ev.ID) {
					continue
				}
				total += s.dispatchTier(p, t, ev, "escalation")
			}
		}
	}
	return total
}

func (s *Service) hasDelivery(policyID uint, tier int, eventID uint) bool {
	var count int64
	s.db.Model(&model.AlertDelivery{}).
		Where("policy_id = ? AND tier = ? AND event_id = ?", policyID, tier, eventID).
		Count(&count)
	return count > 0
}

// StartEscalation runs the escalation scheduler until ctx is cancelled.
func (s *Service) StartEscalation(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if n := s.Escalate(now); n > 0 {
					log.Printf("[alert] escalated %d deliveries", n)
				}
			}
		}
	}()
}

// TestPolicy sends a synthetic event through every tier of a policy.
func (s *Service) TestPolicy(policyID uint) (int, error) {
	var p model.AlertPolicy
	if err := s.db.First(&p, policyID).Error; err != nil {
		return 0, err
	}
	var tiers []model.AlertPolicyTier
	s.db.Where("policy_id = ?", p.ID).Order("tier ASC").Find(&tiers)
	if len(tiers) == 0 {
		return 0, fmt.Errorf("policy has no tiers")
	}
	ev := model.AIEvent{
		Kind: "test", EventType: "test_alert", Level: "critical",
		Summary: "EasyAVR 告警策略测试", OccurredAt: time.Now(),
	}
	n := 0
	for _, t := range tiers {
		n += s.dispatchTier(p, t, ev, "test")
	}
	return n, nil
}
