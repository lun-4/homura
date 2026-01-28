package main

import (
	"fmt"
	"log"
	"net"
	"sync"
)

// MmapManager handles mmap operations with the host
type MmapManager struct {
	client *P9Client
	conn   net.Conn

	mu      sync.RWMutex
	regions map[string]*MmapInfo // path -> mmap info
}

// MmapInfo tracks information about a memory-mapped file
type MmapInfo struct {
	MmapID uint64
	Path   string
	Size   uint64
}

// NewMmapManager creates a new mmap manager
func NewMmapManager(client *P9Client, conn net.Conn) *MmapManager {
	return &MmapManager{
		client:  client,
		conn:    conn,
		regions: make(map[string]*MmapInfo),
	}
}

// RegisterMmap registers a file for mmap with the host
func (mm *MmapManager) RegisterMmap(path string, size uint64) (*MmapInfo, error) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Check if already registered
	if info, ok := mm.regions[path]; ok {
		return info, nil
	}

	// Send MmapRegister request to host
	req := &MmapRegisterRequest{
		FilePath: path,
		Length:   size,
		Flags:    1, // MAP_SHARED
	}

	reqData := EncodeMmapRegisterRequest(req)
	if err := WriteMessage(mm.conn, MsgTypeMmapRegister, reqData); err != nil {
		return nil, fmt.Errorf("failed to send register request: %w", err)
	}

	// Read response
	msg, err := ReadMessage(mm.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read register response: %w", err)
	}

	if msg.Type == MsgTypeError {
		errResp, _ := DecodeErrorResponse(msg.Data)
		return nil, fmt.Errorf("host error: %s", errResp.Message)
	}

	if msg.Type != MsgTypeResponse {
		return nil, fmt.Errorf("unexpected response type: %d", msg.Type)
	}

	resp, err := DecodeMmapRegisterResponse(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Store mmap info
	info := &MmapInfo{
		MmapID: resp.MmapID,
		Path:   path,
		Size:   size,
	}
	mm.regions[path] = info

	log.Printf("Registered mmap for %s: ID=%d", path, info.MmapID)
	return info, nil
}

// BulkRead performs a bulk read from an mmap region
func (mm *MmapManager) BulkRead(mmapID uint64, offset, length uint64) ([]byte, error) {
	req := &MmapBulkReadRequest{
		MmapID: mmapID,
		Offset: offset,
		Length: length,
	}

	reqData := EncodeMmapBulkReadRequest(req)
	if err := WriteMessage(mm.conn, MsgTypeMmapBulkRead, reqData); err != nil {
		return nil, fmt.Errorf("failed to send bulk read request: %w", err)
	}

	// Read response
	msg, err := ReadMessage(mm.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read bulk read response: %w", err)
	}

	if msg.Type == MsgTypeError {
		errResp, _ := DecodeErrorResponse(msg.Data)
		return nil, fmt.Errorf("host error: %s", errResp.Message)
	}

	if msg.Type != MsgTypeResponse {
		return nil, fmt.Errorf("unexpected response type: %d", msg.Type)
	}

	resp, err := DecodeMmapBulkReadResponse(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp.Data, nil
}

// Write writes data to an mmap region
func (mm *MmapManager) Write(mmapID uint64, offset uint64, data []byte) (uint64, error) {
	req := &MmapWriteRequest{
		MmapID: mmapID,
		Offset: offset,
		Data:   data,
	}

	reqData := EncodeMmapWriteRequest(req)
	if err := WriteMessage(mm.conn, MsgTypeMmapWrite, reqData); err != nil {
		return 0, fmt.Errorf("failed to send write request: %w", err)
	}

	// Read response
	msg, err := ReadMessage(mm.conn)
	if err != nil {
		return 0, fmt.Errorf("failed to read write response: %w", err)
	}

	if msg.Type == MsgTypeError {
		errResp, _ := DecodeErrorResponse(msg.Data)
		return 0, fmt.Errorf("host error: %s", errResp.Message)
	}

	if msg.Type != MsgTypeResponse {
		return 0, fmt.Errorf("unexpected response type: %d", msg.Type)
	}

	resp, err := DecodeMmapWriteResponse(msg.Data)
	if err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp.BytesWritten, nil
}

// Unregister removes an mmap registration
func (mm *MmapManager) Unregister(path string) error {
	mm.mu.Lock()
	info, ok := mm.regions[path]
	if !ok {
		mm.mu.Unlock()
		return nil
	}
	delete(mm.regions, path)
	mm.mu.Unlock()

	// Send unregister request
	req := &MmapUnregisterRequest{
		MmapID: info.MmapID,
	}

	reqData := EncodeMmapUnregisterRequest(req)
	if err := WriteMessage(mm.conn, MsgTypeMmapUnregister, reqData); err != nil {
		return fmt.Errorf("failed to send unregister request: %w", err)
	}

	log.Printf("Unregistered mmap for %s: ID=%d", path, info.MmapID)
	return nil
}

// GetInfo retrieves mmap info for a path
func (mm *MmapManager) GetInfo(path string) (*MmapInfo, bool) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	info, ok := mm.regions[path]
	return info, ok
}

// Helper to encode unregister request (not in protocol.go yet)
func EncodeMmapUnregisterRequest(req *MmapUnregisterRequest) []byte {
	// Simple encoding: just the mmap ID
	buf := make([]byte, 8)
	// Using binary encoding (assuming little endian)
	buf[0] = byte(req.MmapID)
	buf[1] = byte(req.MmapID >> 8)
	buf[2] = byte(req.MmapID >> 16)
	buf[3] = byte(req.MmapID >> 24)
	buf[4] = byte(req.MmapID >> 32)
	buf[5] = byte(req.MmapID >> 40)
	buf[6] = byte(req.MmapID >> 48)
	buf[7] = byte(req.MmapID >> 56)
	return buf
}

// MmapUnregisterRequest represents a request to unregister mmap
type MmapUnregisterRequest struct {
	MmapID uint64
}
