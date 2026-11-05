package piacp

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spachava753/acp-sdk/acp"
)

const (
	optionModel    = "model"
	optionThinking = "thinking"
)

type piState struct {
	Model *struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"model"`
	ThinkingLevel string `json:"thinkingLevel"`
	SessionFile   string `json:"sessionFile"`
	SessionID     string `json:"sessionId"`
	SessionName   string `json:"sessionName"`
}

type piModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// session is one open Pi session and its process.
type session struct {
	a   *adapter
	cwd string
	pi  *piProcess

	ctx    context.Context // ends with the session
	finish context.CancelFunc

	mu          sync.Mutex
	id          acp.SessionId
	path        string
	name        string
	current     string // the model, as provider/id, or "" with none
	thinking    string
	models      []piModel
	levels      []string
	turn        *turn
	dialogs     map[string]bool                  // extension dialogs Pi is waiting on
	contextSize uint64                           // the context window, as last reported
	cost        *acp.Cost                        // and the session's cost
	running     string                           // the tool call now executing
	reported    map[string]bool                  // tool calls the client knows about
	proposed    map[string][]acp.ToolCallContent // what file tools said they'd change
	stream      int                              // the assistant message being streamed
	closing     bool
	background  sync.WaitGroup // aborts and questions to the client
}

func newSession(a *adapter, cwd string) *session {
	ctx, finish := context.WithCancel(context.Background())
	return &session{a: a, cwd: cwd, ctx: ctx, finish: finish, dialogs: map[string]bool{}, reported: map[string]bool{}, proposed: map[string][]acp.ToolCallContent{}}
}

// ID is the session's ID, which Pi chooses for a fork.
func (s *session) ID() acp.SessionId {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) send(update acp.SessionUpdate) {
	if err := s.a.conn.SessionUpdate(s.ctx, &acp.SessionNotification{SessionID: s.ID(), Update: update}); err != nil && s.ctx.Err() == nil {
		s.a.diag("could not send an update for session %s: %v", s.ID(), err)
	}
}

func (s *session) readState(ctx context.Context) error {
	var state piState
	if err := s.pi.call(ctx, "get_state", nil, &state); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id, s.path, s.name, s.thinking = acp.SessionId(state.SessionID), state.SessionFile, state.SessionName, state.ThinkingLevel
	s.current = ""
	// With no provider configured Pi reports a model named "unknown".
	if m := state.Model; m != nil && m.ID != "" && m.ID != "unknown" {
		s.current = m.Provider + "/" + m.ID
	}
	return nil
}

func (s *session) file() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

func (s *session) hasFile() bool {
	path := s.file()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (s *session) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turn != nil
}

func (s *session) registered() bool { return s.a.session(s.ID()) == s }

func (s *session) closeUnlessRegistered() {
	if !s.registered() {
		s.close()
	}
}

// close ends the process. A turn in progress finds out when it exits.
func (s *session) close() {
	s.mu.Lock()
	s.closing = true
	if s.turn != nil {
		s.turn.cancelled = true
	}
	s.mu.Unlock()
	s.pi.close()
	s.finish() // Ends any question still waiting for the client's answer.
	s.background.Wait()
}

// refresh reads what Pi offers now. Available models come back empty when no
// provider is configured, and thinking levels depend on the model.
func (s *session) refresh(ctx context.Context) ([]acp.SessionConfigOption, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.readState(ctx); err != nil {
		return nil, err
	}
	var models struct {
		Models []piModel `json:"models"`
	}
	if err := s.pi.call(ctx, "get_available_models", nil, &models); err != nil {
		return nil, err
	}
	var levels struct {
		Levels []string `json:"levels"`
	}
	if err := s.pi.call(ctx, "get_available_thinking_levels", nil, &levels); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models, s.levels = models.Models, levels.Levels
	return s.optionsLocked(), nil
}

// optionsLocked builds the options from what Pi last reported. A model that can't
// think offers no thinking option, and no provider means no model option.
func (s *session) optionsLocked() []acp.SessionConfigOption {
	options := []acp.SessionConfigOption{}
	if len(s.models) > 0 {
		groups := acp.GroupedSessionConfigSelectOptions{}
		for _, m := range s.models {
			i := slices.IndexFunc(groups, func(g acp.SessionConfigSelectGroup) bool { return string(g.Group) == m.Provider })
			if i < 0 {
				groups = append(groups, acp.SessionConfigSelectGroup{Group: acp.SessionConfigGroupId(m.Provider), Name: m.Provider})
				i = len(groups) - 1
			}
			name := m.Name
			if name == "" {
				name = m.ID
			}
			groups[i].Options = append(groups[i].Options, acp.SessionConfigSelectOption{Name: name, Value: acp.SessionConfigValueId(m.Provider + "/" + m.ID)})
		}
		option := acp.SelectSessionConfigOption(optionModel, "Model", acp.SessionConfigValueId(s.current), acp.SessionConfigSelectOptions{Groups: &groups})
		category := acp.SessionConfigOptionCategoryModel
		option.Category = &category
		options = append(options, option)
	}
	if len(s.levels) > 1 {
		values := acp.UngroupedSessionConfigSelectOptions{}
		for _, level := range s.levels {
			values = append(values, acp.SessionConfigSelectOption{Name: level, Value: acp.SessionConfigValueId(level)})
		}
		option := acp.SelectSessionConfigOption(optionThinking, "Thinking", acp.SessionConfigValueId(s.thinking), acp.SessionConfigSelectOptions{Ungrouped: &values})
		category := acp.SessionConfigOptionCategoryThoughtLevel
		option.Category = &category
		options = append(options, option)
	}
	return options
}

// announce sends what a client should know about a session it's about to have,
// and returns the options for its response.
func (s *session) announce(ctx context.Context) ([]acp.SessionConfigOption, error) {
	options, err := s.refresh(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var commands struct {
		Commands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"commands"`
	}
	if err := s.pi.call(ctx, "get_commands", nil, &commands); err != nil {
		return nil, err
	}
	// Pi's terminal commands aren't available over RPC. Compacting has its own
	// command, so the adapter offers it.
	available := []acp.AvailableCommand{{Name: "compact", Description: "Compact the current context"}}
	for _, c := range commands.Commands {
		available = append(available, acp.AvailableCommand{Name: c.Name, Description: c.Description})
	}
	s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeAvailableCommandsUpdate, AvailableCommands: available})
	s.mu.Lock()
	name := s.name
	s.mu.Unlock()
	if name != "" {
		s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeSessionInfoUpdate, Title: &name})
	}
	s.reportUsage(ctx)
	return options, nil
}

// reportUsage sends context usage and cost. Right after compaction, and before
// the first response, Pi has no context figure, and none is sent.
func (s *session) reportUsage(ctx context.Context) {
	var stats struct {
		Cost         float64 `json:"cost"`
		ContextUsage *struct {
			Tokens        *uint64 `json:"tokens"`
			ContextWindow uint64  `json:"contextWindow"`
		} `json:"contextUsage"`
	}
	if err := s.pi.call(ctx, "get_session_stats", nil, &stats); err != nil {
		return
	}
	if u := stats.ContextUsage; u != nil && u.Tokens != nil && u.ContextWindow > 0 {
		// Pi's model costs are in US dollars.
		cost := &acp.Cost{Amount: stats.Cost, Currency: "USD"}
		s.mu.Lock()
		s.contextSize, s.cost = u.ContextWindow, cost
		s.mu.Unlock()
		s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeUsageUpdate, Used: *u.Tokens, Size: u.ContextWindow, Cost: cost})
	}
}

// setOption changes the model or thinking level, then re-reads everything: levels
// depend on the model, and changing to a model that can't think resets the level.
func (s *session) setOption(ctx context.Context, id string, value any) ([]acp.SessionConfigOption, error) {
	text, _ := value.(string)
	if text == "" {
		if v, ok := value.(acp.SessionConfigValueId); ok {
			text = string(v)
		}
	}
	timed, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	s.mu.Lock()
	levels := slices.Clone(s.levels)
	s.mu.Unlock()
	switch id {
	case optionModel:
		provider, model, ok := strings.Cut(text, "/")
		if !ok {
			return nil, invalidParams("unknown model %q", text)
		}
		if err := s.pi.call(timed, "set_model", map[string]any{"provider": provider, "modelId": model}, nil); err != nil {
			return nil, rejectedAsInvalid(err)
		}
	case optionThinking:
		// Pi accepts any value here and resets the level to off.
		if !slices.Contains(levels, text) {
			return nil, invalidParams("unknown thinking level %q", text)
		}
		if err := s.pi.call(timed, "set_thinking_level", map[string]any{"level": text}, nil); err != nil {
			return nil, rejectedAsInvalid(err)
		}
	default:
		return nil, invalidParams("unknown option %q", id)
	}
	return s.refresh(ctx)
}

func rejectedAsInvalid(err error) error {
	var r *rejected
	if errors.As(err, &r) {
		return invalidParams("%s", r.message)
	}
	return err
}
