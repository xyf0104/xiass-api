package proxyroute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	upstream "github.com/gylive/ccodex-sleep-state/bridge"
)

const MaxSubscriptionSources = 16

type Source struct {
	AllowInsecureTLS bool     `json:"allow_insecure_tls,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name,omitempty"`
	URL              string   `json:"url,omitempty"`
	Input            string   `json:"input,omitempty"`
	UserAgent        string   `json:"user_agent,omitempty"`
	IncludeProtocols []string `json:"include_protocols,omitempty"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
}

// Candidate retains the canonical upstream node solely for encrypted snapshot
// export and later reconstruction. It must never be serialized directly.
type Candidate struct {
	AllowInsecureTLS bool
	ID               string
	SourceID         string
	SourceName       string
	Name             string
	Protocol         string
	Node             map[string]any
	Outbound         *upstream.Route
}

type sourceAttribution struct {
	id               string
	name             string
	allowInsecureTLS bool
}

// LoadSources stages inline bodies as private regular files, then delegates
// all reads, downloads, parsing, filtering, deduplication, limits, identity,
// and adapter construction to the vendored upstream Load implementation.
func LoadSources(ctx context.Context, sources []Source) ([]Candidate, error) {
	if len(sources) == 0 {
		return []Candidate{}, nil
	}
	if len(sources) > MaxSubscriptionSources {
		return nil, errors.New("too many subscription sources")
	}

	var tempDir string
	defer func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}()
	var upstreamSources []upstream.Source
	var attributions []sourceAttribution
	for index, source := range sources {
		id, name, err := validateSource(source, index)
		if err != nil {
			return nil, err
		}
		base := upstream.Source{
			AllowInsecureTLS: source.AllowInsecureTLS,
			UserAgent:        source.UserAgent, IncludeProtocols: source.IncludeProtocols, ExcludeKeywords: source.ExcludeKeywords,
		}
		if strings.TrimSpace(source.Input) != "" {
			if tempDir == "" {
				tempDir, err = os.MkdirTemp("", "xiass-proxy-agent-subscription-")
				if err != nil {
					return nil, errors.New("cannot stage local subscription input")
				}
			}
			path := filepath.Join(tempDir, fmt.Sprintf("source-%02d", index+1))
			if err := os.WriteFile(path, []byte(source.Input), 0600); err != nil {
				return nil, errors.New("cannot stage local subscription input")
			}
			base.File = path
			upstreamSources = append(upstreamSources, base)
			attributions = append(attributions, sourceAttribution{id: id, name: name, allowInsecureTLS: source.AllowInsecureTLS})
			continue
		}
		urls := splitURLs(source.URL)
		if len(urls) == 0 {
			return nil, fmt.Errorf("subscription %d has neither url nor input", index+1)
		}
		for _, rawURL := range urls {
			entry := base
			entry.URL = rawURL
			upstreamSources = append(upstreamSources, entry)
			attributions = append(attributions, sourceAttribution{id: id, name: name, allowInsecureTLS: source.AllowInsecureTLS})
		}
		if len(upstreamSources) > MaxSubscriptionSources {
			return nil, errors.New("too many subscription sources after expanding URLs")
		}
	}

	config := upstream.DefaultConfig()
	config.Direct = false
	config.Subscriptions = upstreamSources
	if err := config.Validate(); err != nil {
		return nil, err
	}
	routes, err := upstream.Load(ctx, config)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(routes))
	for index := range routes {
		route := &routes[index]
		if route.SourceIndex < 0 || route.SourceIndex >= len(attributions) {
			closeUpstreamRoutes(routes)
			return nil, errors.New("upstream route source attribution is invalid")
		}
		attribution := attributions[route.SourceIndex]
		candidates = append(candidates, Candidate{
			AllowInsecureTLS: attribution.allowInsecureTLS,
			ID:               route.StableID, SourceID: attribution.id, SourceName: attribution.name,
			Name: route.DisplayName, Protocol: route.Protocol, Node: route.Node, Outbound: route,
		})
	}
	return candidates, nil
}

func BuildCandidate(node map[string]any, index int, sourceID, sourceName string) (Candidate, error) {
	return BuildCandidateWithTLSOption(node, index, sourceID, sourceName, false)
}

func BuildCandidateWithTLSOption(node map[string]any, index int, sourceID, sourceName string, allowInsecureTLS bool) (Candidate, error) {
	route, err := upstream.BuildWithTLSOption(node, index, allowInsecureTLS)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		AllowInsecureTLS: allowInsecureTLS,
		ID:               route.StableID, SourceID: sourceID, SourceName: sourceName,
		Name: route.DisplayName, Protocol: route.Protocol, Node: route.Node, Outbound: &route,
	}, nil
}

func CloseCandidates(candidates []Candidate) {
	for index := range candidates {
		if candidates[index].Outbound != nil {
			candidates[index].Outbound.Close()
			candidates[index].Outbound = nil
		}
	}
}

func closeUpstreamRoutes(routes []upstream.Route) {
	for index := range routes {
		routes[index].Close()
	}
}

func validateSource(source Source, index int) (string, string, error) {
	id := strings.TrimSpace(source.ID)
	if id == "" || len(id) > 96 || strings.ContainsAny(id, "\r\n\t /\\") {
		return "", "", fmt.Errorf("subscription %d has an invalid id", index+1)
	}
	if strings.TrimSpace(source.URL) != "" && strings.TrimSpace(source.Input) != "" {
		return "", "", fmt.Errorf("subscription %d cannot combine url and input", index+1)
	}
	if len(source.UserAgent) > 256 || strings.ContainsAny(source.UserAgent, "\r\n") {
		return "", "", fmt.Errorf("subscription %d has an invalid user agent", index+1)
	}
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, source.Name))
	if name == "" {
		name = fmt.Sprintf("订阅 %d", index+1)
	}
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	return id, name, nil
}

func ValidateAttribution(id, name string, index int) error {
	normalizedID, normalizedName, err := validateSource(Source{ID: id, Name: name}, index)
	if err != nil || normalizedID != id || normalizedName != name {
		return errors.New("snapshot contains invalid source attribution")
	}
	return nil
}

func splitURLs(input string) []string {
	var result []string
	for _, line := range strings.Split(input, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}
