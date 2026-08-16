package libbox

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestResolveConfigExpandsProxyProviderGroups(t *testing.T) {
	workingPath := t.TempDir()
	sWorkingPath = workingPath
	sTempPath = workingPath
	sUserID = os.Getuid()
	sGroupID = os.Getgid()
	require.NoError(t, os.WriteFile(filepath.Join(workingPath, "sub.yaml"), []byte(`
proxies:
  - name: HK SS
    type: ss
    server: hk.example.com
    port: 8388
    cipher: aes-128-gcm
    password: password
`), 0o644))

	resolved, err := ResolveConfig(`{
  "proxy-providers": {
    "sub": { "type": "file", "path": "sub.yaml" }
  },
  "outbounds": [
    { "type": "direct", "tag": "DIRECT" },
    { "type": "selector", "tag": "Proxy", "use": ["sub"] }
  ]
}`)
	require.NoError(t, err)

	options, err := parseConfig(baseContext(nil), resolved.Value)
	require.NoError(t, err)
	require.Empty(t, options.ProxyProviders)
	require.Len(t, options.Outbounds, 3)
	group := options.Outbounds[1].Options.(*option.SelectorOutboundOptions)
	require.Equal(t, []string{"HK SS"}, group.Outbounds)
	require.Equal(t, "shadowsocks", options.Outbounds[2].Type)
	require.Equal(t, "HK SS", options.Outbounds[2].Tag)
}

func TestCheckConfigDoesNotFetchHTTPProxyProvider(t *testing.T) {
	workingPath := t.TempDir()
	sWorkingPath = workingPath
	sTempPath = workingPath
	sUserID = os.Getuid()
	sGroupID = os.Getgid()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	config := "{\"proxy-providers\":{\"sub\":{\"type\":\"http\",\"url\":" + strconv.Quote(server.URL) + ",\"path\":\"missing.yaml\"}},\"outbounds\":[{\"type\":\"selector\",\"tag\":\"Proxy\",\"use\":[\"sub\"]}]}"
	require.NoError(t, CheckConfig(config))
	require.Zero(t, requestCount.Load())
}

func TestUpdateProxyProvidersRefreshesCacheForOfflineResolve(t *testing.T) {
	workingPath := t.TempDir()
	sWorkingPath = workingPath
	sTempPath = workingPath
	sUserID = os.Getuid()
	sGroupID = os.Getgid()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		_, _ = response.Write([]byte(`
proxies:
  - name: Manual SS
    type: ss
    server: manual.example.com
    port: 8388
    cipher: aes-128-gcm
    password: password
`))
	}))
	defer server.Close()

	config := `{
  "proxy-providers": {
    "sub": { "type": "http", "url": ` + strconv.Quote(server.URL) + ` }
  },
  "outbounds": [
    { "type": "selector", "tag": "Proxy", "use": ["sub"] }
  ]
}`
	updated, err := UpdateProxyProviders(config)
	require.NoError(t, err)
	require.Equal(t, int32(1), updated)
	updated, err = UpdateMissingProxyProviders(config)
	require.NoError(t, err)
	require.Zero(t, updated)
	require.Equal(t, int32(1), requestCount.Load())

	resolved, err := ResolveConfig(config)
	require.NoError(t, err)
	options, err := parseConfig(baseContext(nil), resolved.Value)
	require.NoError(t, err)
	require.Len(t, options.Outbounds, 2)
	group := options.Outbounds[0].Options.(*option.SelectorOutboundOptions)
	require.Equal(t, []string{"Manual SS"}, group.Outbounds)
	require.Equal(t, "Manual SS", options.Outbounds[1].Tag)
}
