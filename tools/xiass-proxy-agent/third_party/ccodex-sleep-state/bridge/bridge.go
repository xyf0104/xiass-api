// Package bridge exposes the vendored upstream proxy loader without changing
// its parsing, downloading, filtering, stable identity, or adapter behavior.
package bridge

import (
	"context"

	"github.com/gylive/ccodex-sleep-state/internal/proxyroute"
	"github.com/gylive/ccodex-sleep-state/internal/settings"
)

type Config = settings.Config
type Source = settings.Source
type Route = proxyroute.Route

const MaxSubscriptionBytes = proxyroute.MaxSubscriptionBytes
const MaxNodes = proxyroute.MaxNodes

func Parse(data []byte) ([]map[string]any, error) { return proxyroute.Parse(data) }

func DefaultConfig() Config { return settings.Default() }

func Build(node map[string]any, index int) (Route, error) {
	return proxyroute.Build(node, index)
}

func BuildWithTLSOption(node map[string]any, index int, allowInsecureTLS bool) (Route, error) {
	return proxyroute.BuildWithTLSOption(node, index, allowInsecureTLS)
}

func Load(ctx context.Context, config Config) ([]Route, error) {
	return proxyroute.Load(ctx, config)
}
