package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const (
	maximumMessageRoles = 512
	maximumPendingParts = 512
	maximumSSELineBytes = maximumSSERecordBytes + len("data: ")
)

type providerEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type eventTracker struct {
	externalSessionID string
	promptMessageID   string
	sink              agent.EventSink

	armed             bool
	promptSeen        bool
	assistantActivity bool
	assistantFinal    bool
	idleObserved      bool
	seenIDs           map[string]struct{}
	messageRoles      map[string]string
	messageParents    map[string]string
	currentAssistants map[string]bool
	firstTextFamily   map[string]string
	fallbackFamily    map[string]string
	families          map[string]string
	parts             map[string]*textPart
	pending           map[string][]pendingText
	pendingRecords    int
	pendingBytes      int
}

type textPart struct {
	text string
	kind string
}

type pendingText struct {
	family string
	partID string
	kind   string
	full   *string
	delta  string
}

type eventProperties struct {
	SessionID          string          `json:"sessionID"`
	MessageID          string          `json:"messageID"`
	PartID             string          `json:"partID"`
	Field              string          `json:"field"`
	Delta              string          `json:"delta"`
	Role               string          `json:"role"`
	Status             json.RawMessage `json:"status"`
	Info               *messageInfo    `json:"info"`
	Part               *messagePart    `json:"part"`
	Error              json.RawMessage `json:"error"`
	AssistantMessageID string          `json:"assistantMessageID"`
	TextID             string          `json:"textID"`
}

type messageInfo struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Role      string          `json:"role"`
	ParentID  string          `json:"parentID"`
	Finish    string          `json:"finish"`
	Error     json.RawMessage `json:"error"`
}

type messagePart struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	Role      string `json:"role"`
}

func consumeEvents(ctx context.Context, reader io.Reader, externalSessionID, promptMessageID string, dispatched <-chan struct{}, sink agent.EventSink) error {
	tracker := &eventTracker{
		externalSessionID: externalSessionID,
		promptMessageID:   promptMessageID,
		sink:              sink,
		seenIDs:           make(map[string]struct{}),
		messageRoles:      make(map[string]string),
		messageParents:    make(map[string]string),
		currentAssistants: make(map[string]bool),
		firstTextFamily:   make(map[string]string),
		fallbackFamily:    make(map[string]string),
		families:          make(map[string]string),
		parts:             make(map[string]*textPart),
		pending:           make(map[string][]pendingText),
	}
	oversizedLine := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maximumSSELineBytes+2)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline > maximumSSELineBytes {
			oversizedLine = true
			return newline + 1, []byte{}, nil
		}
		if len(data) > maximumSSELineBytes {
			oversizedLine = true
			return len(data), []byte{}, nil
		}
		return bufio.ScanLines(data, atEOF)
	})
	var record bytes.Buffer
	for scanner.Scan() {
		if oversizedLine {
			return protocolError("OpenCode event exceeds limit")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if record.Len() == 0 {
				continue
			}
			terminal, err := tracker.consume(ctx, record.Bytes(), dispatched)
			record.Reset()
			if err != nil {
				return err
			}
			if terminal {
				return nil
			}
			continue
		}
		if line[0] == ':' || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := line[len("data:"):]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		if record.Len() > 0 {
			_ = record.WriteByte('\n')
		}
		if record.Len()+len(data) > maximumSSERecordBytes {
			return protocolError("OpenCode event exceeds limit")
		}
		_, _ = record.Write(data)
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &agent.AdapterError{Code: "provider_unavailable", Err: errors.New("OpenCode event stream read failed")}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if record.Len() > 0 {
		terminal, err := tracker.consume(ctx, record.Bytes(), dispatched)
		if err != nil {
			return err
		}
		if terminal {
			return nil
		}
	}
	return protocolError("OpenCode event stream ended before terminal event")
}

func (tracker *eventTracker) consume(ctx context.Context, encoded []byte, dispatched <-chan struct{}) (bool, error) {
	if !tracker.armed {
		select {
		case <-dispatched:
			tracker.armed = true
		default:
		}
	}
	var event providerEvent
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&event); err != nil || event.ID == "" || len(event.ID) > 512 || event.Type == "" || len(event.Type) > 128 || len(event.Properties) == 0 {
		return false, protocolError("malformed OpenCode event")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		return false, protocolError("malformed OpenCode event")
	}
	var properties eventProperties
	if err := json.Unmarshal(event.Properties, &properties); err != nil {
		return false, protocolError("malformed OpenCode event properties")
	}
	sessionID, err := consistentSessionID(properties)
	if err != nil {
		return false, err
	}
	if sessionID != tracker.externalSessionID {
		return false, nil
	}
	if !tracker.armed {
		return false, nil
	}
	if _, duplicate := tracker.seenIDs[event.ID]; duplicate {
		return false, nil
	}
	if len(tracker.seenIDs) >= maximumEventIDs {
		return false, protocolError("too many OpenCode events")
	}
	tracker.seenIDs[event.ID] = struct{}{}

	switch event.Type {
	case "message.updated":
		return tracker.messageUpdated(ctx, properties)
	case "message.part.delta":
		return false, tracker.messageDelta(ctx, properties)
	case "message.part.updated":
		return false, tracker.messagePartUpdated(ctx, properties)
	case "session.next.text.delta":
		return false, tracker.compatibilityDelta(ctx, properties.AssistantMessageID, properties.TextID, properties.Delta)
	case "session.status":
		state, err := parseProviderState(properties.Status)
		if err != nil {
			return false, err
		}
		if err := tracker.sink.ProviderState(ctx, state); err != nil {
			return false, err
		}
		if state == "idle" && tracker.assistantActivity {
			tracker.idleObserved = true
		}
		// An assistant placeholder followed by idle can still be followed by
		// session.error. Only a non-tool final finish makes idle successful.
		return tracker.idleObserved && tracker.assistantFinal, nil
	case "session.idle":
		if tracker.assistantActivity {
			tracker.idleObserved = true
		}
		return tracker.idleObserved && tracker.assistantFinal, nil
	case "session.error":
		if !tracker.promptSeen && !tracker.assistantActivity {
			return false, nil
		}
		return false, classifySessionError(properties.Error)
	default:
		if strings.Contains(event.Type, "tool") && tracker.currentAssistants[properties.MessageID] {
			tracker.assistantActivity = true
		}
		return false, nil
	}
}

func (tracker *eventTracker) messageUpdated(ctx context.Context, properties eventProperties) (bool, error) {
	info := properties.Info
	if info == nil {
		return false, nil
	}
	role := strings.ToLower(info.Role)
	if !validEventIdentity(info.ID) {
		return false, protocolError("invalid OpenCode message identity")
	}
	if info.ParentID != "" && !validEventIdentity(info.ParentID) {
		return false, protocolError("invalid OpenCode parent identity")
	}
	if role != "assistant" && role != "user" && role != "system" {
		return false, nil
	}
	if len(tracker.messageRoles) >= maximumMessageRoles {
		if _, exists := tracker.messageRoles[info.ID]; !exists {
			return false, protocolError("too many OpenCode messages")
		}
	}
	if existingRole, exists := tracker.messageRoles[info.ID]; exists && (existingRole != role || tracker.messageParents[info.ID] != info.ParentID) {
		return false, protocolError("OpenCode message identity changed")
	}
	tracker.messageRoles[info.ID] = role
	tracker.messageParents[info.ID] = info.ParentID
	if role == "user" {
		// Production prompt requests deliberately let OpenCode allocate its own
		// monotonic message ID. The event subscription is live (not replayed), so
		// the first user message observed after the dispatch barrier is the
		// current prompt and becomes the correlation parent for this turn.
		if tracker.promptMessageID == "" {
			tracker.promptMessageID = info.ID
		}
		if info.ID == tracker.promptMessageID {
			tracker.promptSeen = true
		}
	}
	current := false
	if role == "assistant" && tracker.promptMessageID != "" && info.ParentID == tracker.promptMessageID {
		tracker.currentAssistants[info.ID] = true
		current = true
	}
	if current {
		tracker.assistantActivity = true
	}
	if tracker.currentAssistants[info.ID] {
		if len(info.Error) > 0 && string(info.Error) != "null" && string(info.Error) != "{}" {
			return false, classifySessionError(info.Error)
		}
		final, err := finalAssistantFinish(info.Finish)
		if err != nil {
			return false, err
		}
		if final {
			tracker.assistantFinal = true
		}
	}
	if tracker.currentAssistants[info.ID] {
		for _, pending := range tracker.pending[info.ID] {
			if pending.family == "compatibility" {
				if err := tracker.emitCompatibility(ctx, info.ID, pending.partID, pending.delta); err != nil {
					return false, err
				}
			} else if pending.full != nil {
				if err := tracker.emitFull(ctx, info.ID, pending.partID, pending.kind, *pending.full); err != nil {
					return false, err
				}
			} else if err := tracker.emitDelta(ctx, info.ID, pending.partID, pending.delta); err != nil {
				return false, err
			}
		}
	}
	tracker.clearPending(info.ID)
	return tracker.idleObserved && tracker.assistantFinal, nil
}

func (tracker *eventTracker) messageDelta(ctx context.Context, properties eventProperties) error {
	if properties.Field != "text" || properties.Delta == "" || !utf8.ValidString(properties.Delta) {
		return nil
	}
	if !validEventIdentity(properties.MessageID) || !validEventIdentity(properties.PartID) {
		return protocolError("invalid OpenCode text identity")
	}
	role, known := tracker.messageRoles[properties.MessageID]
	if known && !tracker.currentAssistants[properties.MessageID] {
		return nil
	}
	partID := partKey(properties.MessageID, properties.PartID)
	if role != "assistant" || !tracker.currentAssistants[properties.MessageID] {
		return tracker.queue(properties.MessageID, pendingText{family: "message-part", partID: partID, delta: properties.Delta})
	}
	return tracker.emitDelta(ctx, properties.MessageID, partID, properties.Delta)
}

func (tracker *eventTracker) messagePartUpdated(ctx context.Context, properties eventProperties) error {
	part := properties.Part
	if part == nil || (part.Type != "text" && part.Type != "reasoning") || !utf8.ValidString(part.Text) {
		return nil
	}
	if !validEventIdentity(part.MessageID) || !validEventIdentity(part.ID) {
		return protocolError("invalid OpenCode text identity")
	}
	if part.Role != "" && strings.ToLower(part.Role) != "assistant" {
		return nil
	}
	role, known := tracker.messageRoles[part.MessageID]
	if known && !tracker.currentAssistants[part.MessageID] {
		return nil
	}
	partID := partKey(part.MessageID, part.ID)
	if role != "assistant" || !tracker.currentAssistants[part.MessageID] {
		text := part.Text
		return tracker.queue(part.MessageID, pendingText{family: "message-part", partID: partID, kind: part.Type, full: &text})
	}
	return tracker.emitFull(ctx, part.MessageID, partID, part.Type, part.Text)
}

func (tracker *eventTracker) compatibilityDelta(ctx context.Context, assistantMessageID, textID, delta string) error {
	if delta == "" || !utf8.ValidString(delta) {
		return nil
	}
	if assistantMessageID == "" {
		return nil
	}
	if !validEventIdentity(assistantMessageID) || (textID != "" && !validEventIdentity(textID)) {
		return protocolError("invalid OpenCode compatibility identity")
	}
	if _, known := tracker.messageRoles[assistantMessageID]; known && !tracker.currentAssistants[assistantMessageID] {
		return nil
	}
	if !tracker.currentAssistants[assistantMessageID] {
		return tracker.queue(assistantMessageID, pendingText{family: "compatibility", partID: textID, delta: delta})
	}
	return tracker.emitCompatibility(ctx, assistantMessageID, textID, delta)
}

func (tracker *eventTracker) emitCompatibility(ctx context.Context, messageID, textID, delta string) error {
	if textID == "" {
		if tracker.fallbackFamily[messageID] == "" {
			tracker.fallbackFamily[messageID] = tracker.firstTextFamily[messageID]
			if tracker.fallbackFamily[messageID] == "" {
				tracker.fallbackFamily[messageID] = "compatibility"
			}
		}
		if tracker.fallbackFamily[messageID] != "compatibility" {
			return nil
		}
		tracker.recordFirstFamily(messageID, "compatibility")
		tracker.assistantActivity = true
		return tracker.sink.TextDelta(ctx, delta)
	}
	if fallback := tracker.fallbackFamily[messageID]; fallback != "" {
		if fallback != "compatibility" {
			return nil
		}
		tracker.recordFirstFamily(messageID, "compatibility")
		tracker.assistantActivity = true
		return tracker.sink.TextDelta(ctx, delta)
	}
	partID := partKey(messageID, textID)
	if family := tracker.families[partID]; family != "" && family != "compatibility" {
		return nil
	}
	tracker.families[partID] = "compatibility"
	tracker.recordFirstFamily(messageID, "compatibility")
	tracker.assistantActivity = true
	return tracker.sink.TextDelta(ctx, delta)
}

func (tracker *eventTracker) emitDelta(ctx context.Context, messageID, partID, delta string) error {
	if fallback := tracker.fallbackFamily[messageID]; fallback != "" && fallback != "message-part" {
		return nil
	}
	if family := tracker.families[partID]; family != "" && family != "message-part" {
		return nil
	}
	part, err := tracker.getPart(partID, "")
	if err != nil {
		return err
	}
	tracker.families[partID] = "message-part"
	tracker.recordFirstFamily(messageID, "message-part")
	tracker.assistantActivity = true
	part.text += delta
	return tracker.emitPartDelta(ctx, part.kind, delta)
}

func (tracker *eventTracker) emitFull(ctx context.Context, messageID, partID, kind, text string) error {
	if fallback := tracker.fallbackFamily[messageID]; fallback != "" && fallback != "message-part" {
		return nil
	}
	if family := tracker.families[partID]; family != "" && family != "message-part" {
		return nil
	}
	part, err := tracker.getPart(partID, kind)
	if err != nil {
		return err
	}
	if text == part.text {
		return nil
	}
	if strings.HasPrefix(part.text, text) {
		return nil
	}
	if !strings.HasPrefix(text, part.text) {
		return protocolError("OpenCode text part changed non-monotonically")
	}
	delta := text[len(part.text):]
	part.text = text
	if delta == "" {
		return nil
	}
	tracker.families[partID] = "message-part"
	tracker.recordFirstFamily(messageID, "message-part")
	tracker.assistantActivity = true
	return tracker.emitPartDelta(ctx, part.kind, delta)
}

func (tracker *eventTracker) emitPartDelta(ctx context.Context, kind, delta string) error {
	if kind == "reasoning" {
		return tracker.sink.ReasoningDelta(ctx, delta)
	}
	return tracker.sink.TextDelta(ctx, delta)
}

func (tracker *eventTracker) getPart(partID, kind string) (*textPart, error) {
	if partID == "" {
		return nil, protocolError("OpenCode text part lacks identity")
	}
	if part := tracker.parts[partID]; part != nil {
		if kind != "" && part.kind != kind {
			return nil, protocolError("OpenCode message part type changed")
		}
		return part, nil
	}
	if len(tracker.parts) >= maximumTextParts {
		return nil, protocolError("too many OpenCode text parts")
	}
	if kind == "" {
		kind = "text"
	}
	part := &textPart{kind: kind}
	tracker.parts[partID] = part
	return part, nil
}

func finalAssistantFinish(value string) (bool, error) {
	if value == "" {
		return false, nil
	}
	if len(value) > 128 || !utf8.ValidString(value) {
		return false, protocolError("invalid OpenCode assistant finish")
	}
	value = strings.ToLower(value)
	return value != "tool-calls" && value != "tool_calls", nil
}

func (tracker *eventTracker) queue(messageID string, text pendingText) error {
	if messageID == "" {
		return nil
	}
	size := len(text.delta)
	if text.full != nil {
		size = len(*text.full)
	}
	if tracker.pendingRecords >= maximumPendingParts || size > agent.MaxMessageBytes || tracker.pendingBytes > agent.MaxMessageBytes-size {
		return protocolError("too many unassociated OpenCode parts")
	}
	tracker.pending[messageID] = append(tracker.pending[messageID], text)
	tracker.pendingRecords++
	tracker.pendingBytes += size
	return nil
}

func (tracker *eventTracker) clearPending(messageID string) {
	for _, text := range tracker.pending[messageID] {
		tracker.pendingRecords--
		tracker.pendingBytes -= len(text.delta)
		if text.full != nil {
			tracker.pendingBytes -= len(*text.full)
		}
	}
	delete(tracker.pending, messageID)
}

func partKey(messageID, partID string) string {
	if messageID == "" || partID == "" {
		return ""
	}
	return messageID + "\x00" + partID
}

func validEventIdentity(value string) bool {
	return value != "" && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validSessionIdentity(value string) bool {
	return len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func (tracker *eventTracker) recordFirstFamily(messageID, family string) {
	if tracker.firstTextFamily[messageID] == "" {
		tracker.firstTextFamily[messageID] = family
	}
}

func consistentSessionID(properties eventProperties) (string, error) {
	values := []string{properties.SessionID}
	if properties.Info != nil {
		values = append(values, properties.Info.SessionID)
	}
	if properties.Part != nil {
		values = append(values, properties.Part.SessionID)
	}
	var sessionID string
	for _, value := range values {
		if value == "" {
			continue
		}
		if !validSessionIdentity(value) {
			return "", protocolError("invalid OpenCode session identity")
		}
		if sessionID != "" && sessionID != value {
			return "", protocolError("OpenCode event session identity changed")
		}
		sessionID = value
	}
	return sessionID, nil
}

func parseProviderState(raw json.RawMessage) (string, error) {
	var state string
	if len(raw) == 0 {
		return "", protocolError("OpenCode status lacks state")
	}
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &state); err != nil {
			return "", protocolError("malformed OpenCode status")
		}
	} else {
		var object struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return "", protocolError("malformed OpenCode status")
		}
		state = object.Type
	}
	state = strings.ToLower(state)
	if state == "" || len(state) > 128 || !utf8.ValidString(state) {
		return "", protocolError("invalid OpenCode status")
	}
	return state, nil
}

func classifySessionError(raw json.RawMessage) error {
	encoded := strings.ToLower(string(raw))
	code := "provider_unavailable"
	switch {
	case strings.Contains(encoded, "rate") || strings.Contains(encoded, "quota") || strings.Contains(encoded, "429"):
		code = "rate_limited"
	case strings.Contains(encoded, "unauthorized") || strings.Contains(encoded, "forbidden") || strings.Contains(encoded, "credential") || strings.Contains(encoded, "401") || strings.Contains(encoded, "403"):
		code = "unauthorized"
	}
	return &agent.AdapterError{Code: code, Err: fmt.Errorf("OpenCode session failed")}
}

func protocolError(message string) error {
	return &agent.AdapterError{Code: "protocol_error", Err: errors.New(message)}
}
