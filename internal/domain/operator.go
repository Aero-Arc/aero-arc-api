package domain

import "time"

// TODO: enforce OperatorID as required on operator-scoped records in service
// validation and durable database constraints before multi-tenant production use.
type Operator struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	LegalName string    `json:"legal_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
