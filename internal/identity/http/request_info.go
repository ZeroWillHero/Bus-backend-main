package http

import "net/http"

func deviceInfo(r *http.Request) string {
	return r.Header.Get("User-Agent")
}
