package upstreamverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type manifest struct {
	Repository string         `json:"repository"`
	Commit     string         `json:"commit"`
	Files      []manifestFile `json:"files"`
}

type manifestFile struct {
	Retained       string `json:"retained"`
	RetainedSHA256 string `json:"retained_sha256"`
	Build          string `json:"build"`
	BuildSHA256    string `json:"build_sha256"`
	Equivalence    string `json:"equivalence"`
}

func TestRetainedUpstreamSourceMatchesManifest(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "UPSTREAM_SOURCE_MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source manifest
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	if source.Repository != "luweiming1/ccodex-sleep-state" || source.Commit != "89294a1c723c7f061fa60afa42710e17a40ec6a6" {
		t.Fatalf("unexpected upstream identity: %s@%s", source.Repository, source.Commit)
	}
	for _, file := range source.Files {
		retained := readAndVerify(t, root, file.Retained, file.RetainedSHA256)
		build := readAndVerify(t, root, file.Build, file.BuildSHA256)
		if file.Equivalence == "exact" && !bytes.Equal(retained, build) {
			t.Fatalf("exact upstream build copy changed: %s", file.Build)
		}
	}
}

func readAndVerify(t *testing.T, root, path, expected string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if actual := hex.EncodeToString(digest[:]); actual != expected {
		t.Fatalf("%s sha256=%s want=%s", path, actual, expected)
	}
	return data
}
