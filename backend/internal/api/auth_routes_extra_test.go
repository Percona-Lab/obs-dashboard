package api

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestRebuildReachesHandlerWithSessionAndOrigin(t *testing.T) {
	h, _, sid := authRouter(t, true)
	rebuild := `{"project":"ppg:staging:18","repo":"r","arch":"x86_64","package":"p"}`
	if code := call(h, http.MethodPost, "/api/rebuild", rebuild, dashOrigin, sid); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("valid session+origin should reach handler, got %d", code)
	}
	if code := call(h, http.MethodPost, "/api/rebuild", rebuild, "http://evil.test", sid); code != http.StatusForbidden {
		t.Errorf("foreign origin: got %d want 403", code)
	}
}

func TestLoginRouteAbsentWhenAuthDisabled(t *testing.T) {
	h, _, _ := authRouter(t, false)
	if code := call(h, http.MethodGet, "/auth/login", "", "", ""); code != http.StatusNotFound {
		t.Errorf("got %d want 404", code)
	}
}

func TestCallbackQueryNotLogged(t *testing.T) {
	var buf bytes.Buffer
	old := middleware.DefaultLogger
	middleware.DefaultLogger = middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: log.New(&buf, "", 0), NoColor: true})
	defer func() { middleware.DefaultLogger = old }()
	h, _, _ := authRouter(t, true)
	call(h, http.MethodGet, "/auth/callback?code=secret&state=s3cret", "", "", "")
	if buf.Len() == 0 {
		t.Fatal("expected a log line")
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("log leaked callback query: %s", buf.String())
	}
}
