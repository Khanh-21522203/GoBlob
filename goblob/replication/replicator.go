package replication

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"GoBlob/goblob/core/types"
)

// ReplicateRequest contains the data needed to replicate a needle write.
type ReplicateRequest struct {
	VolumeId    types.VolumeId
	NeedleId    types.NeedleId
	NeedleBytes []byte // full serialized needle (header+body+footer)
	JWTToken    string
}

// Replicator defines the interface for replicating writes.
type Replicator interface {
	ReplicatedWrite(ctx context.Context, req ReplicateRequest, replicaAddrs []types.ServerAddress) error
}

// HTTPReplicator implements Replicator using HTTP PUT requests.
type HTTPReplicator struct {
	client *http.Client
}

func NewHTTPReplicator() *HTTPReplicator {
	return &HTTPReplicator{client: &http.Client{Timeout: 30 * time.Second}}
}

// ReplicatedWrite sends the needle to all replica addresses in parallel.
// Returns an error if ANY replica fails.
func (r *HTTPReplicator) ReplicatedWrite(ctx context.Context, req ReplicateRequest, replicaAddrs []types.ServerAddress) error {
	if len(replicaAddrs) == 0 {
		return nil
	}

	type result struct{ err error }
	results := make(chan result, len(replicaAddrs))

	for _, addr := range replicaAddrs {
		go func(addr types.ServerAddress) {
			results <- result{r.writeToReplica(ctx, addr, req)}
		}(addr)
	}

	var errs []error
	for range replicaAddrs {
		if res := <-results; res.err != nil {
			errs = append(errs, res.err)
		}
	}
	// combine errors
	if len(errs) > 0 {
		return fmt.Errorf("replication failed: %v", errs)
	}
	return nil
}

// writeToReplica sends needle bytes to a single replica server.
func (r *HTTPReplicator) writeToReplica(ctx context.Context, addr types.ServerAddress, req ReplicateRequest) error {
	// URL: http://addr/{vid},{needleIdHex}
	url := fmt.Sprintf("http://%s/%d,%016x", addr, req.VolumeId, req.NeedleId)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(req.NeedleBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("X-Replication", "true")
	if req.JWTToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.JWTToken)
	}
	httpReq.ContentLength = int64(len(req.NeedleBytes))

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("replica %s returned status %d", addr, resp.StatusCode)
	}
	return nil
}
