package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const defaultTCPTransportTimeout = 2 * time.Second

type TCPTransport struct {
	mu       sync.RWMutex
	addr     string
	peers    map[string]string
	decoder  CommandDecoder
	timeout  time.Duration
	listener net.Listener
	engine   *Engine
}

func NewTCPTransport(addr string, peers map[string]string, decoder CommandDecoder) (*TCPTransport, error) {
	if addr == "" {
		return nil, errors.New("native raft tcp transport: address is required")
	}
	copied := make(map[string]string, len(peers))
	for id, peerAddr := range peers {
		copied[id] = peerAddr
	}
	return &TCPTransport{
		addr:    addr,
		peers:   copied,
		decoder: decoder,
		timeout: defaultTCPTransportTimeout,
	}, nil
}

func (t *TCPTransport) Start(engine *Engine) error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.listener = ln
	t.engine = engine
	t.addr = ln.Addr().String()
	t.mu.Unlock()

	go t.acceptLoop(ln)
	return nil
}

func (t *TCPTransport) Shutdown(id string) error {
	t.mu.Lock()
	ln := t.listener
	t.listener = nil
	t.engine = nil
	t.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (t *TCPTransport) RequestVote(target string, req requestVoteRequest) (requestVoteResponse, bool) {
	resp, ok := t.call(target, tcpRequest{Method: "request_vote", Vote: &req})
	if !ok || resp.Vote == nil {
		return requestVoteResponse{}, false
	}
	return *resp.Vote, true
}

func (t *TCPTransport) AppendEntries(target string, req appendEntriesRequest) (appendEntriesResponse, bool) {
	wireReq, err := encodeAppendEntriesRequest(req)
	if err != nil {
		return appendEntriesResponse{}, false
	}
	resp, ok := t.call(target, tcpRequest{Method: "append_entries", Append: wireReq})
	if !ok || resp.Append == nil {
		return appendEntriesResponse{}, false
	}
	return *resp.Append, true
}

func (t *TCPTransport) InstallSnapshot(target string, req installSnapshotRequest) (installSnapshotResponse, bool) {
	resp, ok := t.call(target, tcpRequest{Method: "install_snapshot", Snapshot: &req})
	if !ok || resp.Snapshot == nil {
		return installSnapshotResponse{}, false
	}
	return *resp.Snapshot, true
}

func (t *TCPTransport) Addr() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.addr
}

func (t *TCPTransport) SetTimeout(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = timeout
}

func (t *TCPTransport) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go t.handleConn(conn)
	}
}

func (t *TCPTransport) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(t.currentTimeout()))

	var req tcpRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(tcpResponse{Error: err.Error()})
		return
	}

	t.mu.RLock()
	engine := t.engine
	decoder := t.decoder
	t.mu.RUnlock()
	if engine == nil {
		_ = json.NewEncoder(conn).Encode(tcpResponse{Error: "native raft tcp transport: engine is not running"})
		return
	}

	switch req.Method {
	case "request_vote":
		if req.Vote == nil {
			_ = json.NewEncoder(conn).Encode(tcpResponse{Error: "missing vote request"})
			return
		}
		resp := engine.handleRequestVote(*req.Vote)
		_ = json.NewEncoder(conn).Encode(tcpResponse{Vote: &resp})
	case "append_entries":
		if req.Append == nil {
			_ = json.NewEncoder(conn).Encode(tcpResponse{Error: "missing append entries request"})
			return
		}
		decoded, err := decodeAppendEntriesRequest(*req.Append, decoder)
		if err != nil {
			_ = json.NewEncoder(conn).Encode(tcpResponse{Error: err.Error()})
			return
		}
		resp := engine.handleAppendEntries(decoded)
		_ = json.NewEncoder(conn).Encode(tcpResponse{Append: &resp})
	case "install_snapshot":
		if req.Snapshot == nil {
			_ = json.NewEncoder(conn).Encode(tcpResponse{Error: "missing install snapshot request"})
			return
		}
		resp := engine.handleInstallSnapshot(*req.Snapshot)
		_ = json.NewEncoder(conn).Encode(tcpResponse{Snapshot: &resp})
	default:
		_ = json.NewEncoder(conn).Encode(tcpResponse{Error: fmt.Sprintf("unsupported raft rpc %q", req.Method)})
	}
}

func (t *TCPTransport) call(target string, req tcpRequest) (tcpResponse, bool) {
	addr := t.peerAddress(target)
	if addr == "" {
		return tcpResponse{}, false
	}
	conn, err := net.DialTimeout("tcp", addr, t.currentTimeout())
	if err != nil {
		return tcpResponse{}, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(t.currentTimeout()))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return tcpResponse{}, false
	}
	var resp tcpResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return tcpResponse{}, false
	}
	if resp.Error != "" {
		return tcpResponse{}, false
	}
	return resp, true
}

func (t *TCPTransport) peerAddress(target string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if addr := t.peers[target]; addr != "" {
		return addr
	}
	return target
}

func (t *TCPTransport) currentTimeout() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.timeout <= 0 {
		return defaultTCPTransportTimeout
	}
	return t.timeout
}

type tcpRequest struct {
	Method   string                    `json:"method"`
	Vote     *requestVoteRequest       `json:"vote,omitempty"`
	Append   *wireAppendEntriesRequest `json:"append,omitempty"`
	Snapshot *installSnapshotRequest   `json:"snapshot,omitempty"`
}

type tcpResponse struct {
	Vote     *requestVoteResponse     `json:"vote,omitempty"`
	Append   *appendEntriesResponse   `json:"append,omitempty"`
	Snapshot *installSnapshotResponse `json:"snapshot,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

type wireAppendEntriesRequest struct {
	Term         uint64         `json:"term"`
	LeaderID     string         `json:"leader_id"`
	PrevLogIndex uint64         `json:"prev_log_index"`
	PrevLogTerm  uint64         `json:"prev_log_term"`
	Entries      []wireLogEntry `json:"entries,omitempty"`
	LeaderCommit uint64         `json:"leader_commit"`
}

type wireLogEntry struct {
	Index   uint64          `json:"index"`
	Term    uint64          `json:"term"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func encodeAppendEntriesRequest(req appendEntriesRequest) (*wireAppendEntriesRequest, error) {
	wire := &wireAppendEntriesRequest{
		Term:         req.Term,
		LeaderID:     req.LeaderID,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		LeaderCommit: req.LeaderCommit,
	}
	for _, entry := range req.Entries {
		if entry.Command == nil {
			return nil, errors.New("native raft tcp transport: log entry command is nil")
		}
		payload, err := entry.Command.Encode()
		if err != nil {
			return nil, err
		}
		wire.Entries = append(wire.Entries, wireLogEntry{
			Index:   entry.Index,
			Term:    entry.Term,
			Type:    entry.Command.Type(),
			Payload: append([]byte(nil), payload...),
		})
	}
	return wire, nil
}

func decodeAppendEntriesRequest(req wireAppendEntriesRequest, decoder CommandDecoder) (appendEntriesRequest, error) {
	decoded := appendEntriesRequest{
		Term:         req.Term,
		LeaderID:     req.LeaderID,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		LeaderCommit: req.LeaderCommit,
	}
	for _, entry := range req.Entries {
		if decoder == nil {
			return appendEntriesRequest{}, errors.New("native raft tcp transport: command decoder is required")
		}
		cmd, err := decoder(entry.Type, entry.Payload)
		if err != nil {
			return appendEntriesRequest{}, err
		}
		decoded.Entries = append(decoded.Entries, LogEntry{
			Index:   entry.Index,
			Term:    entry.Term,
			Command: cmd,
		})
	}
	return decoded, nil
}
