package extractor

import (
	"net/url"
	"strings"
)

// extractorHosts — non-exhaustive list of hostnames that yt-dlp handles
// natively but hget's HTTP engine cannot (manifest-based delivery,
// signed expiring URLs, post-processing required).  Used to auto-route
// without forcing the user to pass --extractor.
//
// We deliberately keep this short and obvious — anything more exotic
// requires explicit --extractor=yt-dlp so power users opt in knowingly.
var extractorHosts = []string{
	"youtube.com", "youtu.be", "music.youtube.com",
	"vimeo.com", "twitch.tv", "soundcloud.com",
	"dailymotion.com", "bilibili.com", "twitter.com",
	"x.com", "tiktok.com", "instagram.com", "facebook.com",
	"reddit.com", "v.redd.it",
}

// LooksExtractable returns true when rawURL points at a host that yt-dlp
// is known to handle and hget's plain HTTP engine is not.  Used to suggest
// (or, with --extractor=auto, automatically pick) the extractor pipeline.
func LooksExtractable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	for _, h := range extractorHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}
