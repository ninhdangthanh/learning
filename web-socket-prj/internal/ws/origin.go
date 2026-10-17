package ws

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const anyOrigin = "*"

func newOriginChecker(allowedOrigins []string) func(*http.Request) bool {
	allowAll := slices.Contains(allowedOrigins, anyOrigin)
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" || allowAll {
			return true
		}
		if isSameOrigin(origin, r.Host) {
			return true
		}
		return slices.ContainsFunc(allowedOrigins, func(allowed string) bool {
			return strings.EqualFold(allowed, origin)
		})
	}
}

func isSameOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}
