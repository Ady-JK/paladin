package inference

import (
	"errors"
	"net/http"
)

// Provider requests contain API keys. A redirect may keep those headers
// even when the destination is a different port or a subdomain.
var providerHTTPClient = &http.Client{CheckRedirect: func(next *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many provider redirects")
	}
	if next.URL.Scheme != via[0].URL.Scheme || next.URL.Host != via[0].URL.Host {
		return errors.New("provider redirect changed origin")
	}
	return nil
}}
