package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/lan/meta-gateway/internal/plugins"
)

// Both public plugin entry points share the credential and header boundary.
// Rewrite runs after hop-by-hop stripping, so client-controlled Connection
// headers cannot erase the plugin credential we inject.
func newPluginReverseProxy(target *url.URL, transport http.RoundTripper, mode sidecarAuthMode, token, key, config string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			req := request.Out
			req.URL.Scheme, req.URL.Host, req.URL.Path = target.Scheme, target.Host, target.Path
			req.URL.RawPath = ""
			if key != "" {
				req.Header.Set("X-Plugin-Key", key)
			}
			if config != "" {
				req.Header.Set(plugins.XPluginConfigHeader, config)
			}
			if mode == sidecarAuthAdmin {
				req.Header.Del("Authorization")
			} else if mode == sidecarAuthPlugin {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			query := req.URL.Query()
			query.Del("t")
			req.URL.RawQuery = query.Encode()
			request.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error { setPluginSecurityHeaderValues(resp.Header); return nil },
	}
}
