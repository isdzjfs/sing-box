package rule

import "regexp"

// Remote rule-sets are overwhelmingly hosted on GitHub, which is frequently unreachable on the
// networks this fork targets. When the URL a config points at cannot be fetched, these rules derive
// alternate URLs to try.
//
// The table is deliberately not exposed through the config schema. The rewrite is mechanical, so
// there is nothing for a user to tune; keeping it internal also means generated configs stay
// loadable by upstream sing-box, and a subscription cannot redirect rule-set downloads.
var (
	// https://github.com/<owner>/<repo>/raw/[refs/heads/]<ref>/<path>
	reGitHubRaw = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/raw/(?:refs/heads/)?([^/]+)/(.+)$`)
	// https://raw.githubusercontent.com/<owner>/<repo>/[refs/heads/]<ref>/<path>
	reGitHubUserContent = regexp.MustCompile(`^https://raw\.githubusercontent\.com/([^/]+)/([^/]+)/(?:refs/heads/)?([^/]+)/(.+)$`)
)

type ruleSetMirror struct {
	pattern *regexp.Regexp
	replace string
}

// Ordered by expected reachability. Both GitHub URL shapes map onto the same jsDelivr path, and
// several jsDelivr edges are listed because individual edges have been blocked in the past.
var ruleSetMirrors = []ruleSetMirror{
	{reGitHubRaw, "https://cdn.jsdelivr.net/gh/$1/$2@$3/$4"},
	{reGitHubUserContent, "https://cdn.jsdelivr.net/gh/$1/$2@$3/$4"},
	{reGitHubRaw, "https://testingcf.jsdelivr.net/gh/$1/$2@$3/$4"},
	{reGitHubUserContent, "https://testingcf.jsdelivr.net/gh/$1/$2@$3/$4"},
	{reGitHubRaw, "https://gcore.jsdelivr.net/gh/$1/$2@$3/$4"},
	{reGitHubUserContent, "https://gcore.jsdelivr.net/gh/$1/$2@$3/$4"},
}

// ruleSetMirrorURLs returns alternate URLs for sourceURL in the order they should be tried. A URL
// that matches no pattern yields nothing, which is how non-GitHub sources opt out without any
// special casing.
func ruleSetMirrorURLs(sourceURL string) []string {
	var urls []string
	seen := map[string]bool{sourceURL: true}
	for _, mirror := range ruleSetMirrors {
		if !mirror.pattern.MatchString(sourceURL) {
			continue
		}
		mirrored := mirror.pattern.ReplaceAllString(sourceURL, mirror.replace)
		if mirrored == "" || seen[mirrored] {
			continue
		}
		seen[mirrored] = true
		urls = append(urls, mirrored)
	}
	return urls
}
