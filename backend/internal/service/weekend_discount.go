package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const SettingKeyWeekendDiscount = "weekend_discount"
const WeekendDiscountMultiplier = 0.8
const DiscountModeWeekends = "weekends"
const DiscountModeCustom = "custom"

var weekendLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type WeekendDiscountSettings struct {
	Enabled  bool       `json:"enabled"`
	Mode     string     `json:"mode,omitempty"`
	StartsAt *time.Time `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
}

type WeekendDiscountStatus struct {
	WeekendDiscountSettings
	Timezone           string     `json:"timezone"`
	DiscountMultiplier float64    `json:"discount_multiplier"`
	AppliedMultiplier  float64    `json:"applied_multiplier"`
	Active             bool       `json:"active"`
	ServerTime         time.Time  `json:"server_time"`
	NextChangeAt       *time.Time `json:"next_change_at"`
}

type cachedWeekendDiscount struct {
	settings WeekendDiscountSettings
	loadedAt time.Time
}

func (s WeekendDiscountSettings) Validate() error {
	if s.Mode != "" && s.Mode != DiscountModeWeekends && s.Mode != DiscountModeCustom {
		return errors.New("activity mode must be weekends or custom")
	}
	if s.StartsAt != nil && (s.StartsAt.Year() < 2000 || s.StartsAt.Year() > 2200) || s.EndsAt != nil && (s.EndsAt.Year() < 2000 || s.EndsAt.Year() > 2200) {
		return errors.New("activity dates must be between 2000 and 2200")
	}
	if s.StartsAt != nil && s.EndsAt != nil && !s.EndsAt.After(*s.StartsAt) {
		return errors.New("end time must be after start time")
	}
	return nil
}

func (s WeekendDiscountSettings) MultiplierAt(at time.Time) float64 {
	if !s.Enabled || at.IsZero() || s.StartsAt != nil && at.Before(*s.StartsAt) || s.EndsAt != nil && !at.Before(*s.EndsAt) {
		return 1
	}
	if s.Mode == DiscountModeCustom {
		return WeekendDiscountMultiplier
	}
	if s.Mode != "" && s.Mode != DiscountModeWeekends {
		return 1
	}
	day := at.In(weekendLocation).Weekday()
	if day == time.Saturday || day == time.Sunday {
		return WeekendDiscountMultiplier
	}
	return 1
}

func (s WeekendDiscountSettings) Status(at time.Time) WeekendDiscountStatus {
	r := WeekendDiscountStatus{WeekendDiscountSettings: s, Timezone: "Asia/Shanghai", DiscountMultiplier: WeekendDiscountMultiplier, AppliedMultiplier: s.MultiplierAt(at), ServerTime: at}
	if r.Mode == "" {
		r.Mode = DiscountModeWeekends
	}
	r.Active = r.AppliedMultiplier < 1
	if !s.Enabled || s.EndsAt != nil && !at.Before(*s.EndsAt) {
		return r
	}
	if s.Mode == DiscountModeCustom {
		if s.StartsAt != nil && at.Before(*s.StartsAt) {
			r.NextChangeAt = s.StartsAt
		} else {
			r.NextChangeAt = s.EndsAt
		}
		return r
	}
	next := at.In(weekendLocation)
	if s.StartsAt != nil && next.Before(*s.StartsAt) {
		next = s.StartsAt.In(weekendLocation)
	}
	if r.Active {
		days := (int(time.Monday) - int(next.Weekday()) + 7) % 7
		next = time.Date(next.Year(), next.Month(), next.Day()+days, 0, 0, 0, 0, weekendLocation)
		if s.EndsAt != nil && s.EndsAt.Before(next) {
			next = *s.EndsAt
		}
	} else {
		for next.Weekday() != time.Saturday && next.Weekday() != time.Sunday {
			next = time.Date(next.Year(), next.Month(), next.Day()+1, 0, 0, 0, 0, weekendLocation)
		}
		if s.EndsAt != nil && !next.Before(*s.EndsAt) {
			return r
		}
	}
	r.NextChangeAt = &next
	return r
}

// Configuration is cached, never the time-dependent active flag. Midnight and
// schedule boundaries therefore take effect without rewriting group prices.
func (s *SettingService) GetWeekendDiscount(ctx context.Context) (WeekendDiscountSettings, error) {
	if s == nil || s.settingRepo == nil {
		return WeekendDiscountSettings{}, nil
	}
	s.weekendDiscountMu.Lock()
	defer s.weekendDiscountMu.Unlock()
	if c := s.weekendDiscountCache; c != nil && time.Since(c.loadedAt) < 15*time.Second {
		return c.settings, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyWeekendDiscount)
	var settings WeekendDiscountSettings
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	}
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &settings)
	}
	if err == nil {
		err = settings.Validate()
	}
	if err != nil {
		// Retain the last known schedule during a transient storage outage.
		if c := s.weekendDiscountCache; c != nil {
			c.loadedAt = time.Now()
			return c.settings, nil
		}
		return WeekendDiscountSettings{}, fmt.Errorf("load weekend discount: %w", err)
	}
	s.weekendDiscountCache = &cachedWeekendDiscount{settings: settings, loadedAt: time.Now()}
	return settings, nil
}

func (s *SettingService) SetWeekendDiscount(ctx context.Context, settings WeekendDiscountSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if s == nil || s.settingRepo == nil {
		return errors.New("settings store unavailable")
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	s.weekendDiscountMu.Lock()
	defer s.weekendDiscountMu.Unlock()
	if err := s.settingRepo.Set(ctx, SettingKeyWeekendDiscount, string(data)); err != nil {
		return err
	}
	s.weekendDiscountCache = &cachedWeekendDiscount{settings: settings, loadedAt: time.Now()}
	return nil
}

func (s *SettingService) WeekendMultiplierAt(ctx context.Context, at time.Time) float64 {
	settings, err := s.GetWeekendDiscount(ctx)
	if err != nil {
		return 1
	}
	return settings.MultiplierAt(at)
}

func applyWeekendUsageDiscount(ctx context.Context, settings *SettingService, usageLog *UsageLog, p *postUsageBillingParams) error {
	if p == nil || p.Cost == nil || p.weekendDiscountApplied {
		return nil
	}
	config, err := settings.GetWeekendDiscount(ctx)
	if err != nil {
		return err
	}
	at := p.PricingAt
	if at.IsZero() && usageLog != nil {
		at = usageLog.CreatedAt
	}
	if at.IsZero() {
		at = time.Now()
	}
	multiplier := config.MultiplierAt(at)
	// Discount the final user price, including explicit final-price overrides;
	// keep raw/upstream costs and token counters intact for cost accounting.
	cost := *p.Cost
	cost.ActualCost *= multiplier
	p.Cost = &cost
	if usageLog != nil {
		usageLog.ActualCost = cost.ActualCost
		usageLog.RateMultiplier *= multiplier
	}
	p.weekendDiscountApplied = true
	return nil
}
