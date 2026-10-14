package server

import "testing"

// TestUICacheControl pins the caching policy: hashed assets are immutable, and
// everything else (the SPA entry document) must always revalidate so a browser
// cannot keep running a stale bundle after an upgrade.
func TestUICacheControl(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"assets/index-D9FNz1uj.js", "public, max-age=31536000, immutable"},
		{"assets/index-Blv-4Shi.css", "public, max-age=31536000, immutable"},
		{"index.html", "no-cache"},
		{"", "no-cache"},
		{"favicon.ico", "no-cache"},
	}
	for _, tc := range cases {
		if got := uiCacheControl(tc.path); got != tc.want {
			t.Errorf("uiCacheControl(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
