package domain

import pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"

// FlightCompletion is the durable API admission and finalization obligation.
type FlightCompletion struct {
	Evidence   *pb.FlightCompletionEvidence `json:"evidence"`
	State      string                       `json:"state"`
	Attempts   int                          `json:"attempts"`
	Generation int64                        `json:"-"`
	Error      string                       `json:"error,omitempty"`
}
