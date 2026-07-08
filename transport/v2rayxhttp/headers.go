package v2rayxhttp

import "net/http"

const defaultChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"

func applyDefaultFetchHeaders(header http.Header) {
	if header == nil {
		return
	}
	if len(header.Values("User-Agent")) == 0 {
		header.Set("User-Agent", defaultChromeUserAgent)
		header.Set("Accept-Language", "en-US,en;q=0.9")
		header["Sec-CH-UA"] = []string{`"Not A Brand";v="99", "Chromium";v="144", "Google Chrome";v="144"`}
		header["Sec-CH-UA-Mobile"] = []string{"?0"}
		header["Sec-CH-UA-Platform"] = []string{`"Windows"`}
		header["DNT"] = []string{"1"}
	}
	header.Set("Sec-Fetch-Mode", "cors")
	header.Set("Sec-Fetch-Dest", "empty")
	header.Set("Sec-Fetch-Site", "same-origin")
	if header.Get("Priority") == "" {
		header.Set("Priority", "u=1, i")
	}
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
	if header.Get("Pragma") == "" {
		header.Set("Pragma", "no-cache")
	}
	if header.Get("Accept") == "" {
		header.Set("Accept", "*/*")
	}
}
