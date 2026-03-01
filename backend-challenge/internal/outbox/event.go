package outbox

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event types
const (
	EventOrderPlaced   = "OrderPlaced"
	EventProductCreated = "ProductCreated"
	EventProductUpdated = "ProductUpdated"
	EventProductDeleted = "ProductDeleted"
)

// Aggregate types
const (
	AggregateOrder   = "order"
	AggregateProduct = "product"
)

// Event represents an outbox event to be published
type Event struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	Status        string
	CreatedAt     time.Time
	ProcessedAt   *time.Time
	ErrorMessage  *string
}

// NewEvent creates an event for insertion into the outbox
func NewEvent(aggregateType, aggregateID, eventType string, payload any) (*Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Event{
		ID:            uuid.New().String(),
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		EventType:     eventType,
		Payload:       data,
		Status:        "pending",
		CreatedAt:     time.Now(),
	}, nil
}
