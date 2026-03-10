package server

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/security"
	"GoBlob/goblob/topology"
)

// AssignRequest is the JSON request for /dir/assign.
type AssignRequest struct {
	Collection  string `json:"collection"`
	Replication string `json:"replication"`
	Ttl         string `json:"ttl"`
	Count       uint64 `json:"count"`
	DataCenter  string `json:"dataCenter"`
	Rack        string `json:"rack"`
	DiskType    string `json:"diskType"`
}

// AssignResponse is the JSON response for /dir/assign.
type AssignResponse struct {
	Fid       string `json:"fid"`
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
	Count     uint64 `json:"count"`
	Auth      string `json:"auth"`
	Error     string `json:"error,omitempty"`
}

// LookupRequest is the JSON request for /dir/lookup.
type LookupRequest struct {
	VolumeId    string `json:"volumeId"`
	Collection  string `json:"collection,omitempty"`
	Replication string `json:"replication,omitempty"`
}

// Location represents a volume server location.
type Location struct {
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
}

// LookupResponse is the JSON response for /dir/lookup.
type LookupResponse struct {
	VolumeId   string     `json:"volumeId,omitempty"`
	Locations  []Location `json:"locations"`
	Error      string     `json:"error,omitempty"`
}

// handleAssign handles POST /dir/assign requests.
func (ms *MasterServer) handleAssign(w http.ResponseWriter, r *http.Request) {
	// Leader check
	if !ms.isLeader.Load() {
		ms.proxyToLeader(w, r)
		return
	}

	// Parse query params
	q := r.URL.Query()

	// Parse count (default 1)
	countStr := q.Get("count")
	count := uint64(1)
	if countStr != "" {
		if c, err := strconv.ParseUint(countStr, 10, 64); err == nil {
			count = c
		}
	}

	collection := q.Get("collection")
	replication := q.Get("replication")
	if replication == "" {
		replication = ms.option.DefaultReplication
	}
	ttl := q.Get("ttl")
	_ = q.Get("dataCenter") // not used yet
	_ = q.Get("rack")        // not used yet
	diskType := q.Get("diskType")

	// Get or create volume layout
	volumeLayout := ms.Topo.GetOrCreateVolumeLayout(collection, replication, ttl, types.DiskType(diskType))

	// Pick for write (find writable volume)
	vid, locations, err := volumeLayout.PickForWrite(&topology.VolumeGrowOption{
		Collection: collection,
		Ttl:        ttl,
		DiskType:   types.DiskType(diskType),
	})
	if err != nil || len(locations) == 0 {
		// No writable volumes - send growth request
		select {
		case ms.volumeGrowthRequestChan <- &topology.VolumeGrowOption{
			Collection: collection,
			Ttl:        ttl,
			DiskType:   types.DiskType(diskType),
		}:
		default:
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(AssignResponse{
			Error: "no writable volumes available",
		})
		return
	}

	// Generate needle ID from sequencer
	needleId := ms.Sequencer.NextFileId(count)

	// Create file ID with random cookie
	fid := types.FileId{
		VolumeId: types.VolumeId(vid),
		NeedleId: types.NeedleId(needleId),
		Cookie:   types.Cookie(rand.Uint32()),
	}

	// Generate JWT
	auth := ""
	if ms.Guard.HasJWTSigningKey() {
		token, _ := security.SignJWT(ms.Guard.SigningKey(), ms.option.JwtExpireSeconds)
		auth = token
	}

	// Get URL from DataNode
	url := ""
	publicUrl := ""
	if len(locations) > 0 && locations[0].DataNode != nil {
		url = locations[0].DataNode.GetUrl()
		publicUrl = locations[0].DataNode.GetPublicUrl()
		if publicUrl == "" {
			publicUrl = url
		}
	}

	// Build response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AssignResponse{
		Fid:       fid.String(),
		Url:       url,
		PublicUrl: publicUrl,
		Count:     count,
		Auth:      auth,
	})
}

// handleLookup handles GET /dir/lookup requests.
func (ms *MasterServer) handleLookup(w http.ResponseWriter, r *http.Request) {
	// Parse volume ID
	vidStr := r.URL.Query().Get("volumeId")
	if vidStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(LookupResponse{Error: "missing volumeId parameter"})
		return
	}

	vid, err := strconv.ParseUint(vidStr, 10, 32)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(LookupResponse{Error: "invalid volumeId"})
		return
	}

	// Lookup volume locations
	locations := ms.Topo.LookupVolumeLocation(uint32(vid))

	// Convert to JSON response
	resp := LookupResponse{
		VolumeId:  vidStr,
		Locations: make([]Location, 0, len(locations)),
	}

	for _, loc := range locations {
		resp.Locations = append(resp.Locations, Location{
			Url:       loc.Url,
			PublicUrl: loc.PublicUrl,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStatus handles GET /dir/status requests.
func (ms *MasterServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"isLeader":          ms.isLeader.Load(),
		"topology":          ms.Topo.ToProto(),
		"dataCenterCount":   ms.Topo.GetTotalDataCenterCount(),
		"raft":              ms.Raft.Stats(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleVolGrow handles POST /vol/grow requests.
func (ms *MasterServer) handleVolGrow(w http.ResponseWriter, r *http.Request) {
	if !ms.isLeader.Load() {
		ms.proxyToLeader(w, r)
		return
	}

	// Parse request
	q := r.URL.Query()
	collection := q.Get("collection")
	replication := q.Get("replication")
	if replication == "" {
		replication = ms.option.DefaultReplication
	}
	ttl := q.Get("ttl")
	countStr := q.Get("count")
	_ = countStr // count is not used yet
	_ = uint64(7) // default growth count - not used yet

	// Send to volume growth channel
	option := &topology.VolumeGrowOption{
		Collection:       collection,
		ReplicaPlacement: types.ReplicaPlacement{}, // Parse from replication string if needed
		Ttl:              ttl,
		DiskType:         types.DefaultDiskType,
	}

	select {
	case ms.volumeGrowthRequestChan <- option:
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "volume growth channel full"})
	}
}

// handleVacuum handles POST /vol/vacuum requests.
func (ms *MasterServer) handleVacuum(w http.ResponseWriter, r *http.Request) {
	if !ms.isLeader.Load() {
		ms.proxyToLeader(w, r)
		return
	}

	// TODO: Implement vacuum (Phase 5)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleHealthz handles GET /cluster/healthz requests.
func (ms *MasterServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// proxyToLeader proxies the request to the Raft leader.
func (ms *MasterServer) proxyToLeader(w http.ResponseWriter, r *http.Request) {
	leader := ms.Raft.LeaderAddress()
	if leader == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "no leader available"})
		return
	}

	// Build leader URL
	leaderURL, err := url.Parse("http://" + leader)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid leader address"})
		return
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(leaderURL)

	// Update request to target leader
	r.URL.Host = leaderURL.Host
	r.URL.Scheme = leaderURL.Scheme

	// Proxy the request
	proxy.ServeHTTP(w, r)
}
