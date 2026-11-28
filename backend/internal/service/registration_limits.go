package service

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

var (
	ErrRegistrationIPDailyLimit = infraerrors.TooManyRequests("REGISTRATION_IP_DAILY_LIMIT", "该 IP 今天已注册过账号，请明天再试。")
	ErrRegistrationDomainLimit  = infraerrors.Conflict("REGISTRATION_EMAIL_DOMAIN_LIMIT", "该自定义邮箱域名已注册过账号，请使用公共邮箱或其他域名。")
)

type RegistrationLimits struct {
	ClientIP                string
	IPDailyLimitEnabled     bool
	EmailDomainQuotaEnabled bool
}

type registrationLimitsContextKey struct{}

func WithRegistrationLimits(ctx context.Context, limits RegistrationLimits) context.Context {
	return context.WithValue(ctx, registrationLimitsContextKey{}, limits)
}

func RegistrationLimitsFromContext(ctx context.Context) RegistrationLimits {
	limits, _ := ctx.Value(registrationLimitsContextKey{}).(RegistrationLimits)
	return limits
}

func (s *AuthService) registrationContext(ctx context.Context) (context.Context, error) {
	limits := RegistrationLimitsFromContext(ctx)
	if s.settingService != nil {
		values, err := s.settingService.settingRepo.GetMultiple(ctx, []string{
			SettingKeyRegistrationIPDailyLimitEnabled,
			SettingKeyRegistrationEmailDomainQuotaEnabled,
		})
		if err != nil {
			return nil, ErrServiceUnavailable
		}
		limits.IPDailyLimitEnabled = values[SettingKeyRegistrationIPDailyLimitEnabled] == "true"
		limits.EmailDomainQuotaEnabled = values[SettingKeyRegistrationEmailDomainQuotaEnabled] == "true"
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(limits.ClientIP)); err == nil {
		limits.ClientIP = ip.NormalizeRateLimitIP(addr.Unmap().String())
	} else {
		limits.ClientIP = ""
		if limits.IPDailyLimitEnabled {
			return nil, ErrServiceUnavailable
		}
	}
	return WithRegistrationLimits(ctx, limits), nil
}

func isRegistrationLimitError(err error) bool {
	return errors.Is(err, ErrRegistrationIPDailyLimit) || errors.Is(err, ErrRegistrationDomainLimit)
}

// Exempt exact provider domains only. Customer-controlled subdomains still share
// a registrable-domain quota, even when their parent is a public mail provider.
var publicRegistrationEmailDomains = map[string]bool{
	"qq.com": true, "vip.qq.com": true, "foxmail.com": true,
	"163.com": true, "126.com": true, "yeah.net": true, "188.com": true,
	"vip.163.com": true, "vip.126.com": true,
	"gmail.com": true, "googlemail.com": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"outlook.cn": true, "hotmail.co.uk": true, "live.cn": true,
	"icloud.com": true, "me.com": true, "mac.com": true,
	"yahoo.com": true, "yahoo.co.jp": true, "yahoo.co.uk": true,
	"yahoo.com.hk": true, "yahoo.com.tw": true, "ymail.com": true, "rocketmail.com": true,
	"sina.com": true, "sina.cn": true, "vip.sina.com": true, "sohu.com": true,
	"aliyun.com": true, "tom.com": true, "139.com": true, "189.cn": true, "wo.cn": true,
	"proton.me": true, "protonmail.com": true, "pm.me": true,
	"aol.com": true, "mail.com": true, "gmx.com": true, "gmx.net": true,
	"fastmail.com": true, "tuta.com": true, "tutanota.com": true,
	"zoho.com": true, "zohomail.com": true,
	"mail.ru": true, "bk.ru": true, "inbox.ru": true, "list.ru": true,
	"yandex.com": true, "yandex.ru": true, "rambler.ru": true,
	"naver.com": true, "daum.net": true, "hanmail.net": true,
}

// RegistrationEmailDomain returns the quota bucket for a custom mail domain.
// Synthetic OAuth identities have no customer-owned email domain.
func RegistrationEmailDomain(email string) (string, error) {
	if isReservedEmail(email) {
		return "", nil
	}
	_, domain, ok := splitEmailForPolicy(email)
	if !ok {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	domain, err := idna.Lookup.ToASCII(strings.TrimRight(domain, "."))
	if err != nil || !isValidRegistrationEmailDomain(domain) || len(domain) > 253 {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	domain = strings.ToLower(domain)
	if publicRegistrationEmailDomains[domain] {
		return "", nil
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return domain, nil
	}
	return root, nil
}
