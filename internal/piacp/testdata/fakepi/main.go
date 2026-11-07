// A deterministic stand-in for `pi --mode rpc`, used by the adapter's tests. Its
// records copy the shapes real Pi 1.5.0 was seen to produce. The prompt's text
// picks what a turn does.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type object = map[string]any

var (
	writeMu sync.Mutex
	stateMu sync.Mutex

	sessionID, sessionPath, sessionName, cwd string
	model                                    = "fake/basic"
	thinking                                 = "off"
	entries                                  []object
	nextEntry                                int
	written                                  bool
	turns                                    int
	running                                  chan struct{}
	settled                                  chan struct{}
	dialogs                                  = map[string]chan object{}
	dialogSeq                                int
	compacted                                bool
)

func emit(record object) {
	writeMu.Lock()
	defer writeMu.Unlock()
	data, _ := json.Marshal(record)
	fmt.Println(string(data))
}

func logLine(entry object) {
	path := os.Getenv("FAKEPI_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(entry)
	fmt.Fprintln(f, string(data))
}

func root() string {
	dir := os.Getenv("FAKEPI_SESSIONS")
	if os.Getenv("FAKEPI_FLAT") != "" {
		return dir
	}
	return filepath.Join(dir, "--"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")+"--")
}

func newPath(id string) string {
	return filepath.Join(root(), time.Now().UTC().Format("2006-01-02T15-04-05.000Z")+"_"+id+".jsonl")
}

// record adds an entry to the session tree, writing it if the session has a file.
func record(entry object) {
	stateMu.Lock()
	defer stateMu.Unlock()
	nextEntry++
	entry["id"] = fmt.Sprintf("entry-%d", nextEntry)
	if len(entries) > 0 {
		entry["parentId"] = entries[len(entries)-1]["id"]
	}
	entries = append(entries, entry)
	if written {
		f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		data, _ := json.Marshal(entry)
		fmt.Fprintln(f, string(data))
		return
	}
	flushLocked()
}

// flushLocked writes the session, which Pi does only once it has an assistant
// message.
func flushLocked() {
	if sessionPath == "" || written {
		return
	}
	hasAssistant := false
	for _, e := range entries {
		if m, ok := e["message"].(object); ok {
			hasAssistant = hasAssistant || m["role"] == "assistant"
		}
	}
	if !hasAssistant {
		return
	}
	_ = os.MkdirAll(filepath.Dir(sessionPath), 0o700)
	f, err := os.Create(sessionPath)
	if err != nil {
		return
	}
	defer f.Close()
	lines := append([]object{{"type": "session", "version": 3, "id": sessionID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": cwd}}, entries...)
	for _, line := range lines {
		data, _ := json.Marshal(line)
		fmt.Fprintln(f, string(data))
	}
	written = true
}

func addMessage(m object) { record(object{"type": "message", "message": m}) }

// contextMessages is what Pi's get_messages gives: what the model will see next,
// which after a compaction leaves out what came before.
func contextMessages() []object {
	stateMu.Lock()
	defer stateMu.Unlock()
	messages := []object{}
	for _, e := range entries {
		switch e["type"] {
		case "compaction":
			messages = []object{{"role": "compactionSummary", "summary": e["summary"]}}
		case "message":
			messages = append(messages, e["message"].(object))
		}
	}
	return messages
}

// readFile loads a session, returning false if it isn't one.
func readFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var header struct {
		Type, ID, Cwd string
	}
	if json.Unmarshal([]byte(lines[0]), &header) != nil || header.Type != "session" {
		return false
	}
	sessionID, entries = header.ID, nil
	for _, line := range lines[1:] {
		var entry object
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		entries = append(entries, entry)
		nextEntry++
		if entry["type"] == "session_info" {
			sessionName, _ = entry["name"].(string)
		}
	}
	return true
}

func available() []object {
	if os.Getenv("FAKEPI_NO_MODELS") != "" {
		return []object{}
	}
	return []object{
		{"id": "basic", "name": "Basic", "provider": "fake", "reasoning": false, "contextWindow": 100000},
		{"id": "deep", "name": "Deep", "provider": "fake", "reasoning": true, "contextWindow": 200000},
	}
}

func levels() []string {
	if model == "fake/deep" && os.Getenv("FAKEPI_NO_MODELS") == "" {
		return []string{"off", "low", "high"}
	}
	return []string{"off"}
}

func stateData() object {
	provider, id, _ := strings.Cut(model, "/")
	if os.Getenv("FAKEPI_NO_MODELS") != "" {
		provider, id = "unknown", "unknown"
	}
	state := object{"model": object{"id": id, "provider": provider}, "thinkingLevel": thinking, "isStreaming": running != nil, "sessionFile": sessionPath, "sessionId": sessionID, "messageCount": len(contextMessages())}
	if sessionName != "" {
		state["sessionName"] = sessionName
	}
	return state
}

func respond(id any, command string, data any) {
	record := object{"type": "response", "command": command, "success": true}
	if id != nil {
		record["id"] = id
	}
	if data != nil {
		record["data"] = data
	}
	emit(record)
}

func refuse(id any, command, reason string) {
	emit(object{"id": id, "type": "response", "command": command, "success": false, "error": reason})
}

func usage(total int) object {
	return object{"input": total - 8, "output": 8, "cacheRead": 0, "cacheWrite": 0, "totalTokens": total}
}

func update(event object) {
	emit(object{"type": "message_update", "usage": usage(48), "assistantMessageEvent": event})
}

func assistant(content []object, stop, failure string) object {
	m := object{"role": "assistant", "content": content, "stopReason": stop, "usage": usage(48), "api": "fake", "provider": "fake", "model": "basic"}
	if failure != "" {
		m["errorMessage"] = failure
	}
	return m
}

func endMessage(m object) {
	addMessage(m)
	emit(object{"type": "message_end", "message": m})
}

func streamText(index int, text string) {
	update(object{"type": "text_start", "contentIndex": index})
	update(object{"type": "text_delta", "contentIndex": index, "delta": text})
	update(object{"type": "text_end", "contentIndex": index, "content": text})
}

func beginTurn(text string, images []object) {
	emit(object{"type": "agent_start"})
	emit(object{"type": "turn_start"})
	if turns == 0 {
		system := object{"role": "system", "content": ""}
		emit(object{"type": "message_start", "message": system})
		endMessage(system)
	}
	turns++
	content := []object{{"type": "text", "text": text}}
	for _, image := range images {
		content = append(content, image)
	}
	user := object{"role": "user", "content": content, "timestamp": time.Now().UnixMilli()}
	emit(object{"type": "message_start", "message": user})
	endMessage(user)
	emit(object{"type": "message_start", "message": object{"role": "assistant", "content": []object{}, "stopReason": "pending"}})
}

func endTurn(aborted bool) {
	emit(object{"type": "turn_end"})
	emit(object{"type": "agent_end", "messages": []object{}, "willRetry": false})
	stateMu.Lock()
	done := settled
	running, settled = nil, nil
	stateMu.Unlock()
	emit(object{"type": "agent_settled", "aborted": aborted})
	if done != nil {
		close(done)
	}
}

func toolStart(id, name string, arguments object) {
	update(object{"type": "toolcall_start", "contentIndex": 1, "id": id, "toolName": name})
	update(object{"type": "toolcall_delta", "contentIndex": 1, "delta": "{"})
	update(object{"type": "toolcall_end", "contentIndex": 1, "toolCall": object{"type": "toolCall", "id": id, "name": name, "arguments": arguments}})
}

func toolResult(id, name, text string, failed bool) { toolResultWith(id, name, text, nil, failed) }

func toolResultWith(id, name, text string, details object, failed bool) {
	result := object{"content": []object{{"type": "text", "text": text}}}
	if details != nil {
		result["details"] = details
	}
	emit(object{"type": "tool_execution_end", "toolCallId": id, "toolName": name, "result": result, "isError": failed, "durationMs": 3})
	m := object{"role": "toolResult", "toolCallId": id, "toolName": name, "content": result["content"], "isError": failed}
	if details != nil {
		m["details"] = details
	}
	emit(object{"type": "message_start", "message": m})
	endMessage(m)
}

func ask(method string, fields object) object {
	stateMu.Lock()
	dialogSeq++
	id := fmt.Sprintf("dialog-%d", dialogSeq)
	answer := make(chan object, 1)
	dialogs[id] = answer
	stateMu.Unlock()
	request := object{"type": "extension_ui_request", "id": id, "method": method}
	for k, v := range fields {
		request[k] = v
	}
	emit(request)
	return <-answer
}

// runTurn plays out the turn the prompt's text names.
func runTurn(text string, images []object, stop chan struct{}) {
	beginTurn(text, images)
	switch {
	case strings.Contains(text, "exit-now"):
		os.Exit(3)
	case strings.Contains(text, "fail-now"):
		endMessage(assistant([]object{}, "error", "400: the model refused"))
	case strings.Contains(text, "too-long"):
		streamText(0, "Partial")
		endMessage(assistant([]object{{"type": "text", "text": "Partial"}}, "length", ""))
	case strings.Contains(text, "retry-once"):
		endMessage(assistant([]object{}, "error", "503: overloaded"))
		emit(object{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 10, "errorMessage": "503: overloaded"})
		emit(object{"type": "auto_retry_end", "success": true, "attempt": 2})
		streamText(0, "Recovered")
		endMessage(assistant([]object{{"type": "text", "text": "Recovered"}}, "stop", ""))
	case strings.Contains(text, "run-slowly"):
		streamText(0, "Working")
		<-stop
		endMessage(assistant([]object{{"type": "text", "text": "Working"}}, "aborted", "Request was aborted"))
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		emit(object{"type": "message_start", "message": object{"role": "assistant", "content": []object{}}})
		endMessage(assistant([]object{}, "aborted", "Request was aborted"))
		endTurn(true)
		return
	case strings.Contains(text, "use-a-tool"):
		arguments := object{"command": "echo hi\nsecond line"}
		update(object{"type": "text_start", "contentIndex": 0})
		update(object{"type": "text_delta", "contentIndex": 0, "delta": "Running. "})
		toolStart("call-1", "bash", arguments)
		update(object{"type": "text_end", "contentIndex": 0, "content": "Running. "})
		endMessage(assistant([]object{{"type": "text", "text": "Running. "}, {"type": "toolCall", "id": "call-1", "name": "bash", "arguments": arguments}}, "toolUse", ""))
		emit(object{"type": "tool_execution_start", "toolCallId": "call-1", "toolName": "bash", "args": arguments})
		emit(object{"type": "tool_execution_update", "toolCallId": "call-1", "toolName": "bash", "partialResult": object{"content": []object{}}})
		emit(object{"type": "tool_execution_update", "toolCallId": "call-1", "toolName": "bash", "partialResult": object{"content": []object{{"type": "text", "text": "hi\n"}}}})
		toolResult("call-1", "bash", "hi\nthere\n", false)
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		streamText(0, "Done.")
		endMessage(assistant([]object{{"type": "text", "text": "Done."}}, "stop", ""))
	case strings.Contains(text, "edit-file"):
		arguments := object{"path": "a.txt", "edits": []object{{"oldText": "two", "newText": "TWO"}, {"oldText": "eight", "newText": "EIGHT"}}}
		toolStart("call-edit", "edit", arguments)
		endMessage(assistant([]object{{"type": "toolCall", "id": "call-edit", "name": "edit", "arguments": arguments}}, "toolUse", ""))
		emit(object{"type": "tool_execution_start", "toolCallId": "call-edit", "toolName": "edit", "args": arguments})
		patch := "--- a.txt\n+++ a.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n@@ -7,2 +7,2 @@\n seven\n-eight\n+EIGHT\n"
		toolResultWith("call-edit", "edit", "Successfully replaced 2 block(s) in a.txt.", object{"diff": "unused", "patch": patch, "firstChangedLine": 2}, false)
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		streamText(0, "Edited.")
		endMessage(assistant([]object{{"type": "text", "text": "Edited."}}, "stop", ""))
	case strings.Contains(text, "write-file"):
		arguments := object{"path": "new.txt", "content": "fresh\n"}
		toolStart("call-write", "write", arguments)
		endMessage(assistant([]object{{"type": "toolCall", "id": "call-write", "name": "write", "arguments": arguments}}, "toolUse", ""))
		emit(object{"type": "tool_execution_start", "toolCallId": "call-write", "toolName": "write", "args": arguments})
		toolResult("call-write", "write", "Successfully wrote to new.txt", false)
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		streamText(0, "Wrote.")
		endMessage(assistant([]object{{"type": "text", "text": "Wrote."}}, "stop", ""))
	case strings.Contains(text, "edit-fails"):
		arguments := object{"path": "a.txt", "edits": []object{{"oldText": "absent", "newText": "x"}}}
		toolStart("call-bad", "edit", arguments)
		endMessage(assistant([]object{{"type": "toolCall", "id": "call-bad", "name": "edit", "arguments": arguments}}, "toolUse", ""))
		emit(object{"type": "tool_execution_start", "toolCallId": "call-bad", "toolName": "edit", "args": arguments})
		toolResultWith("call-bad", "edit", "Could not find edits[0] in a.txt.", object{}, true)
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		streamText(0, "Sorry.")
		endMessage(assistant([]object{{"type": "text", "text": "Sorry."}}, "stop", ""))
	case strings.Contains(text, "gate-tool"):
		arguments := object{"path": "notes.txt"}
		toolStart("call-2", "read", arguments)
		endMessage(assistant([]object{{"type": "toolCall", "id": "call-2", "name": "read", "arguments": arguments}}, "toolUse", ""))
		emit(object{"type": "tool_execution_start", "toolCallId": "call-2", "toolName": "read", "args": arguments})
		answer := ask("confirm", object{"title": "Allow read?", "message": "From an extension"})
		if answer["confirmed"] == true {
			toolResult("call-2", "read", "contents", false)
		} else {
			toolResult("call-2", "read", "Blocked", true)
		}
		emit(object{"type": "turn_end"})
		emit(object{"type": "turn_start"})
		streamText(0, fmt.Sprintf("Answer: %v", answer["confirmed"]))
		endMessage(assistant([]object{{"type": "text", "text": "ok"}}, "stop", ""))
	case strings.Contains(text, "ask-for-text"):
		answer := ask("input", object{"title": "Your name", "placeholder": "name"})
		streamText(0, fmt.Sprintf("Cancelled: %v", answer["cancelled"]))
		endMessage(assistant([]object{{"type": "text", "text": "ok"}}, "stop", ""))
	default:
		reply := "Hello from Pi."
		update(object{"type": "thinking_start", "contentIndex": 0})
		update(object{"type": "thinking_delta", "contentIndex": 0, "delta": "Considering."})
		update(object{"type": "thinking_end", "contentIndex": 0, "content": "Considering."})
		streamText(1, reply)
		endMessage(assistant([]object{{"type": "thinking", "thinking": "Considering."}, {"type": "text", "text": reply}}, "stop", ""))
	}
	endTurn(false)
}

func main() {
	var sessionFlag, forkFlag string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version":
			version := os.Getenv("FAKEPI_VERSION")
			if version == "" {
				version = "1.5.0"
			}
			fmt.Println(version)
			return
		case "--session-id":
			i++
			sessionFlag = args[i]
		case "--fork":
			i++
			forkFlag = args[i]
		}
	}
	cwd, _ = os.Getwd()
	logLine(object{"args": args, "cwd": cwd, "pid": os.Getpid()})
	switch {
	case forkFlag != "":
		if !readFile(forkFlag) {
			fmt.Fprintln(os.Stderr, "Error: Cannot fork: source session file is empty or invalid: "+forkFlag)
			os.Exit(1)
		}
		sessionID = fmt.Sprintf("fork-%d", time.Now().UnixNano())
		sessionPath = newPath(sessionID)
		written = false
		stateMu.Lock()
		flushLocked()
		stateMu.Unlock()
	case sessionFlag != "":
		sessionID = sessionFlag
		matches, _ := filepath.Glob(filepath.Join(root(), "*_"+sessionFlag+".jsonl"))
		if len(matches) > 0 && readFile(matches[0]) {
			sessionPath, written = matches[0], true
		} else {
			fmt.Fprintf(os.Stderr, "Warning: No project session found with id '%s'; creating a new session with that id.\n", sessionFlag)
			sessionPath = newPath(sessionFlag)
		}
	default:
		sessionID = fmt.Sprintf("auto-%d", time.Now().UnixNano())
		sessionPath = newPath(sessionID)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var c struct {
			ID                 any      `json:"id"`
			Type               string   `json:"type"`
			Message            string   `json:"message"`
			Images             []object `json:"images"`
			Provider           string   `json:"provider"`
			ModelID            string   `json:"modelId"`
			Level              string   `json:"level"`
			Name               string   `json:"name"`
			CustomInstructions string   `json:"customInstructions"`
			Confirmed          *bool    `json:"confirmed"`
			Cancelled          bool     `json:"cancelled"`
			Value              *string  `json:"value"`
		}
		if json.Unmarshal(scanner.Bytes(), &c) != nil {
			continue
		}
		logLine(object{"command": c.Type, "message": c.Message, "customInstructions": c.CustomInstructions, "pid": os.Getpid()})
		switch c.Type {
		case "get_state":
			respond(c.ID, c.Type, stateData())
		case "get_available_models":
			respond(c.ID, c.Type, object{"models": available()})
		case "get_available_thinking_levels":
			respond(c.ID, c.Type, object{"levels": levels()})
		case "get_commands":
			respond(c.ID, c.Type, object{"commands": []object{{"name": "fix-tests", "description": "Fix failing tests", "source": "prompt"}}})
		case "get_session_stats":
			data := object{"cost": 0.25}
			if compacted {
				data["contextUsage"] = object{"tokens": nil, "contextWindow": 100000, "percent": nil}
			} else if len(contextMessages()) > 0 {
				data["contextUsage"] = object{"tokens": 48, "contextWindow": 100000, "percent": 0.048}
			}
			respond(c.ID, c.Type, data)
		case "get_messages":
			respond(c.ID, c.Type, object{"messages": contextMessages()})
		case "get_entries":
			// Everything, including a branch that was abandoned, and the leaf.
			stateMu.Lock()
			all := slices.Clone(entries)
			leaf := any(nil)
			if len(entries) > 0 {
				leaf = entries[len(entries)-1]["id"]
				all = append(all, object{"type": "message", "id": "abandoned", "parentId": entries[0]["id"], "message": object{"role": "user", "content": "from an abandoned branch"}})
			}
			stateMu.Unlock()
			respond(c.ID, c.Type, object{"entries": all, "leafId": leaf})
		case "set_model":
			if c.Provider != "fake" || (c.ModelID != "basic" && c.ModelID != "deep") {
				refuse(c.ID, c.Type, "Model not found: "+c.Provider+"/"+c.ModelID)
				continue
			}
			model = c.Provider + "/" + c.ModelID
			if model == "fake/basic" && thinking != "off" {
				thinking = "off"
				emit(object{"type": "thinking_level_changed", "level": "off"})
			}
			respond(c.ID, c.Type, object{"id": c.ModelID, "provider": c.Provider})
		case "set_thinking_level":
			thinking = c.Level
			found := false
			for _, level := range levels() {
				found = found || level == c.Level
			}
			if !found {
				thinking = "off"
			}
			emit(object{"type": "thinking_level_changed", "level": thinking})
			respond(c.ID, c.Type, nil)
		case "set_session_name":
			sessionName = c.Name
			record(object{"type": "session_info", "name": c.Name})
			emit(object{"type": "session_info_changed", "name": c.Name})
			respond(c.ID, c.Type, nil)
		case "compact":
			if len(contextMessages()) < 2 {
				refuse(c.ID, c.Type, "Nothing to compact (session too small)")
				continue
			}
			record(object{"type": "compaction", "summary": "Summary of earlier work."})
			compacted = true
			respond(c.ID, c.Type, object{"summary": "Short", "tokensBefore": 150000, "estimatedTokensAfter": 32000})
		case "abort":
			stateMu.Lock()
			stop, done := running, settled
			if stop != nil {
				select {
				case <-stop:
				default:
					close(stop)
				}
			}
			stateMu.Unlock()
			if done != nil {
				<-done // Pi answers once the session is idle.
			}
			respond(c.ID, c.Type, nil)
		case "extension_ui_response":
			stateMu.Lock()
			answer := dialogs[fmt.Sprint(c.ID)]
			delete(dialogs, fmt.Sprint(c.ID))
			stateMu.Unlock()
			if answer != nil {
				answer <- object{"confirmed": c.Confirmed != nil && *c.Confirmed, "cancelled": c.Cancelled, "value": c.Value}
			}
		case "prompt":
			if os.Getenv("FAKEPI_NO_MODELS") != "" {
				refuse(c.ID, c.Type, "No API key found for the selected model.\n\nUse /login to log into a provider via OAuth or API key.")
				continue
			}
			if running != nil {
				refuse(c.ID, c.Type, "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
				continue
			}
			if strings.Contains(c.Message, "extension-command") {
				// Its response waits for its dialog.
				go func(id any) {
					answer := ask("confirm", object{"title": "Proceed?", "message": ""})
					emit(object{"type": "extension_ui_request", "id": "n", "method": "notify", "message": fmt.Sprintf("answered %v", answer["confirmed"]), "notifyType": "warning"})
					respond(id, "prompt", object{"disposition": "handled"})
				}(c.ID)
				continue
			}
			stop := make(chan struct{})
			stateMu.Lock()
			running, settled = stop, make(chan struct{})
			stateMu.Unlock()
			respond(c.ID, c.Type, object{"disposition": "started"})
			go runTurn(c.Message, c.Images, stop)
		}
	}
}
