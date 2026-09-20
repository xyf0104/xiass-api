// Package proxyroute adapts the vendored ccodex-sleep-state outbound routes
// to XIASS loopback SOCKS listeners. Parsing and node identity remain upstream.
package proxyroute

import upstream "github.com/gylive/ccodex-sleep-state/bridge"

const MaxSubscriptionBytes = upstream.MaxSubscriptionBytes
const MaxNodes = upstream.MaxNodes

type NodeSummary struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

func Parse(data []byte) ([]map[string]any, error) {
	return upstream.Parse(data)
}

// Summaries validates every parsed node through the upstream Build path. It
// creates adapters but never opens a listener or dials a remote endpoint.
func Summaries(nodes []map[string]any) ([]NodeSummary, error) {
	result := make([]NodeSummary, 0, len(nodes))
	for index, node := range nodes {
		route, err := upstream.Build(node, index)
		if err != nil {
			return nil, err
		}
		result = append(result, NodeSummary{
			Index: index, ID: route.StableID, Name: route.DisplayName, Protocol: route.Protocol,
		})
		route.Close()
	}
	return result, nil
}
