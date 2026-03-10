package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// Guard enforces access control: IP whitelist + optional JWT verification.
type Guard struct {
	mu         sync.RWMutex
	whiteList  []string // CIDR ranges and/or exact IPs
	whiteNets  []*net.IPNet
	signingKey string // HS256 key; empty = no JWT required
	filerKey   string // separate key for filer access
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
	g.mu.RLock()
	allowed := g.isIPAllowedLocked(r.RemoteAddr)
	signingKey := g.signingKey
	g.mu.RUnlock()

	// Check IP whitelist
	if !allowed {
		return false
	}

	// Check JWT if required and key is configured
	if requiresJWT && signingKey != "" {
		token := r.Header.Get("Authorization")
		if token == "" {
			return false
		}

		// Strip "Bearer " prefix if present
		token = strings.TrimPrefix(token, "Bearer ")

		_, err := VerifyJWT(token, signingKey)
		return err == nil
	}

	return true
}

// isIPAllowed checks if the IP is in the whitelist.
func (g *Guard) isIPAllowed(remoteAddr string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isIPAllowedLocked(remoteAddr)
}

func (g *Guard) isIPAllowedLocked(remoteAddr string) bool {
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
	g.mu.RLock()
	allowed := g.isIPAllowedLocked(r.RemoteAddr)
	filerKey := g.filerKey
	g.mu.RUnlock()

	if !allowed {
		return false
	}

	if filerKey != "" {
		token := r.Header.Get("Authorization")
		if token == "" {
			return false
		}
		token = strings.TrimPrefix(token, "Bearer ")

		_, err := VerifyJWT(token, filerKey)
		return err == nil
	}

	return true
}

// SigningKey returns the JWT signing key.
func (g *Guard) SigningKey() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.signingKey
}

// FilerKey returns the filer JWT signing key.
func (g *Guard) FilerKey() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.filerKey
}

// IsWhiteListEmpty returns true if no whitelist is configured.
func (g *Guard) IsWhiteListEmpty() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.whiteNets) == 0
}

// WhiteListContains checks if an IP is in the whitelist.
func (g *Guard) WhiteListContains(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, ipNet := range g.whiteNets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// GetWhiteList returns the whitelist entries.
func (g *Guard) GetWhiteList() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, len(g.whiteList))
	copy(out, g.whiteList)
	return out
}

// SetWhiteList sets a new whitelist and recompiles it.
func (g *Guard) SetWhiteList(whiteList string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.whiteList = ParseWhiteList(whiteList)
	g.compileWhiteList()
}

// HasJWTSigningKey returns true if a JWT signing key is configured.
func (g *Guard) HasJWTSigningKey() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.signingKey != ""
}

// HasFilerKey returns true if a filer key is configured.
func (g *Guard) HasFilerKey() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.filerKey != ""
}

// SetSigningKey updates the JWT signing key.
func (g *Guard) SetSigningKey(signingKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signingKey = signingKey
}

// SetFilerKey updates the filer JWT key.
func (g *Guard) SetFilerKey(filerKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.filerKey = filerKey
}
