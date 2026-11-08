package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIOSInstallRoutesUseSameOriginAndProxyPackage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const ipaBody = "test-ipa-content"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			release := []iosRelease{{
				TagName: "ios-v1.0.11",
				Assets: []iosReleaseAsset{
					{Name: "XIASSAdmin-1.0.11.ipa", BrowserDownloadURL: server.URL + "/XIASSAdmin-1.0.11.ipa"},
					{Name: "XIASSAdmin-1.0.11.plist", BrowserDownloadURL: server.URL + "/XIASSAdmin-1.0.11.plist"},
				},
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(release)
		case "/XIASSAdmin-1.0.11.plist":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>items</key><array><dict><key>assets</key><array><dict><key>kind</key><string>software-package</string><key>url</key><string>https://github.com/xyf0104/xiass-api/releases/download/ios-v1.0.11/XIASSAdmin-1.0.11.ipa</string></dict></array><key>metadata</key><dict><key>bundle-identifier</key><string>com.xiass.admin</string><key>bundle-version</key><string>1.0.11</string></dict></dict></array></dict></plist>`))
		case "/XIASSAdmin-1.0.11.ipa":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprint(len(ipaBody)))
			_, _ = w.Write([]byte(ipaBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := newIOSInstallHandler(server.URL+"/releases", server.Client(), server.Client())
	router := gin.New()
	handler.register(router)

	infoResponse := httptest.NewRecorder()
	router.ServeHTTP(infoResponse, httptest.NewRequest(http.MethodGet, "/ios/info", nil))
	require.Equal(t, http.StatusOK, infoResponse.Code)
	require.Contains(t, infoResponse.Body.String(), "\"version\":\"1.0.11\"")
	require.Contains(t, infoResponse.Body.String(), iosPublicBaseURL+"/manifest")
	require.Contains(t, infoResponse.Body.String(), iosPublicBaseURL+"/package")

	manifestResponse := httptest.NewRecorder()
	router.ServeHTTP(manifestResponse, httptest.NewRequest(http.MethodGet, "/ios/manifest", nil))
	require.Equal(t, http.StatusOK, manifestResponse.Code)
	require.Equal(t, "application/xml; charset=utf-8", manifestResponse.Header().Get("Content-Type"))
	require.Contains(t, manifestResponse.Body.String(), "com.xiass.admin")
	require.Contains(t, manifestResponse.Body.String(), "1.0.11")
	require.Contains(t, manifestResponse.Body.String(), iosPublicBaseURL+"/package")
	require.NotContains(t, manifestResponse.Body.String(), "github.com/xyf0104/xiass-api/releases/download")

	packageResponse := httptest.NewRecorder()
	router.ServeHTTP(packageResponse, httptest.NewRequest(http.MethodGet, "/ios/package", nil))
	require.Equal(t, http.StatusOK, packageResponse.Code)
	require.Equal(t, "application/octet-stream", packageResponse.Header().Get("Content-Type"))
	require.Equal(t, fmt.Sprint(len(ipaBody)), packageResponse.Header().Get("Content-Length"))
	require.Contains(t, packageResponse.Header().Get("Content-Disposition"), "XIASSAdmin-1.0.11.ipa")
	require.Equal(t, ipaBody, packageResponse.Body.String())
}

func TestSelectIOSInstallAssetsMatchesCaseInsensitiveNames(t *testing.T) {
	release := iosRelease{
		TagName: "ios-v1.0.12",
		Assets: []iosReleaseAsset{
			{Name: "xiassadmin-1.0.12.IPA", BrowserDownloadURL: "https://github.com/xyf0104/xiass-api/releases/download/ios-v1.0.12/app.ipa"},
			{Name: "XIASSADMIN-1.0.12.PLIST", BrowserDownloadURL: "https://github.com/xyf0104/xiass-api/releases/download/ios-v1.0.12/app.plist"},
		},
	}

	selected := selectIOSInstallAssets(release)
	require.NotNil(t, selected)
	require.Equal(t, "1.0.12", selected.Version)
	require.Equal(t, "xiassadmin-1.0.12.IPA", selected.IPAFilename)
}

func TestSelectIOSInstallAssetsRejectsPathLikeAssetNames(t *testing.T) {
	release := iosRelease{
		TagName: "ios-v1.0.12",
		Assets: []iosReleaseAsset{
			{Name: "XIASSAdmin-1.0.12.ipa/extra", BrowserDownloadURL: "https://github.com/xyf0104/xiass-api/releases/download/ios-v1.0.12/app.ipa"},
			{Name: "XIASSAdmin-1.0.12.plist", BrowserDownloadURL: "https://github.com/xyf0104/xiass-api/releases/download/ios-v1.0.12/app.plist"},
		},
	}

	require.Nil(t, selectIOSInstallAssets(release))
}

func TestIOSInstallRoutesReturnBadGatewayWhenNoReleaseIsAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		statusCode int
		body       string
	}{{name: "upstream error", statusCode: http.StatusInternalServerError, body: "upstream unavailable"}, {name: "no installable release", statusCode: http.StatusOK, body: "[]"}} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.statusCode)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			router := gin.New()
			newIOSInstallHandler(server.URL, server.Client(), server.Client()).register(router)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ios/info", nil))
			require.Equal(t, http.StatusBadGateway, recorder.Code)
			require.True(t, strings.Contains(recorder.Body.String(), "iOS release unavailable"))
		})
	}
}
