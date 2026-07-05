package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	params, _ := json.Marshal(StartVMParams{WorkDir: "/tmp/x", ShareMode: "9p"})
	orig := Request{Method: MethodStartVM, Params: params, ID: 7}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != orig.Method || got.ID != orig.ID {
		t.Fatalf("mismatch: got %+v want %+v", got, orig)
	}
	var p StartVMParams
	if err := json.Unmarshal(got.Params, &p); err != nil {
		t.Fatalf("params unmarshal: %v", err)
	}
	if p.WorkDir != "/tmp/x" || p.ShareMode != "9p" {
		t.Fatalf("params mismatch: %+v", p)
	}
}

func TestResponseRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		resp Response
	}{
		{
			name: "log event",
			resp: func() Response {
				d, _ := json.Marshal(LogEvent{Line: "building..."})
				return Response{Event: "log", Data: d, ID: 1}
			}(),
		},
		{
			name: "result",
			resp: func() Response {
				r, _ := json.Marshal(PingResult{PID: 42, Version: 33})
				return Response{Result: r, ID: 1}
			}(),
		},
		{
			name: "error",
			resp: Response{Error: &Error{Code: CodeUnknownVM, Message: "nope"}, ID: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Response
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Event != tc.resp.Event || got.ID != tc.resp.ID {
				t.Fatalf("mismatch: got %+v want %+v", got, tc.resp)
			}
			if tc.resp.Error != nil {
				if got.Error == nil || got.Error.Code != tc.resp.Error.Code || got.Error.Message != tc.resp.Error.Message {
					t.Fatalf("error mismatch: got %+v want %+v", got.Error, tc.resp.Error)
				}
			}
		})
	}
}

// TestStreamOrdering asserts that log events arrive before the terminating
// result, and that the result frame ends the stream, using a real socket pair.
func TestStreamOrdering(t *testing.T) {
	serverEnd, clientEnd := net.Pipe()

	go func() {
		cw := &connWriter{conn: serverEnd}
		ew := &eventWriter{cw: cw, id: 1}
		ew.Write([]byte("first line\n"))
		ew.Write([]byte("second line\n"))
		ew.Flush()
		cw.sendResult(1, PingResult{PID: 99, Version: 33})
		serverEnd.Close()
	}()

	scanner := bufio.NewScanner(clientEnd)
	var events []string
	var result *PingResult
	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Event == "log" {
			if result != nil {
				t.Fatal("log event arrived after result")
			}
			var le LogEvent
			json.Unmarshal(resp.Data, &le)
			events = append(events, le.Line)
			continue
		}
		if resp.Error != nil {
			t.Fatalf("unexpected error frame: %v", resp.Error)
		}
		var pr PingResult
		if err := json.Unmarshal(resp.Result, &pr); err != nil {
			t.Fatalf("result unmarshal: %v", err)
		}
		result = &pr
		break // result terminates the stream
	}
	clientEnd.Close()

	if len(events) != 2 || events[0] != "first line" || events[1] != "second line" {
		t.Fatalf("unexpected events: %v", events)
	}
	if result == nil || result.PID != 99 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestErrorFrameDecodes asserts an error frame is surfaced as an error.
func TestErrorFrameDecodes(t *testing.T) {
	serverEnd, clientEnd := net.Pipe()
	go func() {
		cw := &connWriter{conn: serverEnd}
		cw.sendError(1, CodeInternal, "boom")
		serverEnd.Close()
	}()

	scanner := bufio.NewScanner(clientEnd)
	if !scanner.Scan() {
		t.Fatal("no frame received")
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	clientEnd.Close()
	if resp.Error == nil {
		t.Fatal("expected error frame")
	}
	if resp.Error.Code != CodeInternal || resp.Error.Message != "boom" {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	// Error implements the error interface.
	var e error = resp.Error
	if e.Error() != "boom" {
		t.Fatalf("Error() = %q", e.Error())
	}
}
