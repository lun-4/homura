package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RequestStatus represents the status of a path request
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusDenied   RequestStatus = "denied"
)

// RequestResult represents the result of a path request
type RequestResult struct {
	Approved bool
	Reason   string
}

// PathRequest represents a path exposure request from a VM
type PathRequest struct {
	ID           string
	Path         string
	RequestedAt  time.Time
	Status       RequestStatus
	VMPID        int
	DenialReason string
	resultChan   chan RequestResult
}

// Wait blocks until the request is resolved (approved or denied)
func (pr *PathRequest) Wait(ctx context.Context) RequestResult {
	select {
	case result := <-pr.resultChan:
		return result
	case <-ctx.Done():
		return RequestResult{
			Approved: false,
			Reason:   "Request cancelled",
		}
	}
}

// RequestQueue manages pending path requests
type RequestQueue struct {
	mu       sync.RWMutex
	requests map[string]*PathRequest
	sequence int
}

// NewRequestQueue creates a new request queue
func NewRequestQueue() *RequestQueue {
	return &RequestQueue{
		requests: make(map[string]*PathRequest),
		sequence: 0,
	}
}

// generateID generates a unique request ID
func (rq *RequestQueue) generateID() string {
	rq.sequence++
	return fmt.Sprintf("req-%d-%03d", time.Now().Unix(), rq.sequence)
}

// Create creates a new path request and adds it to the queue
func (rq *RequestQueue) Create(path string, vmPID int) *PathRequest {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	req := &PathRequest{
		ID:          rq.generateID(),
		Path:        path,
		RequestedAt: time.Now(),
		Status:      StatusPending,
		VMPID:       vmPID,
		resultChan:  make(chan RequestResult, 1),
	}

	rq.requests[req.ID] = req
	return req
}

// Get retrieves a request by ID
func (rq *RequestQueue) Get(id string) (*PathRequest, bool) {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	req, ok := rq.requests[id]
	return req, ok
}

// List returns all pending requests
func (rq *RequestQueue) List() []*PathRequest {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	result := make([]*PathRequest, 0, len(rq.requests))
	for _, req := range rq.requests {
		if req.Status == StatusPending {
			result = append(result, req)
		}
	}

	return result
}

// Approve approves a request by ID
func (rq *RequestQueue) Approve(id string) error {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	req, ok := rq.requests[id]
	if !ok {
		return fmt.Errorf("request not found")
	}

	if req.Status != StatusPending {
		return fmt.Errorf("request already resolved with status: %s", req.Status)
	}

	req.Status = StatusApproved
	req.resultChan <- RequestResult{
		Approved: true,
	}

	return nil
}

// Deny denies a request by ID with an optional reason
func (rq *RequestQueue) Deny(id string, reason string) error {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	req, ok := rq.requests[id]
	if !ok {
		return fmt.Errorf("request not found")
	}

	if req.Status != StatusPending {
		return fmt.Errorf("request already resolved with status: %s", req.Status)
	}

	req.Status = StatusDenied
	req.DenialReason = reason
	req.resultChan <- RequestResult{
		Approved: false,
		Reason:   reason,
	}

	return nil
}

// CloseAll closes all pending requests (used during shutdown)
func (rq *RequestQueue) CloseAll() {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	for _, req := range rq.requests {
		if req.Status == StatusPending {
			req.Status = StatusDenied
			req.DenialReason = "Server shutdown"
			close(req.resultChan)
		}
	}
}
