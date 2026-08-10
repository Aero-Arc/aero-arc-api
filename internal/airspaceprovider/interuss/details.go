package interussprovider

import (
	"encoding/json"
	"fmt"

	"github.com/Aero-Arc/dss-clients/interuss/gen/scdv1"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func PublishedOperationalIntent(referenceJSON []byte, volumes []domain.OperationalVolume) (scdv1.OperationalIntent, error) {
	var reference scdv1.OperationalIntentReference
	if len(referenceJSON) == 0 {
		return scdv1.OperationalIntent{}, fmt.Errorf("published DSS reference is missing")
	}
	if err := json.Unmarshal(referenceJSON, &reference); err != nil {
		return scdv1.OperationalIntent{}, fmt.Errorf("decode published DSS reference: %w", err)
	}
	nominal := make([]scdv1.Volume4D, 0, len(volumes))
	offNominal := make([]scdv1.Volume4D, 0)
	for _, volume := range volumes {
		converted, err := toSCDVolume(volume)
		if err != nil {
			return scdv1.OperationalIntent{}, fmt.Errorf("convert published volume %q: %w", volume.ID, err)
		}
		switch volume.VolumeType {
		case domain.OperationalVolumeContingency, domain.OperationalVolumeEmergency:
			offNominal = append(offNominal, converted)
		default:
			nominal = append(nominal, converted)
		}
	}
	if (reference.State == scdv1.Accepted || reference.State == scdv1.Activated) && len(offNominal) > 0 {
		return scdv1.OperationalIntent{}, fmt.Errorf("accepted and activated intents cannot publish off-nominal volumes")
	}
	priority := scdv1.Priority(0)
	details := scdv1.OperationalIntentDetails{Priority: &priority}
	if len(nominal) > 0 {
		details.Volumes = &nominal
	}
	if len(offNominal) > 0 {
		details.OffNominalVolumes = &offNominal
	}
	return scdv1.OperationalIntent{Reference: reference, Details: details}, nil
}
