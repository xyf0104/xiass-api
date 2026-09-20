package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"io"
	"strings"

	"github.com/xyf0104/xiass-proxy-agent/internal/proxyroute"
)

const snapshotPrefix = "xps1."
const snapshotVersion = 1

type snapshotDocument struct {
	Version int
	Nodes   []snapshotNode
}

type snapshotNode struct {
	AllowInsecureTLS bool
	ID               string
	SourceID         string
	SourceName       string
	Node             map[string]any
}

func init() {
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

// Snapshots are portable across agent restarts because the backend launches
// each process with a new control token. The checksum detects corruption; the
// snapshot remains sensitive and is only returned over the authenticated
// loopback control channel for encrypted backend storage.
func encodeSnapshot(candidates []proxyroute.Candidate) (string, error) {
	document := snapshotDocument{Version: snapshotVersion, Nodes: make([]snapshotNode, 0, len(candidates))}
	for _, candidate := range candidates {
		document.Nodes = append(document.Nodes, snapshotNode{
			AllowInsecureTLS: candidate.AllowInsecureTLS,
			ID:               candidate.ID, SourceID: candidate.SourceID, SourceName: candidate.SourceName, Node: candidate.Node,
		})
	}
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(document); err != nil {
		return "", errors.New("snapshot cannot encode canonical nodes")
	}
	digest := sha256.Sum256(payload.Bytes())
	data := make([]byte, 0, len(digest)+payload.Len())
	data = append(data, digest[:]...)
	data = append(data, payload.Bytes()...)
	return snapshotPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeAndBuildSnapshot(opaque string) ([]proxyroute.Candidate, error) {
	if !strings.HasPrefix(opaque, snapshotPrefix) {
		return nil, errors.New("invalid snapshot")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(opaque, snapshotPrefix))
	if err != nil || len(data) <= sha256.Size {
		return nil, errors.New("invalid snapshot")
	}
	want := data[:sha256.Size]
	payload := data[sha256.Size:]
	actual := sha256.Sum256(payload)
	if !bytes.Equal(want, actual[:]) {
		return nil, errors.New("invalid snapshot")
	}
	var document snapshotDocument
	decoder := gob.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&document); err != nil || document.Version != snapshotVersion || len(document.Nodes) > proxyroute.MaxNodes {
		return nil, errors.New("invalid snapshot")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid snapshot")
	}

	candidates := make([]proxyroute.Candidate, 0, len(document.Nodes))
	for index, node := range document.Nodes {
		if err := proxyroute.ValidateAttribution(node.SourceID, node.SourceName, index); err != nil {
			proxyroute.CloseCandidates(candidates)
			return nil, err
		}
		candidate, err := proxyroute.BuildCandidateWithTLSOption(node.Node, index, node.SourceID, node.SourceName, node.AllowInsecureTLS)
		if err != nil || candidate.ID != node.ID {
			proxyroute.CloseCandidates(candidates)
			return nil, errors.New("snapshot contains an invalid node")
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}
