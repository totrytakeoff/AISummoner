package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const (
	deepSeekSettingsNamespace = "llm-deepseek"
	piAISettingsNamespace     = "llm-pi-ai"
	deepSeekProviderRoute     = "deepseek-official"
	maximumProviderRows       = 256
	maximumCredentialRefs     = 64
	maximumReasoningEfforts   = 16
)

var (
	providerRoutePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	piAIProtocols        = []string{"openai-completions", "openai-responses", "anthropic-messages"}
)

type configurableProviderWire struct {
	Provider     string   `json:"provider"`
	DisplayName  string   `json:"displayName"`
	SettingsNS   string   `json:"settingsNs"`
	SettingsPath []string `json:"settingsPath"`
	Active       bool     `json:"active"`
	Declared     *bool    `json:"declared,omitempty"`
}

type settingsSecretWire struct {
	Path []string `json:"path"`
	Set  bool     `json:"set"`
}

type settingsNamespaceWire struct {
	NS       string               `json:"ns"`
	Schema   json.RawMessage      `json:"schema"`
	Value    json.RawMessage      `json:"value"`
	Base     json.RawMessage      `json:"base,omitempty"`
	User     json.RawMessage      `json:"user,omitempty"`
	Applies  string               `json:"applies"`
	Secrets  []settingsSecretWire `json:"secrets"`
	Revision int64                `json:"revision"`
}

type credentialViewWire struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Writable   bool   `json:"writable"`
}

type modelSelectionWire struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type modelReasoningEffortWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type modelReasoningWire struct {
	Efforts       []modelReasoningEffortWire `json:"efforts"`
	DefaultEffort string                     `json:"defaultEffort,omitempty"`
}

type modelCatalogModelWire struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Reasoning   *modelReasoningWire `json:"reasoning,omitempty"`
}

type modelProviderGroupWire struct {
	ID     string                  `json:"id"`
	Name   string                  `json:"name"`
	Models []modelCatalogModelWire `json:"models"`
}

type modelCatalogFailureWire struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type providerModelSettingsWire struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int64  `json:"contextWindow,omitempty"`
	MaxTokens     int64  `json:"maxTokens,omitempty"`
}

type providerRecord struct {
	profile       agent.RuntimeProviderProfile
	credentialRef string
	settingsNS    string
	settingsPath  []string
}

type configurationSnapshot struct {
	directory  agent.RuntimeProviderDirectory
	providers  map[string]providerRecord
	namespaces map[string]settingsNamespaceWire
}

// ProviderDirectory projects DSH's configurable-provider directory, layered
// settings, and value-free credential metadata. DSH remains the only storage
// and validation authority for every returned fact.
func (adapter *Adapter) ProviderDirectory(ctx context.Context) (agent.RuntimeProviderDirectory, error) {
	snapshot, err := adapter.configurationSnapshot(ctx)
	if err != nil {
		return agent.RuntimeProviderDirectory{}, err
	}
	return snapshot.directory, nil
}

func (adapter *Adapter) configurationSnapshot(ctx context.Context) (configurationSnapshot, error) {
	var providerValue struct {
		Providers []configurableProviderWire `json:"providers"`
	}
	if err := adapter.callBounded(ctx, "llm.providers", struct{}{}, &providerValue, maximumCatalogRPCResponseBytes); err != nil {
		return configurationSnapshot{}, err
	}
	if len(providerValue.Providers) > maximumProviderRows {
		return configurationSnapshot{}, protocolError("DSH provider directory exceeds the safe limit")
	}

	var settingsValue struct {
		Writable    bool                    `json:"writable"`
		HasDocument bool                    `json:"hasDocument"`
		Namespaces  []settingsNamespaceWire `json:"namespaces"`
	}
	if err := adapter.callBounded(ctx, "settings.describe", struct{}{}, &settingsValue, maximumConfigurationRPCResponseBytes); err != nil {
		return configurationSnapshot{}, err
	}
	namespaces := make(map[string]settingsNamespaceWire, 2)
	for _, namespace := range settingsValue.Namespaces {
		if namespace.NS != deepSeekSettingsNamespace && namespace.NS != piAISettingsNamespace {
			continue
		}
		if namespace.Revision < 0 || (namespace.Applies != "live" && namespace.Applies != "restart") {
			return configurationSnapshot{}, protocolError("DSH settings description is invalid")
		}
		namespaces[namespace.NS] = namespace
	}

	directory := agent.RuntimeProviderDirectory{
		Runtime:     ProviderName,
		DisplayName: "DeepSeek Harness",
		Writable:    settingsValue.Writable,
		Protocols:   append([]string(nil), piAIProtocols...),
		Providers:   make([]agent.RuntimeProviderProfile, 0, len(providerValue.Providers)),
	}
	if namespace, ok := namespaces[piAISettingsNamespace]; ok {
		directory.CustomProviderRevision = namespace.Revision
	} else {
		directory.Writable = false
	}

	records := make(map[string]providerRecord)
	providerOrder := make([]string, 0, len(providerValue.Providers))
	refs := make([]string, 0)
	refSeen := make(map[string]struct{})
	for _, row := range providerValue.Providers {
		if row.SettingsNS != deepSeekSettingsNamespace && row.SettingsNS != piAISettingsNamespace {
			continue
		}
		if !validProviderText(row.Provider, agent.MaxProviderIDBytes) ||
			!validProviderText(row.DisplayName, agent.MaxDisplayNameBytes) || len(row.SettingsPath) > 4 {
			return configurationSnapshot{}, protocolError("DSH provider directory is invalid")
		}
		namespace, ok := namespaces[row.SettingsNS]
		if !ok {
			continue
		}
		if !validSettingsAddress(row) {
			return configurationSnapshot{}, protocolError("DSH provider settings address is invalid")
		}
		effective, configured := rawAt(namespace.Value, row.SettingsPath)
		_, userOwnsProfile := rawAt(namespace.User, row.SettingsPath)
		_, baseOwnsProfile := rawAt(namespace.Base, row.SettingsPath)
		if row.SettingsNS == deepSeekSettingsNamespace {
			configured = true
		}
		profile := agent.RuntimeProviderProfile{
			ID:          row.Provider,
			DisplayName: row.DisplayName,
			Family:      row.SettingsNS,
			Active:      row.Active,
			Configured:  configured,
			Custom:      row.Declared != nil && *row.Declared,
			Removable:   len(row.SettingsPath) > 0 && userOwnsProfile && !baseOwnsProfile,
			Revision:    namespace.Revision,
		}
		if configured {
			profile.BaseURL, _ = rawStringAt(effective, []string{"baseURL"})
			profile.API, _ = rawStringAt(effective, []string{"api"})
			if displayName, ok := rawStringAt(effective, []string{"displayName"}); ok && displayName != "" {
				profile.DisplayName = displayName
			}
			profile.Models = providerModelsAt(effective)
			_, profile.ModelsOverridden = rawAt(namespace.User, appendPath(row.SettingsPath, "models"))
		}
		credentialRef, _ := rawStringAt(effective, []string{"apiKeyEnv"})
		if row.SettingsNS == deepSeekSettingsNamespace && credentialRef == "" {
			credentialRef = CredentialReference
		}
		if credentialRef != "" {
			if !validCredentialReference(credentialRef) {
				return configurationSnapshot{}, protocolError("DSH provider credential reference is invalid")
			}
			if _, exists := refSeen[credentialRef]; !exists {
				refSeen[credentialRef] = struct{}{}
				refs = append(refs, credentialRef)
			}
		}
		if _, duplicate := records[row.Provider]; duplicate {
			return configurationSnapshot{}, protocolError("DSH provider directory contains a duplicate route")
		}
		records[row.Provider] = providerRecord{
			profile: profile, credentialRef: credentialRef,
			settingsNS: row.SettingsNS, settingsPath: append([]string(nil), row.SettingsPath...),
		}
		providerOrder = append(providerOrder, row.Provider)
	}

	credentials, err := adapter.describeCredentials(ctx, refs)
	if err != nil {
		return configurationSnapshot{}, err
	}
	for _, providerID := range providerOrder {
		record := records[providerID]
		if record.credentialRef != "" {
			view, ok := credentials[record.credentialRef]
			if !ok {
				return configurationSnapshot{}, protocolError("DSH credential description is incomplete")
			}
			status := agent.CredentialStatus{Configured: view.Configured, Writable: view.Writable}
			record.profile.Credential = &status
			records[providerID] = record
		}
		directory.Providers = append(directory.Providers, record.profile)
	}
	return configurationSnapshot{directory: directory, providers: records, namespaces: namespaces}, nil
}

func (adapter *Adapter) describeCredentials(ctx context.Context, refs []string) (map[string]credentialViewWire, error) {
	views := make(map[string]credentialViewWire, len(refs))
	for offset := 0; offset < len(refs); offset += maximumCredentialRefs {
		end := offset + maximumCredentialRefs
		if end > len(refs) {
			end = len(refs)
		}
		var value struct {
			Credentials map[string]credentialViewWire `json:"credentials"`
		}
		if err := adapter.call(ctx, "credentials.describe", struct {
			Refs []string `json:"refs"`
		}{Refs: refs[offset:end]}, &value); err != nil {
			return nil, err
		}
		for ref, view := range value.Credentials {
			if _, requested := refSeenIn(refs[offset:end], ref); !requested {
				return nil, protocolError("DSH credential description contains an unexpected reference")
			}
			views[ref] = view
		}
	}
	return views, nil
}

func refSeenIn(refs []string, target string) (int, bool) {
	for index, ref := range refs {
		if ref == target {
			return index, true
		}
	}
	return -1, false
}

// ConfigureProvider applies only the curated DSH provider fields and stores a
// credential through DSH's write-only credential seam. Unrelated settings
// fields remain untouched because every existing edit is path-addressed.
func (adapter *Adapter) ConfigureProvider(ctx context.Context, mutation agent.RuntimeProviderMutation) error {
	if err := validateProviderMutation(mutation); err != nil {
		return err
	}
	snapshot, err := adapter.configurationSnapshot(ctx)
	if err != nil {
		return err
	}
	record, exists := snapshot.providers[mutation.Provider]
	if !exists {
		return adapter.createCustomProvider(ctx, snapshot, mutation)
	}
	if mutation.ExpectedRevision != record.profile.Revision || !snapshot.directory.Writable {
		return &agent.AdapterError{Code: "configuration_conflict", Err: errors.New("DSH provider configuration changed")}
	}
	if record.settingsNS == deepSeekSettingsNamespace &&
		(mutation.DisplayName != "" && mutation.DisplayName != record.profile.DisplayName || mutation.API != "") {
		return agent.ErrInvalidRequest
	}
	if record.settingsNS == piAISettingsNamespace && !record.profile.Custom &&
		(mutation.DisplayName != "" && mutation.DisplayName != record.profile.DisplayName || mutation.API != "" && mutation.API != record.profile.API) {
		return agent.ErrInvalidRequest
	}

	namespace := snapshot.namespaces[record.settingsNS]
	ops := make([]settingsPathOperation, 0, 6)
	userProfile, _ := rawAt(namespace.User, record.settingsPath)
	effectiveProfile, configured := rawAt(namespace.Value, record.settingsPath)
	if record.settingsNS == deepSeekSettingsNamespace {
		configured = true
	}
	ops = appendStringOverride(ops, record.settingsPath, userProfile, effectiveProfile, "baseURL", mutation.BaseURL)
	if record.settingsNS == piAISettingsNamespace && record.profile.Custom {
		ops = appendStringOverride(ops, record.settingsPath, userProfile, effectiveProfile, "displayName", mutation.DisplayName)
		ops = appendStringOverride(ops, record.settingsPath, userProfile, effectiveProfile, "api", mutation.API)
	}
	modelsPath := appendPath(record.settingsPath, "models")
	if mutation.ModelsOverridden {
		models := providerModelSettingsValues(mutation.Models)
		if !rawEqualValueAt(namespace.User, modelsPath, models) {
			ops = append(ops, settingsPathOperation{Op: "set", Path: modelsPath, Value: models})
		}
	} else if _, ok := rawAt(namespace.User, modelsPath); ok {
		ops = append(ops, settingsPathOperation{Op: "unset", Path: modelsPath})
	}

	credentialRef := record.credentialRef
	if mutation.APIKey != "" && credentialRef == "" {
		credentialRef = deriveCredentialReference(mutation.Provider)
		ops = append(ops, settingsPathOperation{Op: "set", Path: appendPath(record.settingsPath, "apiKeyEnv"), Value: credentialRef})
	}
	if !configured && len(ops) == 0 {
		ops = append(ops, settingsPathOperation{Op: "set", Path: record.settingsPath, Value: map[string]any{}})
	}
	if len(ops) > 0 {
		if err := adapter.mutateSettings(ctx, record.settingsNS, mutation.ExpectedRevision, ops); err != nil {
			return err
		}
	}
	if mutation.APIKey != "" {
		if credentialRef == "" {
			return protocolError("DSH provider credential route is invalid")
		}
		if err := adapter.setCredential(ctx, credentialRef, mutation.APIKey); err != nil {
			return err
		}
	}
	return nil
}

func (adapter *Adapter) createCustomProvider(ctx context.Context, snapshot configurationSnapshot, mutation agent.RuntimeProviderMutation) error {
	namespace, ok := snapshot.namespaces[piAISettingsNamespace]
	if !ok || !snapshot.directory.Writable || mutation.ExpectedRevision != namespace.Revision ||
		mutation.BaseURL == "" || mutation.API == "" || !containsString(piAIProtocols, mutation.API) ||
		!mutation.ModelsOverridden || len(mutation.Models) == 0 {
		return agent.ErrInvalidRequest
	}
	profile := map[string]any{
		"api": mutation.API, "baseURL": mutation.BaseURL, "models": providerModelSettingsValues(mutation.Models),
	}
	if mutation.DisplayName != "" {
		profile["displayName"] = mutation.DisplayName
	}
	credentialRef := ""
	if mutation.APIKey != "" {
		credentialRef = deriveCredentialReference(mutation.Provider)
		profile["apiKeyEnv"] = credentialRef
	}
	if err := adapter.mutateSettings(ctx, piAISettingsNamespace, mutation.ExpectedRevision, []settingsPathOperation{{
		Op: "set", Path: []string{"providers", mutation.Provider}, Value: profile,
	}}); err != nil {
		return err
	}
	if mutation.APIKey != "" {
		return adapter.setCredential(ctx, credentialRef, mutation.APIKey)
	}
	return nil
}

// RemoveProvider removes only a user-owned custom route. The revision-checked
// settings mutation commits first, so a conflict cannot strand a still-active
// route without its credential. A conventionally derived managed credential
// is then cleared; environment/custom references are deliberately retained.
func (adapter *Adapter) RemoveProvider(ctx context.Context, provider string, expectedRevision int64) error {
	if !providerRoutePattern.MatchString(provider) || expectedRevision < 0 {
		return agent.ErrInvalidRequest
	}
	snapshot, err := adapter.configurationSnapshot(ctx)
	if err != nil {
		return err
	}
	record, ok := snapshot.providers[provider]
	if !ok {
		return agent.ErrNotFound
	}
	if !record.profile.Removable || !record.profile.Custom || expectedRevision != record.profile.Revision || !snapshot.directory.Writable {
		return &agent.AdapterError{Code: "configuration_conflict", Err: errors.New("DSH provider cannot be removed from this view")}
	}
	derivedRef := deriveCredentialReference(provider)
	if err := adapter.mutateSettings(ctx, record.settingsNS, expectedRevision, []settingsPathOperation{{
		Op: "unset", Path: record.settingsPath,
	}}); err != nil {
		return err
	}
	if record.credentialRef == derivedRef && record.profile.Credential != nil &&
		record.profile.Credential.Configured && record.profile.Credential.Writable {
		return adapter.call(ctx, "credentials.unset", struct {
			Ref string `json:"ref"`
		}{Ref: derivedRef}, nil)
	}
	return nil
}

type settingsPathOperation struct {
	Op    string   `json:"op"`
	Path  []string `json:"path"`
	Value any      `json:"value,omitempty"`
}

func (adapter *Adapter) mutateSettings(ctx context.Context, namespace string, expectedRevision int64, ops []settingsPathOperation) error {
	if len(ops) == 0 {
		return nil
	}
	expected := expectedRevision
	var value settingsNamespaceWire
	return adapter.callBounded(ctx, "settings.mutate", struct {
		NS               string                  `json:"ns"`
		Ops              []settingsPathOperation `json:"ops"`
		ExpectedRevision *int64                  `json:"expectedRevision"`
	}{NS: namespace, Ops: ops, ExpectedRevision: &expected}, &value, maximumConfigurationRPCResponseBytes)
}

func (adapter *Adapter) setCredential(ctx context.Context, ref, value string) error {
	if !validCredentialReference(ref) || !validCredential(value) {
		return agent.ErrInvalidRequest
	}
	return adapter.call(ctx, "credentials.set", struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}{Ref: ref, Value: value}, nil)
}

// Models returns DSH's native per-Session selection and advisory catalog. The
// selected route's managed credential status is joined without exposing its
// reference or value.
func (adapter *Adapter) Models(ctx context.Context, externalID string) (agent.ModelDirectory, error) {
	if !validExternalSessionID(externalID) {
		return agent.ModelDirectory{}, agent.ErrInvalidRequest
	}
	var value struct {
		Current  modelSelectionWire        `json:"current"`
		Routable bool                      `json:"routable"`
		Groups   []modelProviderGroupWire  `json:"groups"`
		Failures []modelCatalogFailureWire `json:"failures"`
	}
	if err := adapter.callBounded(ctx, "session.models", struct {
		SessionID string `json:"sessionId"`
	}{SessionID: externalID}, &value, maximumCatalogRPCResponseBytes); err != nil {
		return agent.ModelDirectory{}, err
	}
	directory, err := normalizeModelDirectory(value.Current, value.Routable, value.Groups, value.Failures)
	if err != nil {
		return agent.ModelDirectory{}, err
	}
	configuration, err := adapter.configurationSnapshot(ctx)
	if err != nil {
		return agent.ModelDirectory{}, err
	}
	if record, ok := configuration.providers[directory.Current.Provider]; ok && record.profile.Credential != nil {
		status := *record.profile.Credential
		directory.CurrentCredential = &status
	}
	return directory, nil
}

// SelectModel delegates the complete provider/model/reasoning selection to
// DSH, which validates it and applies it to the next assembled step.
func (adapter *Adapter) SelectModel(ctx context.Context, externalID string, selection agent.ModelSelection) (agent.ModelSelection, error) {
	if !validExternalSessionID(externalID) || !validModelSelection(selection) {
		return agent.ModelSelection{}, agent.ErrInvalidRequest
	}
	var value struct {
		Selected modelSelectionWire `json:"selected"`
	}
	payload := struct {
		SessionID       string `json:"sessionId"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort,omitempty"`
	}{SessionID: externalID, Provider: selection.Provider, Model: selection.Model, ReasoningEffort: selection.ReasoningEffort}
	if err := adapter.call(ctx, "session.selectModel", payload, &value); err != nil {
		return agent.ModelSelection{}, err
	}
	selected := agent.ModelSelection{
		Provider: value.Selected.Provider, Model: value.Selected.Model,
		ReasoningEffort: value.Selected.ReasoningEffort,
	}
	if !validModelSelection(selected) || selected.Provider != selection.Provider || selected.Model != selection.Model ||
		(selection.ReasoningEffort != "" && selected.ReasoningEffort != selection.ReasoningEffort) {
		return agent.ModelSelection{}, protocolError("DSH selected an unexpected model")
	}
	return selected, nil
}

func normalizeModelDirectory(current modelSelectionWire, routable bool, groups []modelProviderGroupWire, failures []modelCatalogFailureWire) (agent.ModelDirectory, error) {
	selection := agent.ModelSelection{Provider: current.Provider, Model: current.Model, ReasoningEffort: current.ReasoningEffort}
	if !validModelSelection(selection) || len(groups) > maximumProviderRows || len(failures) > maximumProviderRows {
		return agent.ModelDirectory{}, protocolError("DSH model directory is invalid")
	}
	directory := agent.ModelDirectory{
		Current: selection, Routable: routable,
		Groups:   make([]agent.ModelProviderGroup, 0, len(groups)),
		Failures: make([]agent.ModelCatalogFailure, 0, len(failures)),
	}
	providerSeen := make(map[string]struct{})
	for _, group := range groups {
		if !validProviderText(group.ID, agent.MaxProviderIDBytes) || !validProviderText(group.Name, agent.MaxDisplayNameBytes) ||
			len(group.Models) > agent.MaxProviderModels {
			return agent.ModelDirectory{}, protocolError("DSH model group is invalid")
		}
		if _, exists := providerSeen[group.ID]; exists {
			return agent.ModelDirectory{}, protocolError("DSH model provider is duplicated")
		}
		providerSeen[group.ID] = struct{}{}
		normalized := agent.ModelProviderGroup{ID: group.ID, Name: group.Name, Models: make([]agent.RuntimeModel, 0, len(group.Models))}
		modelSeen := make(map[string]struct{})
		for _, model := range group.Models {
			if !validProviderText(model.ID, agent.MaxModelIDBytes) || !validProviderText(model.Name, agent.MaxDisplayNameBytes) ||
				!validOptionalText(model.Description, agent.MaxDisplayNameBytes) {
				return agent.ModelDirectory{}, protocolError("DSH model entry is invalid")
			}
			if _, exists := modelSeen[model.ID]; exists {
				return agent.ModelDirectory{}, protocolError("DSH model entry is duplicated")
			}
			modelSeen[model.ID] = struct{}{}
			entry := agent.RuntimeModel{ID: model.ID, Name: model.Name, Description: model.Description}
			if model.Reasoning != nil {
				if len(model.Reasoning.Efforts) == 0 || len(model.Reasoning.Efforts) > maximumReasoningEfforts {
					return agent.ModelDirectory{}, protocolError("DSH reasoning directory is invalid")
				}
				effortSeen := make(map[string]struct{})
				for _, effort := range model.Reasoning.Efforts {
					if !validProviderText(effort.ID, agent.MaxReasoningIDBytes) || !validProviderText(effort.Name, agent.MaxDisplayNameBytes) ||
						!validOptionalText(effort.Description, agent.MaxDisplayNameBytes) {
						return agent.ModelDirectory{}, protocolError("DSH reasoning effort is invalid")
					}
					if _, exists := effortSeen[effort.ID]; exists {
						return agent.ModelDirectory{}, protocolError("DSH reasoning effort is duplicated")
					}
					effortSeen[effort.ID] = struct{}{}
					entry.ReasoningEfforts = append(entry.ReasoningEfforts, agent.ModelReasoningEffort{
						ID: effort.ID, Name: effort.Name, Description: effort.Description,
					})
				}
				if model.Reasoning.DefaultEffort != "" {
					if _, ok := effortSeen[model.Reasoning.DefaultEffort]; !ok {
						return agent.ModelDirectory{}, protocolError("DSH default reasoning effort is invalid")
					}
					entry.DefaultReasoningEffort = model.Reasoning.DefaultEffort
				}
			}
			normalized.Models = append(normalized.Models, entry)
		}
		directory.Groups = append(directory.Groups, normalized)
	}
	for _, failure := range failures {
		if !validProviderText(failure.ID, agent.MaxProviderIDBytes) || !validProviderText(failure.Name, agent.MaxDisplayNameBytes) ||
			!validOptionalText(failure.Message, agent.MaxDisplayNameBytes) {
			return agent.ModelDirectory{}, protocolError("DSH model failure is invalid")
		}
		directory.Failures = append(directory.Failures, agent.ModelCatalogFailure{ID: failure.ID, Name: failure.Name, Message: failure.Message})
	}
	return directory, nil
}

func validateProviderMutation(mutation agent.RuntimeProviderMutation) error {
	if !providerRoutePattern.MatchString(mutation.Provider) || len(mutation.Provider) > agent.MaxProviderIDBytes ||
		mutation.ExpectedRevision < 0 || !validOptionalText(mutation.DisplayName, agent.MaxDisplayNameBytes) ||
		!validOptionalText(mutation.BaseURL, agent.MaxBaseURLBytes) || !validOptionalText(mutation.API, agent.MaxReasoningIDBytes) ||
		(mutation.APIKey != "" && !validCredential(mutation.APIKey)) || len(mutation.Models) > agent.MaxProviderModels {
		return agent.ErrInvalidRequest
	}
	if mutation.BaseURL != "" && !validProviderBaseURL(mutation.BaseURL) {
		return agent.ErrInvalidRequest
	}
	if mutation.ModelsOverridden {
		seen := make(map[string]struct{})
		for _, model := range mutation.Models {
			if !validProviderText(model.ID, agent.MaxModelIDBytes) || !validOptionalText(model.Name, agent.MaxDisplayNameBytes) ||
				model.ContextWindow < 0 || model.MaxTokens < 0 ||
				(model.ContextWindow != 0 && model.ContextWindow < 1) || (model.MaxTokens != 0 && model.MaxTokens < 1) {
				return agent.ErrInvalidRequest
			}
			if _, exists := seen[model.ID]; exists {
				return agent.ErrInvalidRequest
			}
			seen[model.ID] = struct{}{}
		}
	} else if len(mutation.Models) != 0 {
		return agent.ErrInvalidRequest
	}
	return nil
}

func validProviderBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		ip := net.ParseIP(parsed.Hostname())
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

func validModelSelection(value agent.ModelSelection) bool {
	return validProviderText(value.Provider, agent.MaxProviderIDBytes) && validProviderText(value.Model, agent.MaxModelIDBytes) &&
		validOptionalText(value.ReasoningEffort, agent.MaxReasoningIDBytes)
}

func validProviderText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value
}

func validOptionalText(value string, maximum int) bool {
	return value == "" || validProviderText(value, maximum)
}

func validCredentialReference(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if index == 0 && !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '_') {
			return false
		}
		if index > 0 && !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func deriveCredentialReference(provider string) string {
	var builder strings.Builder
	for _, character := range strings.ToUpper(provider) {
		if character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
		} else if builder.Len() == 0 || !strings.HasSuffix(builder.String(), "_") {
			builder.WriteByte('_')
		}
	}
	return strings.TrimSuffix(builder.String(), "_") + "_API_KEY"
}

func validSettingsAddress(row configurableProviderWire) bool {
	if row.SettingsNS == deepSeekSettingsNamespace {
		return row.Provider == deepSeekProviderRoute && len(row.SettingsPath) == 0
	}
	return row.SettingsNS == piAISettingsNamespace && len(row.SettingsPath) == 2 &&
		row.SettingsPath[0] == "providers" && row.SettingsPath[1] == row.Provider
}

func rawAt(document json.RawMessage, path []string) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(document)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	current := append(json.RawMessage(nil), trimmed...)
	for _, segment := range path {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false
		}
		next, ok := object[segment]
		if !ok {
			return nil, false
		}
		current = next
	}
	if bytes.Equal(bytes.TrimSpace(current), []byte("null")) {
		return nil, false
	}
	return current, true
}

func rawStringAt(document json.RawMessage, path []string) (string, bool) {
	value, ok := rawAt(document, path)
	if !ok {
		return "", false
	}
	var decoded string
	if json.Unmarshal(value, &decoded) != nil {
		return "", false
	}
	return decoded, true
}

func providerModelsAt(profile json.RawMessage) []agent.RuntimeProviderModel {
	value, ok := rawAt(profile, []string{"models"})
	if !ok {
		return nil
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(value, &rows) != nil || len(rows) > agent.MaxProviderModels {
		return nil
	}
	models := make([]agent.RuntimeProviderModel, 0, len(rows))
	seen := make(map[string]struct{})
	for _, row := range rows {
		var model agent.RuntimeProviderModel
		if json.Unmarshal(row["id"], &model.ID) != nil || !validProviderText(model.ID, agent.MaxModelIDBytes) {
			return nil
		}
		_ = json.Unmarshal(row["name"], &model.Name)
		_ = json.Unmarshal(row["contextWindow"], &model.ContextWindow)
		_ = json.Unmarshal(row["maxTokens"], &model.MaxTokens)
		if !validOptionalText(model.Name, agent.MaxDisplayNameBytes) || model.ContextWindow < 0 || model.MaxTokens < 0 {
			return nil
		}
		if _, exists := seen[model.ID]; exists {
			return nil
		}
		seen[model.ID] = struct{}{}
		models = append(models, model)
	}
	return models
}

func providerModelSettingsValues(models []agent.RuntimeProviderModel) []providerModelSettingsWire {
	values := make([]providerModelSettingsWire, 0, len(models))
	for _, model := range models {
		values = append(values, providerModelSettingsWire{
			ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
		})
	}
	return values
}

func appendPath(base []string, segment string) []string {
	path := make([]string, 0, len(base)+1)
	path = append(path, base...)
	return append(path, segment)
}

func appendStringOverride(ops []settingsPathOperation, base []string, user, effective json.RawMessage, key, desired string) []settingsPathOperation {
	path := appendPath(base, key)
	userValue, userHas := rawStringAt(user, []string{key})
	effectiveValue, _ := rawStringAt(effective, []string{key})
	if desired == "" {
		if userHas {
			return append(ops, settingsPathOperation{Op: "unset", Path: path})
		}
		return ops
	}
	if userHas && userValue == desired || !userHas && effectiveValue == desired {
		return ops
	}
	return append(ops, settingsPathOperation{Op: "set", Path: path, Value: desired})
}

func rawEqualValueAt(document json.RawMessage, path []string, value any) bool {
	current, ok := rawAt(document, path)
	if !ok {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(current), encoded)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
