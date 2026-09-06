// Package sync implements HopDrop's HTTPS transfer protocol.
package sync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xurenhe/hopdrop/core/device"
	"github.com/xurenhe/hopdrop/core/filestore"
	"github.com/xurenhe/hopdrop/core/protocol"
)

const (
	transfersPath        = "/api/v1/transfers"
	maxControlBody       = 16 << 20
	offerDecisionTimeout = 5 * time.Minute
	requestTimeout       = 30 * time.Second
	sessionTTL           = 5 * time.Minute
	maxActiveSessions    = 1024
	maxConcurrentOffers  = 1
)

// Progress describes one side of an active transfer.
type Progress struct {
	Direction   string
	PeerID      string
	PeerName    string
	Phase       string
	CurrentName string
	Files       int
	TotalFiles  int
	Bytes       int64
	TotalBytes  int64
	Err         string
}

type ProgressFunc func(Progress)

// DecisionFunc may wait for a UI response. It must return when ctx is canceled.
type DecisionFunc func(ctx context.Context, peer protocol.DeviceInfo, offer protocol.Offer) protocol.Decision

// Engine owns the HTTP handler, TLS identity, inbound sessions and the sender.
type Engine struct {
	self   protocol.DeviceInfo
	sink   filestore.Sink
	decide DecisionFunc
	cert   tls.Certificate

	mu         sync.Mutex
	sessions   map[string]*inboundSession
	pending    map[string]bool
	offerSlots chan struct{}
	server     *http.Server

	// Mobile sinks are not necessarily thread-safe. Serializing writes also
	// makes filename collision handling deterministic across inbound sessions.
	sinkMu sync.Mutex
}

type inboundSession struct {
	mu sync.Mutex

	id        string
	sender    protocol.DeviceInfo
	offer     protocol.Offer
	accepted  []protocol.FileMeta
	byID      map[string]protocol.FileMeta
	received  map[string]protocol.FileReceipt
	uploading map[string]bool
	status    protocol.TransferStatus
	result    protocol.TransferResult
	updated   time.Time
}

func NewEngine(self protocol.DeviceInfo, sink filestore.Sink, decide DecisionFunc, cert tls.Certificate) *Engine {
	return &Engine{
		self: self, sink: sink, decide: decide, cert: cert,
		sessions:   make(map[string]*inboundSession),
		pending:    make(map[string]bool),
		offerSlots: make(chan struct{}, maxConcurrentOffers),
	}
}

// Serve exposes the versioned API over TLS and shuts active requests down with ctx.
func (e *Engine) Serve(ctx context.Context, ln net.Listener, onProgress ProgressFunc) error {
	if len(e.cert.Certificate) == 0 {
		return errors.New("sync: TLS identity is not configured")
	}
	mux := http.NewServeMux()
	mux.HandleFunc(transfersPath, e.handleCreateTransfer(onProgress))
	mux.HandleFunc(transfersPath+"/", e.handleTransfer(onProgress))

	srv := &http.Server{
		Handler:           mux,
		ErrorLog:          log.New(io.Discard, "", 0),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	e.mu.Lock()
	e.server = srv
	e.mu.Unlock()

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{e.cert},
		// Certificates are self-signed. The handler verifies that the public
		// key owns the fingerprint declared in the offer/session.
		ClientAuth: tls.RequireAnyClientCert,
	}
	tlsListener := tls.NewListener(ln, tlsConfig)

	serveContext, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	cleanupDone := make(chan struct{})
	go e.cleanupLoop(serveContext, cleanupDone)
	go func() {
		<-serveContext.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.Serve(tlsListener)
	cancelServe()
	<-cleanupDone
	e.mu.Lock()
	e.server = nil
	e.mu.Unlock()
	if errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("sync: serve HTTPS: %w", err)
}

// Send performs create -> file uploads -> complete. Success is reported only
// after the receiver returns a final receipt containing every accepted file.
func (e *Engine) Send(
	ctx context.Context,
	endpoint string,
	expectedPeer protocol.DeviceInfo,
	src filestore.Source,
	onProgress ProgressFunc,
) (retErr error) {
	if err := protocol.ValidateFingerprint(expectedPeer.Fingerprint); err != nil {
		return err
	}
	peer := expectedPeer
	defer func() {
		if retErr != nil {
			report(onProgress, Progress{
				Direction: "send", Phase: "error", PeerID: peer.ID,
				PeerName: peer.Name, Err: retErr.Error(),
			})
		}
	}()

	baseURL, err := endpointURL(endpoint)
	if err != nil {
		return err
	}
	files, err := src.List()
	if err != nil {
		return fmt.Errorf("sync: list source: %w", err)
	}
	var total int64
	for _, f := range files {
		if f.Size < 0 || total > protocol.MaxTransferSize-f.Size {
			return fmt.Errorf("sync: invalid source size for %q", f.Name)
		}
		total += f.Size
	}
	offer := protocol.Offer{Files: files, TotalBytes: total}
	if _, err := protocol.ValidateOffer(offer); err != nil {
		return err
	}

	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	client, observedFingerprint := e.httpClient(expectedPeer.Fingerprint)
	defer client.CloseIdleConnections()

	report(onProgress, Progress{
		Direction: "send", Phase: "handshake", PeerID: peer.ID, PeerName: peer.Name,
		TotalFiles: len(files), TotalBytes: total,
	})
	report(onProgress, Progress{
		Direction: "send", Phase: "offer", PeerID: peer.ID, PeerName: peer.Name,
		TotalFiles: len(files), TotalBytes: total,
	})

	createReq := protocol.CreateTransferRequest{
		Version: protocol.ProtocolVersion, SessionID: sessionID,
		Sender: e.self, Offer: offer,
	}
	var createResp protocol.CreateTransferResponse
	offerCtx, cancelOffer := context.WithTimeout(ctx, offerDecisionTimeout)
	err = doJSON(offerCtx, client, http.MethodPost, baseURL+transfersPath, createReq, &createResp)
	cancelOffer()
	if err != nil {
		return fmt.Errorf("sync: create transfer: %w", err)
	}
	if createResp.Version != protocol.ProtocolVersion || createResp.SessionID != sessionID {
		return errors.New("sync: invalid create-transfer response")
	}
	peer = createResp.Receiver
	observed := observedFingerprint()
	if peer.Fingerprint == "" || observed == "" || !sameFingerprint(peer.Fingerprint, observed) {
		return errors.New("sync: receiver identity does not match its TLS certificate")
	}
	if expectedPeer.ID != "" && peer.ID != expectedPeer.ID {
		return fmt.Errorf("sync: connected device is %q, expected %q", peer.ID, expectedPeer.ID)
	}
	if createResp.Status == protocol.StatusRejected {
		report(onProgress, Progress{
			Direction: "send", Phase: "rejected", PeerID: peer.ID,
			PeerName: peer.Name, Err: createResp.Reason,
		})
		return nil
	}
	if createResp.Status != protocol.StatusAccepted {
		return fmt.Errorf("sync: unexpected transfer status %q", createResp.Status)
	}
	accepted, err := explicitAcceptedFiles(offer, createResp.AcceptedIDs)
	if err != nil {
		return err
	}

	completed := false
	defer func() {
		if !completed {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = doJSON(cancelCtx, client, http.MethodDelete,
				baseURL+transfersPath+"/"+url.PathEscape(sessionID), nil, nil)
		}
	}()

	var sentBytes int64
	receipts := make(map[string]protocol.FileReceipt, len(accepted))
	for sentFiles, meta := range accepted {
		rc, err := src.Open(meta.ID)
		if err != nil {
			return fmt.Errorf("sync: open %q: %w", meta.RelPath, err)
		}
		body := newUploadBody(rc, meta.Size, func(current int64) {
			report(onProgress, Progress{
				Direction: "send", Phase: "transfer", PeerID: peer.ID, PeerName: peer.Name,
				CurrentName: meta.Name, Files: sentFiles, TotalFiles: len(accepted),
				Bytes: sentBytes + current, TotalBytes: acceptedSize(accepted),
			})
		})
		putURL := baseURL + transfersPath + "/" + url.PathEscape(sessionID) +
			"/files/" + url.PathEscape(meta.ID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, body)
		if err != nil {
			body.Close()
			return err
		}
		req.ContentLength = meta.Size
		req.Header.Set("Content-Type", contentType(meta.MimeType))
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("sync: upload %q: %w", meta.RelPath, err)
		}
		var receipt protocol.FileReceipt
		err = decodeResponse(resp, &receipt)
		if err != nil {
			return fmt.Errorf("sync: upload %q: %w", meta.RelPath, err)
		}
		if body.Count() != meta.Size {
			return fmt.Errorf("sync: source %q changed size: sent %d of %d", meta.RelPath, body.Count(), meta.Size)
		}
		localHash := body.Sum()
		if receipt.ID != meta.ID || receipt.Size != meta.Size || receipt.SHA256 != localHash {
			return fmt.Errorf("sync: receiver checksum mismatch for %q", meta.RelPath)
		}
		receipts[meta.ID] = receipt
		sentBytes += meta.Size
		report(onProgress, Progress{
			Direction: "send", Phase: "transfer", PeerID: peer.ID, PeerName: peer.Name,
			CurrentName: meta.Name, Files: sentFiles + 1, TotalFiles: len(accepted),
			Bytes: sentBytes, TotalBytes: acceptedSize(accepted),
		})
	}

	var result protocol.TransferResult
	completeCtx, cancelComplete := context.WithTimeout(ctx, requestTimeout)
	err = doJSON(completeCtx, client, http.MethodPost,
		baseURL+transfersPath+"/"+url.PathEscape(sessionID)+"/complete", struct{}{}, &result)
	cancelComplete()
	if err != nil {
		return fmt.Errorf("sync: finalize transfer: %w", err)
	}
	if err := validateResult(result, sessionID, accepted, receipts); err != nil {
		return err
	}
	completed = true
	report(onProgress, Progress{
		Direction: "send", Phase: "done", PeerID: peer.ID, PeerName: peer.Name,
		Files: len(accepted), TotalFiles: len(accepted), Bytes: sentBytes, TotalBytes: sentBytes,
	})
	return nil
}

func (e *Engine) handleCreateTransfer(onProgress ProgressFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req protocol.CreateTransferRequest
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Version != protocol.ProtocolVersion {
			writeError(w, http.StatusPreconditionFailed, fmt.Sprintf("protocol version %d required", protocol.ProtocolVersion))
			return
		}
		if !validSessionID(req.SessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id")
			return
		}
		if err := verifyRequestIdentity(r, req.Sender); err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if _, err := protocol.ValidateOffer(req.Offer); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		e.mu.Lock()
		_, duplicate := e.sessions[req.SessionID]
		duplicate = duplicate || e.pending[req.SessionID]
		tooMany := len(e.sessions) >= maxActiveSessions
		busy := false
		for _, session := range e.sessions {
			if session.isActive() {
				busy = true
				break
			}
		}
		if !duplicate && !tooMany && !busy {
			e.pending[req.SessionID] = true
		}
		e.mu.Unlock()
		if duplicate {
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		if tooMany {
			writeError(w, http.StatusServiceUnavailable, "too many active transfers")
			return
		}
		if busy {
			writeError(w, http.StatusConflict, "receiver is busy")
			return
		}
		defer func() {
			e.mu.Lock()
			delete(e.pending, req.SessionID)
			e.mu.Unlock()
		}()
		select {
		case e.offerSlots <- struct{}{}:
			defer func() { <-e.offerSlots }()
		default:
			writeError(w, http.StatusTooManyRequests, "too many pending offers")
			return
		}

		report(onProgress, Progress{
			Direction: "recv", Phase: "offer", PeerID: req.Sender.ID, PeerName: req.Sender.Name,
			TotalFiles: len(req.Offer.Files), TotalBytes: req.Offer.TotalBytes,
		})
		decision := protocol.Decision{Accept: e.sink != nil}
		if e.sink == nil {
			decision.Reason = "receiver is not configured to accept files"
		} else if e.decide != nil {
			decisionCtx, cancel := context.WithTimeout(r.Context(), offerDecisionTimeout)
			decision = e.decide(decisionCtx, req.Sender, req.Offer)
			cancel()
		}
		accepted, err := protocol.AcceptedFiles(req.Offer, decision)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		response := protocol.CreateTransferResponse{
			Version: protocol.ProtocolVersion, SessionID: req.SessionID,
			Receiver: e.self, Status: protocol.StatusRejected, Reason: decision.Reason,
			AcceptedIDs: make([]string, 0, len(accepted)),
		}
		if !decision.Accept {
			report(onProgress, Progress{
				Direction: "recv", Phase: "rejected", PeerID: req.Sender.ID, PeerName: req.Sender.Name,
			})
			writeJSON(w, http.StatusOK, response)
			return
		}
		response.Status = protocol.StatusAccepted
		for _, f := range accepted {
			response.AcceptedIDs = append(response.AcceptedIDs, f.ID)
		}
		session := newInboundSession(req, accepted)
		e.mu.Lock()
		if _, exists := e.sessions[req.SessionID]; exists {
			e.mu.Unlock()
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		e.sessions[req.SessionID] = session
		e.mu.Unlock()
		writeJSON(w, http.StatusOK, response)
	}
}

func (e *Engine) handleTransfer(onProgress ProgressFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, transfersPath+"/")
		parts := strings.Split(rel, "/")
		if len(parts) == 1 && r.Method == http.MethodDelete {
			e.handleCancel(w, r, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "complete" && r.Method == http.MethodPost {
			e.handleComplete(w, r, parts[0], onProgress)
			return
		}
		if len(parts) == 3 && parts[1] == "files" && r.Method == http.MethodPut {
			fileID, err := url.PathUnescape(parts[2])
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid file id")
				return
			}
			e.handleUpload(w, r, parts[0], fileID, onProgress)
			return
		}
		writeError(w, http.StatusNotFound, "unknown transfer endpoint")
	}
}

func (e *Engine) handleUpload(w http.ResponseWriter, r *http.Request, sessionID, fileID string, onProgress ProgressFunc) {
	session, ok := e.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	meta, prior, err := session.beginUpload(fileID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if prior != nil {
		writeJSON(w, http.StatusOK, prior)
		return
	}
	success := false
	defer func() {
		if !success {
			session.endUpload(fileID, nil, false)
		}
	}()
	if r.ContentLength != meta.Size {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("content length is %d, expected %d", r.ContentLength, meta.Size))
		return
	}

	completedFiles, completedBytes, totalFiles, totalBytes := session.progressBase()
	hasher := sha256.New()
	reader := &countingReader{
		r: io.TeeReader(http.MaxBytesReader(w, r.Body, meta.Size), hasher),
		onRead: func(n int64) {
			session.touch()
			report(onProgress, Progress{
				Direction: "recv", Phase: "transfer", PeerID: session.sender.ID,
				PeerName: session.sender.Name, CurrentName: meta.Name,
				Files: completedFiles, TotalFiles: totalFiles,
				Bytes: completedBytes + n, TotalBytes: totalBytes,
			})
		},
	}
	e.sinkMu.Lock()
	err = e.sink.Create(meta, reader)
	e.sinkMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("store file: %v", err))
		return
	}
	if reader.n != meta.Size {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("received %d bytes, expected %d", reader.n, meta.Size))
		return
	}
	receipt := &protocol.FileReceipt{
		ID: meta.ID, Size: reader.n, SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}
	session.endUpload(fileID, receipt, true)
	success = true
	writeJSON(w, http.StatusOK, receipt)
}

func (e *Engine) handleComplete(w http.ResponseWriter, r *http.Request, sessionID string, onProgress ProgressFunc) {
	session, ok := e.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	result, newlyCompleted, err := session.complete()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if newlyCompleted {
		report(onProgress, Progress{
			Direction: "recv", Phase: "done", PeerID: session.sender.ID, PeerName: session.sender.Name,
			Files: len(result.Files), TotalFiles: len(result.Files),
			Bytes: acceptedSize(session.accepted), TotalBytes: acceptedSize(session.accepted),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (e *Engine) handleCancel(w http.ResponseWriter, r *http.Request, sessionID string) {
	session, ok := e.authorizedSession(w, r, sessionID)
	if !ok {
		return
	}
	session.mu.Lock()
	session.status = protocol.StatusCanceled
	session.updated = time.Now()
	session.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (e *Engine) authorizedSession(w http.ResponseWriter, r *http.Request, escapedID string) (*inboundSession, bool) {
	sessionID, err := url.PathUnescape(escapedID)
	if err != nil || !validSessionID(sessionID) {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return nil, false
	}
	e.mu.Lock()
	session := e.sessions[sessionID]
	e.mu.Unlock()
	if session == nil {
		writeError(w, http.StatusNotFound, "transfer not found")
		return nil, false
	}
	got := requestFingerprint(r)
	if got == "" || !sameFingerprint(got, session.sender.Fingerprint) {
		writeError(w, http.StatusUnauthorized, "request identity does not own this transfer")
		return nil, false
	}
	return session, true
}

func newInboundSession(req protocol.CreateTransferRequest, accepted []protocol.FileMeta) *inboundSession {
	byID := make(map[string]protocol.FileMeta, len(accepted))
	for _, f := range accepted {
		byID[f.ID] = f
	}
	return &inboundSession{
		id: req.SessionID, sender: req.Sender, offer: req.Offer,
		accepted: append([]protocol.FileMeta(nil), accepted...), byID: byID,
		received: make(map[string]protocol.FileReceipt), uploading: make(map[string]bool),
		status: protocol.StatusAccepted, updated: time.Now(),
	}
}

func (s *inboundSession) beginUpload(id string) (protocol.FileMeta, *protocol.FileReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != protocol.StatusAccepted {
		return protocol.FileMeta{}, nil, fmt.Errorf("transfer is %s", s.status)
	}
	meta, ok := s.byID[id]
	if !ok {
		return protocol.FileMeta{}, nil, fmt.Errorf("file %q was not accepted", id)
	}
	if receipt, ok := s.received[id]; ok {
		copy := receipt
		return meta, &copy, nil
	}
	if s.uploading[id] {
		return protocol.FileMeta{}, nil, fmt.Errorf("file %q is already uploading", id)
	}
	s.uploading[id] = true
	s.updated = time.Now()
	return meta, nil, nil
}

func (s *inboundSession) isActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status == protocol.StatusAccepted
}

func (s *inboundSession) touch() {
	s.mu.Lock()
	s.updated = time.Now()
	s.mu.Unlock()
}

func (s *inboundSession) endUpload(id string, receipt *protocol.FileReceipt, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.uploading, id)
	if success && receipt != nil {
		s.received[id] = *receipt
	}
	s.updated = time.Now()
}

func (s *inboundSession) progressBase() (files int, bytes int64, totalFiles int, totalBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, receipt := range s.received {
		files++
		bytes += receipt.Size
	}
	return files, bytes, len(s.accepted), acceptedSize(s.accepted)
}

func (s *inboundSession) complete() (protocol.TransferResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == protocol.StatusComplete {
		return s.result, false, nil
	}
	if s.status != protocol.StatusAccepted {
		return protocol.TransferResult{}, false, fmt.Errorf("transfer is %s", s.status)
	}
	if len(s.uploading) != 0 {
		return protocol.TransferResult{}, false, errors.New("files are still uploading")
	}
	if len(s.received) != len(s.accepted) {
		return protocol.TransferResult{}, false,
			fmt.Errorf("received %d of %d accepted files", len(s.received), len(s.accepted))
	}
	result := protocol.TransferResult{
		Version: protocol.ProtocolVersion, SessionID: s.id,
		Status: protocol.StatusComplete, Files: make([]protocol.FileReceipt, 0, len(s.accepted)),
	}
	for _, f := range s.accepted {
		result.Files = append(result.Files, s.received[f.ID])
	}
	s.status = protocol.StatusComplete
	s.result = result
	s.updated = time.Now()
	return result, true, nil
}

func (e *Engine) cleanupLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.mu.Lock()
			for id, session := range e.sessions {
				session.mu.Lock()
				expired := now.Sub(session.updated) > sessionTTL
				session.mu.Unlock()
				if expired {
					delete(e.sessions, id)
				}
			}
			e.mu.Unlock()
		}
	}
}

func (e *Engine) httpClient(expectedFingerprint string) (*http.Client, func() string) {
	var observedMu sync.Mutex
	var observed string
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{e.cert},
		InsecureSkipVerify: true, // replaced by public-key pinning below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("sync: peer sent no TLS certificate")
			}
			got := device.CertificateFingerprint(cs.PeerCertificates[0])
			if expectedFingerprint != "" && !sameFingerprint(got, expectedFingerprint) {
				return errors.New("sync: peer TLS fingerprint mismatch")
			}
			observedMu.Lock()
			observed = got
			observedMu.Unlock()
			return nil
		},
	}
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		ResponseHeaderTimeout: offerDecisionTimeout + requestTimeout,
	}
	return &http.Client{Transport: transport}, func() string {
		observedMu.Lock()
		defer observedMu.Unlock()
		return observed
	}
}

func verifyRequestIdentity(r *http.Request, claimed protocol.DeviceInfo) error {
	if claimed.ID == "" || claimed.Name == "" || claimed.Fingerprint == "" {
		return errors.New("missing sender identity")
	}
	got := requestFingerprint(r)
	if got == "" || !sameFingerprint(got, claimed.Fingerprint) {
		return errors.New("sender identity does not match its TLS certificate")
	}
	return nil
}

func requestFingerprint(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return device.CertificateFingerprint(r.TLS.PeerCertificates[0])
}

func sameFingerprint(a, b string) bool {
	aBytes, aErr := hex.DecodeString(a)
	bBytes, bErr := hex.DecodeString(b)
	return aErr == nil && bErr == nil && len(aBytes) == sha256.Size && len(bBytes) == sha256.Size &&
		subtle.ConstantTimeCompare(aBytes, bBytes) == 1
}

func endpointURL(endpoint string) (string, error) {
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return "", fmt.Errorf("sync: invalid endpoint %q: %w", endpoint, err)
	}
	return "https://" + endpoint, nil
}

func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("sync: generate session id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validSessionID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 16 && id == strings.ToLower(id)
}

func explicitAcceptedFiles(offer protocol.Offer, ids []string) ([]protocol.FileMeta, error) {
	decision := protocol.Decision{Accept: true, AcceptIDs: ids}
	if ids == nil {
		decision.AcceptIDs = []string{}
	}
	return protocol.AcceptedFiles(offer, decision)
}

func acceptedSize(files []protocol.FileMeta) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

func contentType(mime string) string {
	if mime == "" {
		return "application/octet-stream"
	}
	return mime
}

func validateResult(
	result protocol.TransferResult,
	sessionID string,
	accepted []protocol.FileMeta,
	want map[string]protocol.FileReceipt,
) error {
	if result.Version != protocol.ProtocolVersion || result.SessionID != sessionID || result.Status != protocol.StatusComplete {
		return errors.New("sync: receiver returned an invalid completion result")
	}
	if len(result.Files) != len(accepted) {
		return fmt.Errorf("sync: receiver acknowledged %d of %d files", len(result.Files), len(accepted))
	}
	seen := make(map[string]struct{}, len(result.Files))
	for _, got := range result.Files {
		if _, duplicate := seen[got.ID]; duplicate {
			return fmt.Errorf("sync: duplicate final receipt for file %q", got.ID)
		}
		seen[got.ID] = struct{}{}
		expected, ok := want[got.ID]
		if !ok || got.Size != expected.Size || got.SHA256 != expected.SHA256 {
			return fmt.Errorf("sync: invalid final receipt for file %q", got.ID)
		}
	}
	return nil
}

type uploadBody struct {
	r       io.ReadCloser
	limited io.Reader
	hash    hash.Hash
	n       int64
	onRead  func(int64)
	last    time.Time
}

func newUploadBody(r io.ReadCloser, size int64, onRead func(int64)) *uploadBody {
	h := sha256.New()
	return &uploadBody{r: r, limited: io.TeeReader(io.LimitReader(r, size), h), hash: h, onRead: onRead}
}

func (b *uploadBody) Read(p []byte) (int, error) {
	n, err := b.limited.Read(p)
	if n > 0 {
		b.n += int64(n)
		now := time.Now()
		if b.onRead != nil && (now.Sub(b.last) >= 100*time.Millisecond || b.n%int64(256<<10) == 0) {
			b.last = now
			b.onRead(b.n)
		}
	}
	return n, err
}

func (b *uploadBody) Close() error { return b.r.Close() }
func (b *uploadBody) Count() int64 { return b.n }
func (b *uploadBody) Sum() string  { return hex.EncodeToString(b.hash.Sum(nil)) }

type countingReader struct {
	r      io.Reader
	n      int64
	onRead func(int64)
	last   time.Time
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.n += int64(n)
		now := time.Now()
		if r.onRead != nil && now.Sub(r.last) >= 100*time.Millisecond {
			r.last = now
			r.onRead(r.n)
		}
	}
	return n, err
}

func doJSON(ctx context.Context, client *http.Client, method, endpoint string, in, out any) error {
	var body io.Reader
	if in != nil {
		var data []byte
		var err error
		data, err = json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-HopDrop-Version", fmt.Sprint(protocol.ProtocolVersion))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er protocol.ErrorResponse
		if json.NewDecoder(io.LimitReader(resp.Body, maxControlBody)).Decode(&er) == nil && er.Error != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, er.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxControlBody)).Decode(out)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON: multiple values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.ErrorResponse{Error: message})
}

func report(fn ProgressFunc, p Progress) {
	if fn != nil {
		fn(p)
	}
}
