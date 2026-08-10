package v2rayxhttp

import (
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestNormalizedPath(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		sessionIDPlacement string
		seqPlacement       string
		expected           string
	}{
		{
			name:     "default placement keeps trailing slash",
			path:     "/sh",
			expected: "/sh/",
		},
		{
			name:     "query string is stripped",
			path:     "/?world",
			expected: "/",
		},
		{
			name:               "both off path drops trailing slash",
			path:               "/stream",
			sessionIDPlacement: placementQuery,
			seqPlacement:       placementQuery,
			expected:           "/stream",
		},
		{
			name:               "both off path keeps file-like path",
			path:               "/stream/filename.extension",
			sessionIDPlacement: placementQuery,
			seqPlacement:       placementHeader,
			expected:           "/stream/filename.extension",
		},
		{
			name:               "seq in path keeps trailing slash",
			path:               "/stream",
			sessionIDPlacement: placementQuery,
			expected:           "/stream/",
		},
		{
			name:         "session in path keeps trailing slash",
			path:         "/stream",
			seqPlacement: placementCookie,
			expected:     "/stream/",
		},
		{
			name:               "existing trailing slash is preserved",
			path:               "/stream/",
			sessionIDPlacement: placementQuery,
			seqPlacement:       placementQuery,
			expected:           "/stream/",
		},
		{
			name:               "root is unchanged",
			path:               "/",
			sessionIDPlacement: placementQuery,
			seqPlacement:       placementQuery,
			expected:           "/",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := newConfig(option.V2RayXHTTPOptions{
				Path:               test.path,
				SessionIDPlacement: test.sessionIDPlacement,
				SeqPlacement:       test.seqPlacement,
			})
			if err != nil {
				t.Fatal(err)
			}
			if actual := config.normalizedPath(); actual != test.expected {
				t.Fatalf("normalized path = %q, want %q", actual, test.expected)
			}
		})
	}
}
