//go:build integration

package repository

import (
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *AccountRepoSuite) TestList_DefaultSortByNameAsc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "z-account"})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "a-account"})

	accounts, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("a-account", accounts[0].Name)
	s.Require().Equal("z-account", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_DefaultRecentActivitySortsBeforePagination() {
	base := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	create := func(name string, createdAt, updatedAt time.Time, lastUsedAt *time.Time) *service.Account {
		return mustCreateAccount(s.T(), s.client, &service.Account{
			Name:       name,
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
			LastUsedAt: lastUsedAt,
		})
	}

	oldActivity := base.Add(time.Hour)
	tieActivity := base.Add(2 * time.Hour)
	createdActivity := base.Add(3 * time.Hour)
	editedActivity := base.Add(4 * time.Hour)
	calledActivity := base.Add(5 * time.Hour)

	create("old-activity", base, oldActivity, nil)
	olderTie := create("older-id-tie", base, tieActivity, nil)
	newerTie := create("newer-id-tie", base, tieActivity, nil)
	create("newly-created", createdActivity, createdActivity, nil)
	create("recently-edited", base, editedActivity, nil)
	create("recently-called", base, oldActivity, &calledActivity)
	s.Require().Greater(newerTie.ID, olderTie.ID)

	wantPages := [][]string{
		{"recently-called", "recently-edited"},
		{"newly-created", "newer-id-tie"},
		{"older-id-tie", "old-activity"},
	}
	for page, want := range wantPages {
		accounts, result, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page:      page + 1,
			PageSize:  2,
			SortBy:    service.AccountSortRecentActivity,
			SortOrder: pagination.SortOrderDesc,
		}, "", "", "", "", 0, "")
		s.Require().NoError(err)
		s.Require().Equal(int64(6), result.Total)
		s.Require().Len(accounts, len(want))
		for index, name := range want {
			s.Require().Equal(name, accounts[index].Name)
		}
	}
}

func (s *AccountRepoSuite) TestListWithFilters_DefaultRecentActivityPrioritizesOpenAIOAuthPlans() {
	base := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	create := func(name, platform, accountType, planType string, activity time.Time) {
		credentials := map[string]any{}
		if planType != "" {
			credentials["plan_type"] = planType
		}
		mustCreateAccount(s.T(), s.client, &service.Account{
			Name: name, Platform: platform, Type: accountType,
			Credentials: credentials, CreatedAt: activity, UpdatedAt: activity,
		})
	}

	create("newest-other", service.PlatformAnthropic, service.AccountTypeOAuth, "", base.Add(6*time.Hour))
	create("openai-free", service.PlatformOpenAI, service.AccountTypeOAuth, "free", base.Add(5*time.Hour))
	create("openai-plus", service.PlatformOpenAI, service.AccountTypeOAuth, "plus", base.Add(4*time.Hour))
	create("openai-team", service.PlatformOpenAI, service.AccountTypeOAuth, "team", base.Add(3*time.Hour))
	create("openai-pro", service.PlatformOpenAI, service.AccountTypeOAuth, "pro", base.Add(2*time.Hour))
	create("openai-api-key", service.PlatformOpenAI, service.AccountTypeAPIKey, "", base.Add(time.Hour))

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page: 1, PageSize: 10, SortBy: service.AccountSortRecentActivity, SortOrder: pagination.SortOrderDesc,
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 6)
	want := []string{"openai-pro", "openai-team", "openai-plus", "openai-free", "newest-other", "openai-api-key"}
	for index, name := range want {
		s.Require().Equal(name, accounts[index].Name)
	}
}

func (s *AccountRepoSuite) TestListWithFilters_SortByPriorityDesc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "low-priority", Priority: 10})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "high-priority", Priority: 90})

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "priority",
		SortOrder: "desc",
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("high-priority", accounts[0].Name)
	s.Require().Equal("low-priority", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByUpstreamBillingRateWithMissingLast() {
	makeAccount := func(name, status string, rate any) {
		extra := map[string]any{}
		if rate != nil {
			extra[service.UpstreamBillingProbeExtraKey] = map[string]any{
				"status": status,
				"data":   map[string]any{"effective_rate_multiplier": rate},
			}
		}
		mustCreateAccount(s.T(), s.client, &service.Account{Name: name, Extra: extra})
	}
	makeAccount("high-rate", service.UpstreamBillingProbeStatusOK, 0.8)
	makeAccount("low-rate", service.UpstreamBillingProbeStatusOK, 0.03)
	makeAccount("missing-rate", "", nil)
	makeAccount("unsupported-with-retained-rate", service.UpstreamBillingProbeStatusUnsupported, 0.01)

	for _, tc := range []struct {
		order string
		want  []string
	}{
		{order: "asc", want: []string{"low-rate", "high-rate", "missing-rate", "unsupported-with-retained-rate"}},
		{order: "desc", want: []string{"high-rate", "low-rate", "unsupported-with-retained-rate", "missing-rate"}},
	} {
		accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page: 1, PageSize: 10, SortBy: "upstream_billing_rate", SortOrder: tc.order,
		}, "", "", "", "", 0, "")
		s.Require().NoError(err)
		s.Require().Len(accounts, 4)
		for i, name := range tc.want {
			s.Require().Equal(name, accounts[i].Name)
		}
	}
}

func (s *AccountRepoSuite) TestListWithFilters_SortByCurrentUpstreamBillingRateDuringPeak() {
	now := time.Now()
	locations := []string{"UTC", "Asia/Shanghai", "America/New_York", "Europe/London"}
	var timezone string
	var minute int
	for _, name := range locations {
		location, err := time.LoadLocation(name)
		s.Require().NoError(err)
		local := now.In(location)
		candidate := local.Hour()*60 + local.Minute()
		if candidate >= 2 && candidate <= 1436 {
			timezone = name
			minute = candidate
			break
		}
	}
	s.Require().NotEmpty(timezone)

	peakStart := fmt.Sprintf("%02d:%02d", (minute-2)/60, (minute-2)%60)
	peakEnd := fmt.Sprintf("%02d:%02d", (minute+3)/60, (minute+3)%60)
	mustCreateAccount(s.T(), s.client, &service.Account{
		Name: "current-peak-rate",
		Extra: map[string]any{
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusOK,
				"data": map[string]any{
					"billing_scope":             "token",
					"resolved_rate_multiplier":  1.0,
					"effective_rate_multiplier": 1.0,
					"peak_rate_enabled":         true,
					"peak_start":                peakStart,
					"peak_end":                  peakEnd,
					"peak_rate_multiplier":      10.0,
					"timezone":                  timezone,
				},
			},
		},
	})
	mustCreateAccount(s.T(), s.client, &service.Account{
		Name: "current-off-peak-rate",
		Extra: map[string]any{
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusOK,
				"data": map[string]any{
					"effective_rate_multiplier": 5.0,
				},
			},
		},
	})

	for _, tc := range []struct {
		order string
		want  []string
	}{
		{order: "asc", want: []string{"current-off-peak-rate", "current-peak-rate"}},
		{order: "desc", want: []string{"current-peak-rate", "current-off-peak-rate"}},
	} {
		accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page: 1, PageSize: 10, SortBy: "upstream_billing_rate", SortOrder: tc.order,
		}, "", "", "", "", 0, "")
		s.Require().NoError(err)
		s.Require().Len(accounts, 2)
		for i, name := range tc.want {
			s.Require().Equal(name, accounts[i].Name)
		}
	}
}
