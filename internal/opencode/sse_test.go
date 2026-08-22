package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aisummoner/aisummoner/internal/agent"
)

type recordingSink struct {
	mu        sync.Mutex
	deltas    []string
	reasoning []string
	states    []string
	external  string
	err       error
}

func (sink *recordingSink) ReasoningDelta(_ context.Context, delta string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.reasoning = append(sink.reasoning, delta)
	return sink.err
}

func (sink *recordingSink) TextDelta(_ context.Context, delta string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.deltas = append(sink.deltas, delta)
	return sink.err
}

func (sink *recordingSink) ProviderState(_ context.Context, state string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.states = append(sink.states, state)
	return sink.err
}

func (sink *recordingSink) SetExternalSessionID(_ context.Context, externalSessionID string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.external = externalSessionID
	return sink.err
}

func (sink *recordingSink) text() string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return strings.Join(sink.deltas, "")
}

func (sink *recordingSink) thought() string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return strings.Join(sink.reasoning, "")
}

func sseEvent(t *testing.T, id, eventType string, properties any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"id": id, "type": eventType, "properties": properties})
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(encoded) + "\n\n"
}

func armed() <-chan struct{} {
	ready := make(chan struct{})
	close(ready)
	return ready
}

func TestConsumeEventsFullSnapshotsAndDeltaDeduplication(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := strings.Join([]string{
		sseEvent(t, "1", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}),
		sseEvent(t, "2", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_a", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "A"}}),
		sseEvent(t, "3", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_a", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "AB"}}),
		sseEvent(t, "4", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_a", "field": "text", "delta": "C"}),
		sseEvent(t, "5", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_a", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "ABC"}}),
		sseEvent(t, "6", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_a", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "AB"}}),
		sseEvent(t, "7", "session.idle", map[string]any{"sessionID": sessionID}),
	}, "")
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if got := sink.text(); got != "ABC" {
		t.Fatalf("assistant text=%q", got)
	}
}

func TestConsumeEventsTracksMultipleTextPartsIndependently(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) +
		sseEvent(t, "compat-1", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_1", "delta": "A"}) +
		sseEvent(t, "message-1-duplicate", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_1", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "A"}}) +
		sseEvent(t, "message-2", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_2", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": "B"}}) +
		sseEvent(t, "compat-2-duplicate", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_2", "delta": "B"}) +
		sseEvent(t, "compat-1-more", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_1", "delta": "C"}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID})
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.text() != "ABC" {
		t.Fatalf("multi-part text=%q", sink.text())
	}
}

func TestConsumeEventsLegacyCompatibilityLatchAvoidsCrossFamilyDuplicates(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	for _, test := range []struct {
		name   string
		events string
		want   string
	}{
		{name: "stable first", events: sseEvent(t, "stable", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_a", "field": "text", "delta": "stable"}) + sseEvent(t, "legacy", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "delta": "duplicate"}), want: "stable"},
		{name: "legacy first", events: sseEvent(t, "legacy", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "delta": "legacy"}) + sseEvent(t, "stable", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_a", "field": "text", "delta": "duplicate"}), want: "legacy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) + test.events + sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID})
			sink := &recordingSink{}
			if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
				t.Fatal(err)
			}
			if sink.text() != test.want {
				t.Fatalf("text=%q want=%q", sink.text(), test.want)
			}
		})
	}
}

func TestConsumeEventsOptionalTextIDActivatesConservativeMessageFallback(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	for _, test := range []struct {
		name   string
		events string
		want   string
	}{
		{name: "stable then missing then keyed compatibility", events: sseEvent(t, "stable", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_1", "field": "text", "delta": "A"}) + sseEvent(t, "missing", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "delta": "duplicate"}) + sseEvent(t, "keyed", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_2", "delta": "duplicate"}), want: "A"},
		{name: "keyed then missing compatibility then stable", events: sseEvent(t, "keyed", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_1", "delta": "A"}) + sseEvent(t, "missing", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "delta": "B"}) + sseEvent(t, "stable", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_2", "field": "text", "delta": "duplicate"}), want: "AB"},
		{name: "missing then keyed compatibility then stable", events: sseEvent(t, "missing", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "delta": "A"}) + sseEvent(t, "keyed", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_1", "delta": "B"}) + sseEvent(t, "stable", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_a", "partID": "part_2", "field": "text", "delta": "duplicate"}), want: "AB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) + test.events + sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID})
			sink := &recordingSink{}
			if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
				t.Fatal(err)
			}
			if sink.text() != test.want {
				t.Fatalf("text=%q want=%q", sink.text(), test.want)
			}
		})
	}
}

func TestConsumeEventsEmptyStableSnapshotDoesNotSelectTextFamily(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) +
		sseEvent(t, "empty", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_1", "messageID": "msg_a", "sessionID": sessionID, "type": "text", "text": ""}}) +
		sseEvent(t, "compat", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_a", "textID": "part_1", "delta": "compatibility"}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID})
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.text() != "compatibility" {
		t.Fatalf("empty stable snapshot selected the stable family: %q", sink.text())
	}
}

func TestConsumeEventsRejectsOversizedAndNULIdentities(t *testing.T) {
	for _, test := range []struct {
		name       string
		eventType  string
		properties map[string]any
	}{
		{name: "message oversized", eventType: "message.updated", properties: map[string]any{"info": map[string]any{"id": strings.Repeat("m", 513), "sessionID": "ses_stream", "role": "assistant", "parentID": "msg_prompt"}}},
		{name: "part NUL", eventType: "message.part.delta", properties: map[string]any{"sessionID": "ses_stream", "messageID": "msg_a", "partID": "part\x00evil", "field": "text", "delta": "x"}},
		{name: "text oversized", eventType: "session.next.text.delta", properties: map[string]any{"sessionID": "ses_stream", "assistantMessageID": "msg_a", "textID": strings.Repeat("t", 513), "delta": "x"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prefix := ""
			if test.eventType != "message.updated" {
				prefix = sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_a", "sessionID": "ses_stream", "role": "assistant", "parentID": "msg_prompt"}})
			}
			err := consumeEvents(context.Background(), strings.NewReader(prefix+sseEvent(t, "bad", test.eventType, test.properties)), "ses_stream", "msg_prompt", armed(), &recordingSink{})
			var adapterError *agent.AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
				t.Fatalf("identity error=%v", err)
			}
		})
	}
}

func TestConsumeEventsFiltersUserSessionAndCrossFamilyDuplicates(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := strings.Join([]string{
		sseEvent(t, "stale-idle", "session.idle", map[string]any{"sessionID": sessionID}),
		sseEvent(t, "foreign", "session.next.text.delta", map[string]any{"sessionID": "ses_foreign", "assistantMessageID": "msg_foreign", "delta": "foreign"}),
		sseEvent(t, "stale-assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_old", "sessionID": sessionID, "role": "assistant", "parentID": "msg_old_prompt"}}),
		sseEvent(t, "stale-delta", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_old", "partID": "part_old", "field": "text", "delta": "stale"}),
		sseEvent(t, "stale-terminal", "session.idle", map[string]any{"sessionID": sessionID}),
		sseEvent(t, "u1", "message.updated", map[string]any{"info": map[string]any{"id": promptID, "sessionID": sessionID, "role": "user"}}),
		sseEvent(t, "u2", "message.part.updated", map[string]any{"part": map[string]any{"id": "part_user", "messageID": promptID, "sessionID": sessionID, "type": "text", "text": "secret prompt"}}),
		sseEvent(t, "a1", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}),
		sseEvent(t, "a2", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_assistant", "partID": "part_assistant", "field": "text", "delta": "same same"}),
		sseEvent(t, "compat", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_assistant", "textID": "part_assistant", "delta": "same same"}),
		sseEvent(t, "unknown", "future.event", map[string]any{"sessionID": sessionID, "value": "ignored"}),
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID}),
	}, "")
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if got := sink.text(); got != "same same" {
		t.Fatalf("filtered text=%q", got)
	}
}

func TestConsumeEventsAdoptsOpenCodeGeneratedPromptMessageID(t *testing.T) {
	const sessionID = "ses_stream"
	const generatedPromptID = "msg_019c_generated"
	stream := strings.Join([]string{
		sseEvent(t, "stale-assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_stale", "sessionID": sessionID, "role": "assistant", "parentID": "msg_old_user"}}),
		sseEvent(t, "stale-delta", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_stale", "partID": "part_stale", "field": "text", "delta": "stale"}),
		sseEvent(t, "stale-idle", "session.idle", map[string]any{"sessionID": sessionID}),
		sseEvent(t, "generated-user", "message.updated", map[string]any{"info": map[string]any{"id": generatedPromptID, "sessionID": sessionID, "role": "user"}}),
		sseEvent(t, "current-assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_019c_assistant", "sessionID": sessionID, "role": "assistant", "parentID": generatedPromptID, "finish": "stop"}}),
		sseEvent(t, "current-delta", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_019c_assistant", "partID": "part_current", "field": "text", "delta": "current"}),
		sseEvent(t, "current-idle", "session.idle", map[string]any{"sessionID": sessionID}),
	}, "")
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, "", armed(), sink); err != nil {
		t.Fatal(err)
	}
	if got := sink.text(); got != "current" {
		t.Fatalf("provider-generated correlation emitted %q", got)
	}
}

func TestConsumeEventsStatusIdleIsNotTerminalAndCompatibilityCompletes(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "status", "session.status", map[string]any{"sessionID": sessionID, "status": map[string]any{"type": "idle"}}) +
		sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) +
		sseEvent(t, "delta", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_assistant", "textID": "part_assistant", "delta": "hello"}) +
		sseEvent(t, "done", "session.idle", map[string]any{"sessionID": sessionID})
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.text() != "hello" || len(sink.states) != 1 || sink.states[0] != "idle" {
		t.Fatalf("text=%q states=%v", sink.text(), sink.states)
	}
}

func TestConsumeEventsStatusIdleAfterCurrentAssistantIsTerminal(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "busy", "session.status", map[string]any{"sessionID": sessionID, "status": map[string]any{"type": "busy"}}) +
		sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}) +
		sseEvent(t, "delta", "session.next.text.delta", map[string]any{"sessionID": sessionID, "assistantMessageID": "msg_assistant", "textID": "part_assistant", "delta": "done"}) +
		sseEvent(t, "idle-status", "session.status", map[string]any{"sessionID": sessionID, "status": map[string]any{"type": "idle"}})
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.text() != "done" || len(sink.states) != 2 || sink.states[0] != "busy" || sink.states[1] != "idle" {
		t.Fatalf("text=%q states=%v", sink.text(), sink.states)
	}
}

func TestConsumeEventsPlaceholderIdleThenProviderErrorNeverSucceeds(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant-placeholder", "message.updated", map[string]any{"info": map[string]any{
		"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID,
	}}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID}) +
		sseEvent(t, "provider-error", "session.error", map[string]any{
			"sessionID": sessionID, "error": map[string]any{"name": "UnknownError"},
		})
	err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "provider_unavailable" {
		t.Fatalf("placeholder idle masked provider failure: %v", err)
	}
}

func TestConsumeEventsIdleWaitsForExplicitFinalAssistantFinish(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant-placeholder", "message.updated", map[string]any{"info": map[string]any{
		"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID,
	}}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID}) +
		sseEvent(t, "assistant-final", "message.updated", map[string]any{"info": map[string]any{
			"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop",
		}})
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), &recordingSink{}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumeEventsToolFinishDoesNotMasqueradeAsFinalAnswer(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant-tools", "message.updated", map[string]any{"info": map[string]any{
		"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "tool-calls",
	}}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID}) +
		sseEvent(t, "assistant-final", "message.updated", map[string]any{"info": map[string]any{
			"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop",
		}})
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), &recordingSink{}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumeEventsSeparatesReasoningFromAssistantText(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{
		"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID,
	}}) +
		sseEvent(t, "reasoning", "message.part.updated", map[string]any{"part": map[string]any{
			"id": "part_reasoning", "messageID": "msg_assistant", "sessionID": sessionID, "type": "reasoning", "text": "private analysis",
		}}) +
		sseEvent(t, "text", "message.part.updated", map[string]any{"part": map[string]any{
			"id": "part_text", "messageID": "msg_assistant", "sessionID": sessionID, "type": "text", "text": "public answer",
		}}) +
		sseEvent(t, "assistant-final", "message.updated", map[string]any{"info": map[string]any{
			"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop",
		}}) +
		sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": sessionID})
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.thought() != "private analysis" || sink.text() != "public answer" {
		t.Fatalf("reasoning=%q text=%q", sink.thought(), sink.text())
	}
}

func TestConsumeEventsUserEchoDoesNotAuthorizeStaleIdle(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	stream := sseEvent(t, "user", "message.updated", map[string]any{"info": map[string]any{"id": promptID, "sessionID": sessionID, "role": "user"}}) +
		sseEvent(t, "stale-idle", "session.idle", map[string]any{"sessionID": sessionID})
	err := consumeEvents(context.Background(), strings.NewReader(stream), sessionID, promptID, armed(), &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
		t.Fatalf("user echo authorized stale idle: %v", err)
	}
}

func TestConsumeEventsRejectsIdentityMutation(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{name: "role", event: sseEvent(t, "mutate", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_stream", "role": "user", "parentID": "msg_prompt"}})},
		{name: "parent", event: sseEvent(t, "mutate", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_stream", "role": "assistant", "parentID": "msg_other"}})},
		{name: "session", event: sseEvent(t, "mutate", "message.part.updated", map[string]any{"sessionID": "ses_stream", "part": map[string]any{"id": "part", "messageID": "msg_assistant", "sessionID": "ses_other", "type": "text", "text": "bad"}})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_stream", "role": "assistant", "parentID": "msg_prompt"}}) + test.event
			err := consumeEvents(context.Background(), strings.NewReader(stream), "ses_stream", "msg_prompt", armed(), &recordingSink{})
			var adapterError *agent.AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
				t.Fatalf("identity mutation=%v", err)
			}
		})
	}
}

func TestConsumeEventsBoundariesAndErrors(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		code   string
	}{
		{name: "malformed", stream: "data: {bad}\n\n", code: "protocol_error"},
		{name: "oversized", stream: "data: " + strings.Repeat("x", maximumSSERecordBytes+1) + "\n\n", code: "protocol_error"},
		{name: "premature eof", stream: sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_stream", "role": "assistant", "parentID": "msg_prompt"}}), code: "protocol_error"},
		{name: "rate error", stream: sseEvent(t, "user", "message.updated", map[string]any{"info": map[string]any{"id": "msg_prompt", "sessionID": "ses_stream", "role": "user"}}) + sseEvent(t, "error", "session.error", map[string]any{"sessionID": "ses_stream", "error": map[string]any{"name": "RateLimitError", "status": 429}}), code: "rate_limited"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := consumeEvents(context.Background(), strings.NewReader(test.stream), "ses_stream", "msg_prompt", armed(), &recordingSink{})
			var adapterError *agent.AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != test.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEventTrackerPendingHasGlobalRecordAndByteBounds(t *testing.T) {
	tracker := &eventTracker{pending: make(map[string][]pendingText)}
	for index := 0; index < maximumPendingParts; index++ {
		if err := tracker.queue(fmt.Sprintf("msg_%d", index), pendingText{partID: "part", delta: "x"}); err != nil {
			t.Fatalf("queue %d: %v", index, err)
		}
	}
	if err := tracker.queue("overflow", pendingText{partID: "part", delta: "x"}); err == nil {
		t.Fatal("global pending record cap was not enforced")
	}
	tracker = &eventTracker{pending: make(map[string][]pendingText)}
	if err := tracker.queue("large", pendingText{partID: "part", delta: strings.Repeat("x", agent.MaxMessageBytes)}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.queue("overflow", pendingText{partID: "part", delta: "x"}); err == nil {
		t.Fatal("global pending byte cap was not enforced")
	}
}

func TestConsumeEventsNeverForgetsAnOutputAffectingEventID(t *testing.T) {
	const sessionID = "ses_stream"
	const promptID = "msg_prompt"
	var stream strings.Builder
	stream.WriteString(sseEvent(t, "event-0", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": sessionID, "role": "assistant", "parentID": promptID, "finish": "stop"}}))
	delta := sseEvent(t, "event-1", "message.part.delta", map[string]any{"sessionID": sessionID, "messageID": "msg_assistant", "partID": "part", "field": "text", "delta": "x"})
	stream.WriteString(delta)
	for index := 2; index < maximumEventIDs-1; index++ {
		stream.WriteString(sseEvent(t, fmt.Sprintf("event-%d", index), "future.event", map[string]any{"sessionID": sessionID}))
	}
	stream.WriteString(delta)
	stream.WriteString(sseEvent(t, "event-terminal", "session.idle", map[string]any{"sessionID": sessionID}))
	sink := &recordingSink{}
	if err := consumeEvents(context.Background(), strings.NewReader(stream.String()), sessionID, promptID, armed(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.text() != "x" {
		t.Fatalf("replayed oldest delta was emitted again: %q", sink.text())
	}

	stream.Reset()
	for index := 0; index <= maximumEventIDs; index++ {
		stream.WriteString(sseEvent(t, fmt.Sprintf("unique-%d", index), "future.event", map[string]any{"sessionID": sessionID}))
	}
	err := consumeEvents(context.Background(), strings.NewReader(stream.String()), sessionID, promptID, armed(), &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
		t.Fatalf("unique event overflow=%v", err)
	}
}

func TestWaitForTurnPrefersOriginalFatalOverCancellation(t *testing.T) {
	sentinel := errors.New("original remote exec failure")
	failures := make(chan error, 1)
	parser := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	failures <- sentinel
	cancel()
	parser <- context.Canceled
	if err := waitForTurn(ctx, failures, parser); !errors.Is(err, sentinel) {
		t.Fatalf("returned %v instead of original fatal", err)
	}
}
