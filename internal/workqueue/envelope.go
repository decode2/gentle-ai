package workqueue

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const QueueSchemaVersion = "gentle-ai.workqueue/v1"

var (
	ErrInvalidEnvelope   = errors.New("invalid workqueue envelope")
	ErrUnsupportedSchema = errors.New("unsupported workqueue schema")
	ErrGraphMismatch     = errors.New("workqueue graph mismatch")
)

type ItemStatus string

const ItemPending ItemStatus = "pending"

type ItemState struct {
	ID     string     `json:"id"`
	Status ItemStatus `json:"status"`
}

type State struct {
	Schema        string      `json:"schema"`
	GraphRevision string      `json:"graph_revision"`
	Items         []ItemState `json:"items"`
	Revision      string      `json:"revision"`
}

// Canonicalize derives every authority field from graph and copies its item state.
func Canonicalize(graph GraphSnapshot, state State) (State, error) {
	if !validDigest(graph.GraphRevision()) {
		return State{}, ErrGraphMismatch
	}
	items := append([]ItemState(nil), state.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err := validateItems(graph, items); err != nil {
		return State{}, err
	}
	state = State{Schema: QueueSchemaVersion, GraphRevision: graph.GraphRevision(), Items: items}
	state.Revision = stateRevision(state)
	return state, nil
}

func Encode(graph GraphSnapshot, state State) ([]byte, error) {
	state, err := Canonicalize(graph, state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func Decode(graph GraphSnapshot, data []byte) (State, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	values, err := object(dec, "schema", "graph_revision", "items", "revision")
	if err != nil || trailing(dec) {
		return State{}, ErrInvalidEnvelope
	}
	var state State
	if !stringValue(values["schema"], &state.Schema) || !stringValue(values["graph_revision"], &state.GraphRevision) || !stringValue(values["revision"], &state.Revision) || bytes.Equal(values["items"], []byte("null")) || json.Unmarshal(values["items"], &state.Items) != nil {
		return State{}, ErrInvalidEnvelope
	}
	items, err := decodeItems(values["items"])
	if err != nil {
		return State{}, err
	}
	state.Items = items
	if state.Schema != QueueSchemaVersion {
		if state.Schema == "" {
			return State{}, ErrInvalidEnvelope
		}
		return State{}, ErrUnsupportedSchema
	}
	if !validDigest(state.GraphRevision) || !validDigest(state.Revision) {
		return State{}, ErrInvalidEnvelope
	}
	if state.GraphRevision != graph.GraphRevision() {
		return State{}, ErrGraphMismatch
	}
	canonical, err := Canonicalize(graph, state)
	if err != nil {
		return State{}, err
	}
	if !sameItems(state.Items, canonical.Items) || state.Revision != canonical.Revision {
		return State{}, ErrInvalidEnvelope
	}
	return canonical, nil
}

func validateItems(graph GraphSnapshot, items []ItemState) error {
	if len(items) != len(graph.input.Items) {
		return ErrGraphMismatch
	}
	for i, item := range items {
		if !identifier(item.ID, "._-") || item.Status != ItemPending {
			return ErrInvalidEnvelope
		}
		if i > 0 && items[i-1].ID == item.ID {
			return ErrGraphMismatch
		}
	}
	for i, item := range items {
		if item.ID != graph.input.Items[i].ID {
			return ErrGraphMismatch
		}
	}
	return nil
}

func stateRevision(state State) string {
	payload, _ := json.Marshal(struct {
		Schema        string      `json:"schema"`
		GraphRevision string      `json:"graph_revision"`
		Items         []ItemState `json:"items"`
	}{state.Schema, state.GraphRevision, state.Items})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum)
}

func object(dec *json.Decoder, keys ...string) (map[string]json.RawMessage, error) {
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrInvalidEnvelope
	}
	allowed, values := map[string]bool{}, map[string]json.RawMessage{}
	for _, key := range keys {
		allowed[key] = true
	}
	for dec.More() {
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || values[key] != nil {
			return nil, ErrInvalidEnvelope
		}
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return nil, ErrInvalidEnvelope
		}
		values[key] = raw
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalidEnvelope
	}
	for _, key := range keys {
		if values[key] == nil {
			return nil, ErrInvalidEnvelope
		}
	}
	return values, nil
}

func decodeItems(raw []byte) ([]ItemState, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if token, err := dec.Token(); err != nil || token != json.Delim('[') {
		return nil, ErrInvalidEnvelope
	}
	var items []ItemState
	for dec.More() {
		values, err := object(dec, "id", "status")
		if err != nil {
			return nil, err
		}
		var item ItemState
		if !stringValue(values["id"], &item.ID) || !stringValue(values["status"], (*string)(&item.Status)) {
			return nil, ErrInvalidEnvelope
		}
		items = append(items, item)
	}
	if token, err := dec.Token(); err != nil || token != json.Delim(']') || trailing(dec) {
		return nil, ErrInvalidEnvelope
	}
	return items, nil
}

func stringValue(raw []byte, target *string) bool {
	return !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, target) == nil
}
func trailing(dec *json.Decoder) bool { var value any; return dec.Decode(&value) != io.EOF }
func sameItems(left, right []ItemState) bool {
	return len(left) == len(right) && func() bool {
		for i := range left {
			if left[i] != right[i] {
				return false
			}
		}
		return true
	}()
}
