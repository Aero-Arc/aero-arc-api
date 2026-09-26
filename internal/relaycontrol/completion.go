package relaycontrol

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aero-arc/aero-arc-protos/flightcompletion"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
)

// DrainFlightCompletions discovers live Relays and durably admits their pending
// notifications before acknowledging delivery. Agent placement is not used:
// a previous Relay may still own an older flight's delivery obligation.
// Parameters: ctx bounds discovery/delivery; admit must commit the exact event.
// Returns: joined per-Relay errors while continuing other available Relays.
func (s *Service) DrainFlightCompletions(ctx context.Context, admit func(context.Context, *pb.FlightCompletionEvidence) error) error {
	response, err := s.registry.ListRelays(ctx, &registryv1.ListRelaysRequest{})
	if err != nil {
		return err
	}
	var failures []error
	for _, relay := range response.GetRelays() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		client, err := s.pool.Client(call, relay.GetRelayId(), relayAddress(relay))
		if err == nil {
			var events *relayv1.ListFlightCompletionsResponse
			events, err = client.ListFlightCompletions(call, &relayv1.ListFlightCompletionsRequest{Limit: 100})
			if err == nil {
				for _, e := range events.GetEvents() {
					_, digest, validationErr := flightcompletion.Encode(e)
					if validationErr != nil {
						failures = append(failures, validationErr)
						continue
					}
					if admissionErr := admit(call, e); admissionErr != nil {
						failures = append(failures, admissionErr)
						continue
					}
					_, ackErr := client.AckFlightCompletions(call, &relayv1.AckFlightCompletionsRequest{Receipts: []*pb.FlightCompletionReceipt{{EventId: e.EventId, PayloadSha256: digest}}})
					if ackErr != nil {
						failures = append(failures, ackErr)
					}
				}
			}
		}
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("relay %s completion delivery: %w", relay.GetRelayId(), err))
		}
	}
	return errors.Join(failures...)
}
