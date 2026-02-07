package main

import (
	"bytes"
	"testing"
)

func TestProtocolEncoding(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{"MmapRegister", testMmapRegisterEncoding},
		{"MmapBulkRead", testMmapBulkReadEncoding},
		{"MmapWrite", testMmapWriteEncoding},
		{"MmapNotify", testMmapNotifyEncoding},
		{"Error", testErrorEncoding},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func testMmapRegisterEncoding(t *testing.T) {
	req := &MmapRegisterRequest{
		FilePath: "/tmp/test.db",
		Length:   4096,
		Flags:    1,
	}

	// Encode
	data := EncodeMmapRegisterRequest(req)

	// Decode
	decoded, err := DecodeMmapRegisterRequest(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	if decoded.FilePath != req.FilePath {
		t.Errorf("FilePath mismatch: expected %q, got %q", req.FilePath, decoded.FilePath)
	}
	if decoded.Length != req.Length {
		t.Errorf("Length mismatch: expected %d, got %d", req.Length, decoded.Length)
	}
	if decoded.Flags != req.Flags {
		t.Errorf("Flags mismatch: expected %d, got %d", req.Flags, decoded.Flags)
	}

	// Test response
	resp := &MmapRegisterResponse{MmapID: 42}
	respData := EncodeMmapRegisterResponse(resp)
	decodedResp, err := DecodeMmapRegisterResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if decodedResp.MmapID != resp.MmapID {
		t.Errorf("MmapID mismatch: expected %d, got %d", resp.MmapID, decodedResp.MmapID)
	}
}

func testMmapBulkReadEncoding(t *testing.T) {
	req := &MmapBulkReadRequest{
		MmapID: 123,
		Offset: 1024,
		Length: 4096,
	}

	// Encode
	data := EncodeMmapBulkReadRequest(req)

	// Decode
	decoded, err := DecodeMmapBulkReadRequest(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	if decoded.MmapID != req.MmapID {
		t.Errorf("MmapID mismatch: expected %d, got %d", req.MmapID, decoded.MmapID)
	}
	if decoded.Offset != req.Offset {
		t.Errorf("Offset mismatch: expected %d, got %d", req.Offset, decoded.Offset)
	}
	if decoded.Length != req.Length {
		t.Errorf("Length mismatch: expected %d, got %d", req.Length, decoded.Length)
	}

	// Test response
	resp := &MmapBulkReadResponse{Data: []byte("test data")}
	respData := EncodeMmapBulkReadResponse(resp)
	decodedResp, err := DecodeMmapBulkReadResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if string(decodedResp.Data) != string(resp.Data) {
		t.Errorf("Data mismatch: expected %q, got %q", resp.Data, decodedResp.Data)
	}
}

func testMmapWriteEncoding(t *testing.T) {
	req := &MmapWriteRequest{
		MmapID: 456,
		Offset: 2048,
		Data:   []byte("write this data"),
	}

	// Encode
	data := EncodeMmapWriteRequest(req)

	// Decode
	decoded, err := DecodeMmapWriteRequest(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	if decoded.MmapID != req.MmapID {
		t.Errorf("MmapID mismatch: expected %d, got %d", req.MmapID, decoded.MmapID)
	}
	if decoded.Offset != req.Offset {
		t.Errorf("Offset mismatch: expected %d, got %d", req.Offset, decoded.Offset)
	}
	if string(decoded.Data) != string(req.Data) {
		t.Errorf("Data mismatch: expected %q, got %q", req.Data, decoded.Data)
	}

	// Test response
	resp := &MmapWriteResponse{BytesWritten: 15}
	respData := EncodeMmapWriteResponse(resp)
	decodedResp, err := DecodeMmapWriteResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if decodedResp.BytesWritten != resp.BytesWritten {
		t.Errorf("BytesWritten mismatch: expected %d, got %d", resp.BytesWritten, decodedResp.BytesWritten)
	}
}

func testMmapNotifyEncoding(t *testing.T) {
	msg := &MmapNotifyMessage{
		MmapID: 789,
		Offset: 4096,
		Length: 8192,
	}

	// Encode
	data := EncodeMmapNotifyMessage(msg)

	// Decode
	decoded, err := DecodeMmapNotifyMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	if decoded.MmapID != msg.MmapID {
		t.Errorf("MmapID mismatch: expected %d, got %d", msg.MmapID, decoded.MmapID)
	}
	if decoded.Offset != msg.Offset {
		t.Errorf("Offset mismatch: expected %d, got %d", msg.Offset, decoded.Offset)
	}
	if decoded.Length != msg.Length {
		t.Errorf("Length mismatch: expected %d, got %d", msg.Length, decoded.Length)
	}
}

func testErrorEncoding(t *testing.T) {
	err := &ErrorResponse{
		Code:    404,
		Message: "not found",
	}

	// Encode
	data := EncodeErrorResponse(err)

	// Decode
	decoded, errDecode := DecodeErrorResponse(data)
	if errDecode != nil {
		t.Fatalf("Failed to decode: %v", errDecode)
	}

	// Verify
	if decoded.Code != err.Code {
		t.Errorf("Code mismatch: expected %d, got %d", err.Code, decoded.Code)
	}
	if decoded.Message != err.Message {
		t.Errorf("Message mismatch: expected %q, got %q", err.Message, decoded.Message)
	}
}

func TestMessageReadWrite(t *testing.T) {
	// Create a message
	testData := []byte("test message data")
	msgType := uint8(MsgTypeMmapWrite)

	// Write to buffer
	var buf bytes.Buffer
	if err := WriteMessage(&buf, msgType, testData); err != nil {
		t.Fatalf("Failed to write message: %v", err)
	}

	// Read from buffer
	msg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	// Verify
	if msg.Type != msgType {
		t.Errorf("Type mismatch: expected %d, got %d", msgType, msg.Type)
	}
	if string(msg.Data) != string(testData) {
		t.Errorf("Data mismatch: expected %q, got %q", testData, msg.Data)
	}
}

func TestMessageTooLarge(t *testing.T) {
	// Create a buffer with a message that claims to be too large
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0x01}) // ~16MB+ length
	buf.WriteByte(MsgTypeMmapWrite)

	// Try to read - should fail
	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("Expected error for message too large")
	}
}
