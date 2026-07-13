package libbox

import (
	"os"
	"path/filepath"
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
