package agent

import (
	"encoding/json"
	"io"
)

// The SDK understands boolean options but v0.13.5 lacks their v1 capability
// field. Add just that field to initialize; do not claim other session features
// (such as compaction) that the SDK cannot decode. Remove this bridge when the
// SDK exposes clientCapabilities.session.configOptions.boolean.
// The SDK writes one complete newline-delimited JSON-RPC frame per Write.
// See https://agentclientprotocol.com/protocol/v1/initialization.
type capabilityWriter struct{ io.Writer }

func (w capabilityWriter) Write(p []byte) (int, error) {
	var frame struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(p, &frame) != nil || frame.Method != "initialize" {
		return w.Writer.Write(p)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(p, &message); err != nil {
		return 0, err
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(message["params"], &params); err != nil {
		return 0, err
	}
	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(params["clientCapabilities"], &capabilities); err != nil {
		return 0, err
	}
	if capabilities == nil {
		capabilities = map[string]json.RawMessage{}
	}
	capabilities["session"] = json.RawMessage(`{"configOptions":{"boolean":{}}}`)
	var err error
	if params["clientCapabilities"], err = json.Marshal(capabilities); err != nil {
		return 0, err
	}
	if message["params"], err = json.Marshal(params); err != nil {
		return 0, err
	}
	wire, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	wire = append(wire, '\n')
	n, err := w.Writer.Write(wire)
	if err != nil {
		return 0, err
	}
	if n != len(wire) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
