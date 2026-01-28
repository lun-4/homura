package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types for extended 9p protocol (mmap support)
const (
	MsgTypeMmapRegister   = 200
	MsgTypeMmapBulkRead   = 201
	MsgTypeMmapWrite      = 202
	MsgTypeMmapNotify     = 203
	MsgTypeMmapUnregister = 204

	MsgTypeResponse = 250
	MsgTypeError    = 251
)

// Message represents a protocol message
type Message struct {
	Type uint8
	Data []byte
}

// MmapRegisterRequest - Client wants to mmap a file
type MmapRegisterRequest struct {
	FilePath string
	Length   uint64
	Flags    uint32
}

// MmapRegisterResponse - Server response with mmap ID
type MmapRegisterResponse struct {
	MmapID uint64
}

// MmapBulkReadRequest - Efficient bulk read for initial mmap population
type MmapBulkReadRequest struct {
	MmapID uint64
	Offset uint64
	Length uint64
}

// MmapBulkReadResponse - Data from bulk read
type MmapBulkReadResponse struct {
	Data []byte
}

// MmapWriteRequest - Guest wrote to mmapped region
type MmapWriteRequest struct {
	MmapID uint64
	Offset uint64
	Data   []byte
}

// MmapWriteResponse - Acknowledgment of write
type MmapWriteResponse struct {
	BytesWritten uint64
}

// MmapNotifyMessage - Host detected external change (server→client push)
type MmapNotifyMessage struct {
	MmapID uint64
	Offset uint64
	Length uint64
}

// ErrorResponse - Error from server
type ErrorResponse struct {
	Code    uint32
	Message string
}

// Protocol helper functions

// ReadMessage reads a message from a connection
func ReadMessage(r io.Reader) (*Message, error) {
	// Read message length (4 bytes)
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	if length > 16*1024*1024 { // 16MB max
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	// Read message type (1 byte)
	var msgType uint8
	if err := binary.Read(r, binary.LittleEndian, &msgType); err != nil {
		return nil, fmt.Errorf("failed to read message type: %w", err)
	}

	// Read message data
	data := make([]byte, length-1)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("failed to read message data: %w", err)
	}

	return &Message{
		Type: msgType,
		Data: data,
	}, nil
}

// WriteMessage writes a message to a connection
func WriteMessage(w io.Writer, msgType uint8, data []byte) error {
	// Calculate total length (type + data)
	length := uint32(1 + len(data))

	// Write length
	if err := binary.Write(w, binary.LittleEndian, length); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Write type
	if err := binary.Write(w, binary.LittleEndian, msgType); err != nil {
		return fmt.Errorf("failed to write message type: %w", err)
	}

	// Write data
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write message data: %w", err)
	}

	return nil
}

// Encoding functions (simple binary encoding) - same as host

func EncodeMmapRegisterRequest(req *MmapRegisterRequest) []byte {
	pathLen := uint32(len(req.FilePath))
	buf := make([]byte, 4+pathLen+8+4)

	binary.LittleEndian.PutUint32(buf[0:4], pathLen)
	copy(buf[4:4+pathLen], []byte(req.FilePath))
	binary.LittleEndian.PutUint64(buf[4+pathLen:4+pathLen+8], req.Length)
	binary.LittleEndian.PutUint32(buf[4+pathLen+8:], req.Flags)

	return buf
}

func DecodeMmapRegisterResponse(data []byte) (*MmapRegisterResponse, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short")
	}

	return &MmapRegisterResponse{
		MmapID: binary.LittleEndian.Uint64(data[0:8]),
	}, nil
}

func EncodeMmapBulkReadRequest(req *MmapBulkReadRequest) []byte {
	buf := make([]byte, 24)
	binary.LittleEndian.PutUint64(buf[0:8], req.MmapID)
	binary.LittleEndian.PutUint64(buf[8:16], req.Offset)
	binary.LittleEndian.PutUint64(buf[16:24], req.Length)
	return buf
}

func DecodeMmapBulkReadResponse(data []byte) (*MmapBulkReadResponse, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("data too short")
	}

	dataLen := binary.LittleEndian.Uint32(data[0:4])
	if len(data) < int(4+dataLen) {
		return nil, fmt.Errorf("data too short for content")
	}

	return &MmapBulkReadResponse{
		Data: data[4 : 4+dataLen],
	}, nil
}

func EncodeMmapWriteRequest(req *MmapWriteRequest) []byte {
	dataLen := uint32(len(req.Data))
	buf := make([]byte, 20+dataLen)
	binary.LittleEndian.PutUint64(buf[0:8], req.MmapID)
	binary.LittleEndian.PutUint64(buf[8:16], req.Offset)
	binary.LittleEndian.PutUint32(buf[16:20], dataLen)
	copy(buf[20:], req.Data)
	return buf
}

func DecodeMmapWriteResponse(data []byte) (*MmapWriteResponse, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short")
	}

	return &MmapWriteResponse{
		BytesWritten: binary.LittleEndian.Uint64(data[0:8]),
	}, nil
}

func DecodeMmapNotifyMessage(data []byte) (*MmapNotifyMessage, error) {
	if len(data) < 24 {
		return nil, fmt.Errorf("data too short")
	}

	return &MmapNotifyMessage{
		MmapID: binary.LittleEndian.Uint64(data[0:8]),
		Offset: binary.LittleEndian.Uint64(data[8:16]),
		Length: binary.LittleEndian.Uint64(data[16:24]),
	}, nil
}

func DecodeErrorResponse(data []byte) (*ErrorResponse, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short")
	}

	msgLen := binary.LittleEndian.Uint32(data[4:8])
	if len(data) < int(8+msgLen) {
		return nil, fmt.Errorf("data too short for message")
	}

	return &ErrorResponse{
		Code:    binary.LittleEndian.Uint32(data[0:4]),
		Message: string(data[8 : 8+msgLen]),
	}, nil
}
