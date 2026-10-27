// Package console hosts the static management UI and its session-authenticated API.
// The management handler passed to New is private, in-process, and never mounted directly.
package console

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Origin, StaticDir, PasswordHash string
	Issuer, ClientID, ClientSecret  string
	AllowedUsers                    []string
}

var passwordPattern = regexp.MustCompile(`^scrypt\$131072\$8\$1\$([a-f0-9]{32})\$([a-f0-9]{128})$`)

// Load uses the former Node console's auth environment names for migration.
// HTTP is permitted only for an explicit loopback development origin.
func Load(getenv func(string) string) (Config, error) {
	c := Config{Origin: strings.TrimSuffix(getenv("ORIGIN"), "/"), StaticDir: getenv("CONSOLE_STATIC_DIR")}
	if c.StaticDir == "" {
		c.StaticDir = "frontend/build"
	}
	c.StaticDir = filepath.Clean(c.StaticDir)
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return c, errors.New("console: ORIGIN must be an HTTPS origin (HTTP loopback is allowed for development)")
	}
	mode := strings.TrimSpace(getenv("AUTH_MODE"))
	switch mode {
	case "", "password":
		c.PasswordHash = strings.TrimSpace(getenv("AUTH_PASSWORD_HASH"))
		if !passwordPattern.MatchString(c.PasswordHash) {
			return c, errors.New("console: set a valid AUTH_PASSWORD_HASH using frontend/scripts/hash-password.mjs")
		}
	case "oidc":
		c.Issuer = strings.TrimSpace(getenv("OIDC_ISSUER"))
		issuer, errIssuer := url.Parse(c.Issuer)
		if errIssuer != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
			return c, errors.New("console: OIDC_ISSUER must be an HTTPS issuer URL")
		}
		c.ClientID, c.ClientSecret = strings.TrimSpace(getenv("OIDC_CLIENT_ID")), strings.TrimSpace(getenv("OIDC_CLIENT_SECRET"))
		for _, user := range strings.Split(getenv("OIDC_ALLOWED_USERS"), ",") {
			if user = strings.TrimSpace(user); user != "" {
				c.AllowedUsers = append(c.AllowedUsers, user)
			}
		}
		if c.ClientID == "" || c.ClientSecret == "" || len(c.AllowedUsers) == 0 {
			return c, errors.New("console: OIDC_CLIENT_ID, OIDC_CLIENT_SECRET and OIDC_ALLOWED_USERS are required")
		}
	default:
		return c, errors.New("console: AUTH_MODE must be password or oidc")
	}
	return c, nil
}

func isLoopback(host string) bool { return host == "localhost" || net.ParseIP(host).IsLoopback() }
