package security

import (
	"net"
	"net/http"
	"strings"
)

// Guard enforces access control: IP whitelist + optional JWT verification.
type Guard struct {
	whiteList  []string // CIDR ranges and/or exact IPs
	whiteNets  []*net.IPNet
	signingKey string   // HS256 key; empty = no JWT required
	filerKey   string   // separate key for filer access
}

// NewGuard creates a new Guard with the given whitelist and signing keys.
func NewGuard(whiteList, signingKey, filerKey string) *Guard {
	g := &Guard{
		whiteList:  ParseWhiteList(whiteList),
		signingKey: signingKey,
		filerKey:   filerKey,
	}
	g.compileWhiteList()
	return g
}

// compileWhiteList compiles whitelist entries to IPNet objects.
func (g *Guard) compileWhiteList() {
	g.whiteNets = make([]*net.IPNet, 0, len(g.whiteList))
	for _, entry := range g.whiteList {
		if strings.Contains(entry, "/") {
			// CIDR notation
			_, ipNet, err := net.ParseCIDR(entry)
			if err == nil {
				g.whiteNets = append(g.whiteNets, ipNet)
			}
		} else {
			// Exact IP - treat as /32 or /128
			ip := net.ParseIP(entry)
			if ip != nil {
				if ip4 := ip.To4(); ip4 != nil {
					_, ipNet, _ := net.ParseCIDR(entry + "/32")
					g.whiteNets = append(g.whiteNets, ipNet)
				} else {
					_, ipNet, _ := net.ParseCIDR(entry + "/128")
					g.whiteNets = append(g.whiteNets, ipNet)
				}
			}
		}
	}
}

// Allowed returns true if the request passes whitelist and JWT checks.
func (g *Guard) Allowed(r *http.Request, requiresJWT bool) bool {
	// Check IP whitelist
	if !g.isIPAllowed(r.RemoteAddr) {
		return false
	}

	// Check JWT if required and key is configured
	if requiresJWT && g.signingKey != "" {
		token := r.Header.Get("Authorization")
		if token == "" {
			return false
		}

		// Strip "Bearer " prefix if present
		token = strings.TrimPrefix(token, "Bearer ")

		_, err := VerifyJWT(token, g.signingKey)
		return err == nil
	}

	return true
}

// isIPAllowed checks if the IP is in the whitelist.
func (g *Guard) isIPAllowed(remoteAddr string) bool {
	// Extract IP from host:port
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// Might be just IP without port
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// If no whitelist configured, allow all
	if len(g.whiteNets) == 0 {
		return true
	}

	// Check against all whitelist entries
	for _, ipNet := range g.whiteNets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// ParseWhiteList parses "192.168.0.0/24,10.0.0.1" into a list of CIDRs/IPs.
func ParseWhiteList(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// FilerAllowed checks if a filer request is allowed.
func (g *Guard) FilerAllowed(r *http.Request) bool {
	if !g.isIPAllowed(r.RemoteAddr) {
		return false
	}

	if g.filerKey != "" {
		token := r.Header.Get("Authorization")
		if token == "" {
			return false
		}
		token = strings.TrimPrefix(token, "Bearer ")

		_, err := VerifyJWT(token, g.filerKey)
		return err == nil
	}

	return true
}

// SigningKey returns the JWT signing key.
func (g *Guard) SigningKey() string {
	return g.signingKey
}

// FilerKey returns the filer JWT signing key.
func (g *Guard) FilerKey() string {
	return g.filerKey
}

// IsWhiteListEmpty returns true if no whitelist is configured.
func (g *Guard) IsWhiteListEmpty() bool {
	return len(g.whiteNets) == 0
}

// WhiteListContains checks if an IP is in the whitelist.
func (g *Guard) WhiteListContains(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	for _, ipNet := range g.whiteNets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// GetWhiteList returns the whitelist entries.
func (g *Guard) GetWhiteList() []string {
	return g.whiteList
}

// SetWhiteList sets a new whitelist and recompiles it.
func (g *Guard) SetWhiteList(whiteList string) {
	g.whiteList = ParseWhiteList(whiteList)
	g.compileWhiteList()
}

// HasJWTSigningKey returns true if a JWT signing key is configured.
func (g *Guard) HasJWTSigningKey() bool {
	return g.signingKey != ""
}

// HasFilerKey returns true if a filer key is configured.
func (g *Guard) HasFilerKey() bool {
	return g.filerKey != ""
}
