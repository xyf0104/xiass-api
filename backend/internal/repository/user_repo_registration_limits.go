package repository

import (
	"context"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"golang.org/x/net/idna"
)

// Called under the IP/domain advisory locks in the user-creation transaction.
// A failed creation or invitation claim rolls back the recorded IP as well.
func checkRegistrationLimits(ctx context.Context, client *dbent.Client, limits service.RegistrationLimits, domain string) error {
	if limits.IPDailyLimitEnabled {
		if limits.ClientIP == "" {
			return service.ErrServiceUnavailable
		}
		start := timezone.Today()
		exists, err := client.User.Query().Where(
			user.RegistrationIPEQ(limits.ClientIP),
			user.CreatedAtGTE(start),
			user.CreatedAtLT(start.AddDate(0, 0, 1)),
		).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			return service.ErrRegistrationIPDailyLimit
		}
	}
	if domain != "" {
		exists, err := client.User.Query().Where(registrationEmailDomainPredicate(domain)).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			return service.ErrRegistrationDomainLimit
		}
	}
	return nil
}

func registrationEmailDomainPredicate(domain string) predicate.User {
	domains := []string{domain}
	if unicodeDomain, err := idna.Lookup.ToUnicode(domain); err == nil && unicodeDomain != domain {
		domains = append(domains, unicodeDomain)
	}
	return func(s *entsql.Selector) {
		predicates := make([]*entsql.Predicate, 0, len(domains)*2)
		for _, candidate := range domains {
			for _, suffix := range []string{"%@" + candidate, "%." + candidate} {
				predicates = append(predicates, entsql.P(func(b *entsql.Builder) {
					b.WriteString("LOWER(RTRIM(TRIM(").Ident(s.C(user.FieldEmail)).
						WriteString("), '.')) LIKE ").Arg(suffix)
				}))
			}
		}
		s.Where(entsql.Or(predicates...))
	}
}
