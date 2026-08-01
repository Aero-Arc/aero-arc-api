package interussprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	dss "github.com/Aero-Arc/dss-clients/interuss"
	"github.com/Aero-Arc/dss-clients/interuss/gen/scdv1"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

const (
	providerID = "interuss_scd"
	// Deliberately low so the envelope over-approximates WGS84 distances.
	metersPerDegree = 110_000.0
)

type Client interface {
	QueryOperationalIntentReferences(context.Context, scdv1.Volume4D) ([]scdv1.OperationalIntentReference, error)
	GetOperationalIntent(context.Context, scdv1.OperationalIntentReference) (*scdv1.OperationalIntent, error)
}

type Provider struct {
	client Client
}

type Config struct {
	BaseURL               string
	StaticToken           string
	OAuthTokenURL         string
	OAuthAudience         string
	OAuthIssuer           string
	OAuthSubject          string
	AllowInsecurePeerURLs bool
	RequestTimeout        time.Duration
}

// New constructs an InterUSS provider and the DSS and peer clients it uses.
func New(cfg Config) (*Provider, error) {
	dssHTTPClient := &http.Client{Timeout: cfg.RequestTimeout}
	peerHTTPClient := NewPeerHTTPClient(cfg.RequestTimeout, cfg.AllowInsecurePeerURLs)
	var tokenSource dss.TokenSource
	switch {
	case cfg.StaticToken != "":
		tokenSource = dss.StaticTokenSource(cfg.StaticToken)
	case cfg.OAuthTokenURL != "":
		tokenSource = dss.DummyOAuthTokenSource{
			TokenURL:         cfg.OAuthTokenURL,
			Scope:            dss.ScopeUTMStrategicCoordination,
			IntendedAudience: cfg.OAuthAudience,
			Issuer:           cfg.OAuthIssuer,
			Subject:          cfg.OAuthSubject,
			HTTPClient:       dssHTTPClient,
		}
	}
	queryClient, err := dss.NewClient(dss.Config{
		BaseURL:     cfg.BaseURL,
		TokenSource: tokenSource,
		HTTPClient:  dssHTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("configure InterUSS DSS client: %w", err)
	}
	peerClient, err := dss.NewClient(dss.Config{
		BaseURL:     cfg.BaseURL,
		TokenSource: tokenSource,
		HTTPClient:  peerHTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("configure InterUSS peer client: %w", err)
	}
	return NewWithClient(NewClientWithPeer(
		queryClient, peerClient, cfg.AllowInsecurePeerURLs,
	)), nil
}

// NewWithClient constructs a provider around an injected client.
func NewWithClient(client Client) *Provider {
	return &Provider{client: client}
}

func NewClientWithPeer(queryClient, peerClient *dss.Client, allowInsecurePeerURLs bool) Client {
	return &clientAdapter{
		queryClient:           queryClient,
		peerClient:            peerClient,
		allowInsecurePeerURLs: allowInsecurePeerURLs,
	}
}

// NewPeerHTTPClient blocks peer USS connections to local and private networks
// unless explicitly enabled for a local development stack.
func NewPeerHTTPClient(timeout time.Duration, allowInsecurePeerURLs bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !allowInsecurePeerURLs {
		// A proxy could resolve or reach a private target on the client's behalf.
		transport.Proxy = nil
		dialer := &net.Dialer{Timeout: timeout}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("read peer address: %w", err)
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve peer host %q: %w", host, err)
			}
			for _, address := range addresses {
				if publicPeerIP(address.IP) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
				}
			}
			return nil, fmt.Errorf("peer host %q does not resolve to a public address", host)
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many peer redirects")
			}
			return validatePeerURL(request.URL.String(), allowInsecurePeerURLs)
		},
	}
}

func (p *Provider) ID() string {
	return providerID
}

func (p *Provider) FindOperationalIntents(ctx context.Context, query airspaceprovider.Query) ([]airspaceprovider.OperationalIntent, error) {
	if p.client == nil {
		return nil, fmt.Errorf("InterUSS client is not configured")
	}

	references := make(map[string]scdv1.OperationalIntentReference)
	var queryErrors []error
	for index, volume := range query.Volumes {
		area, err := toSCDVolume(volume)
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("target volume %q: %w", volume.ID, err))
			continue
		}
		found, err := p.client.QueryOperationalIntentReferences(ctx, area)
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("query target volume %d: %w", index, err))
			continue
		}
		for _, reference := range found {
			id, err := referenceID(reference)
			if err != nil {
				queryErrors = append(queryErrors, fmt.Errorf("read DSS reference: %w", err))
				continue
			}
			if current, exists := references[id]; !exists || reference.Version > current.Version {
				references[id] = reference
			}
		}
	}

	records := make([]airspaceprovider.OperationalIntent, 0, len(references))
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, id := range keys {
		reference := references[id]
		key := fmt.Sprintf("%s:%d", id, reference.Version)
		intent, err := p.client.GetOperationalIntent(ctx, reference)
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("get operational intent %s: %w", key, err))
			continue
		}
		record, err := fromSCDIntent(reference, *intent)
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("convert operational intent %s: %w", key, err))
			continue
		}
		records = append(records, record)
	}
	return records, errors.Join(queryErrors...)
}

type clientAdapter struct {
	queryClient           *dss.Client
	peerClient            *dss.Client
	allowInsecurePeerURLs bool
}

func (c *clientAdapter) QueryOperationalIntentReferences(ctx context.Context, area scdv1.Volume4D) ([]scdv1.OperationalIntentReference, error) {
	response, err := c.queryClient.SCDv1.QueryOperationalIntentReferencesWithResponse(
		ctx,
		scdv1.QueryOperationalIntentReferencesJSONRequestBody{AreaOfInterest: &area},
	)
	if err != nil {
		return nil, err
	}
	if response.StatusCode() != http.StatusOK {
		return nil, &dss.SCDResponseError{
			StatusCode: response.StatusCode(),
			Status:     response.Status(),
			Body:       response.Body,
		}
	}
	if response.JSON200 == nil {
		return nil, fmt.Errorf("DSS query returned 200 without a JSON body")
	}
	return response.JSON200.OperationalIntentReferences, nil
}

func (c *clientAdapter) GetOperationalIntent(ctx context.Context, reference scdv1.OperationalIntentReference) (*scdv1.OperationalIntent, error) {
	baseURL, err := reference.UssBaseUrl.AsUssBaseURL()
	if err != nil {
		return nil, fmt.Errorf("read peer USS base URL: %w", err)
	}
	if err := validatePeerURL(baseURL, c.allowInsecurePeerURLs); err != nil {
		return nil, err
	}
	return c.peerClient.GetOperationalIntent(ctx, reference)
}

func validatePeerURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse peer USS URL: %w", err)
	}
	if parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("peer USS URL must contain only a scheme, host, port, and optional path")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("peer USS URL scheme %q is not supported", parsed.Scheme)
	}
	if allowInsecure {
		return nil
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("peer USS URL must use HTTPS")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("peer USS URL cannot target localhost")
	}
	if address, err := netip.ParseAddr(host); err == nil && !publicPeerAddr(address) {
		return fmt.Errorf("peer USS URL cannot target a private or local address")
	}
	return nil
}

func publicPeerIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	return ok && publicPeerAddr(address.Unmap())
}

func publicPeerAddr(address netip.Addr) bool {
	return address.IsGlobalUnicast() &&
		!address.IsPrivate() &&
		!address.IsLoopback() &&
		!address.IsLinkLocalUnicast() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func toSCDVolume(volume domain.OperationalVolume) (scdv1.Volume4D, error) {
	if volume.AltitudeRef != domain.AltitudeReferenceWGS84 {
		return scdv1.Volume4D{}, fmt.Errorf("altitude reference %q is not supported by SCD", volume.AltitudeRef)
	}
	if volume.StartsAt.IsZero() || volume.EndsAt.IsZero() || !volume.StartsAt.Before(volume.EndsAt) {
		return scdv1.Volume4D{}, fmt.Errorf("time window is invalid")
	}
	if volume.MinAltitudeM < 0 || volume.MinAltitudeM >= volume.MaxAltitudeM {
		return scdv1.Volume4D{}, fmt.Errorf("altitude band is invalid")
	}
	bounds, err := geoJSONBounds(volume.GeoJSON)
	if err != nil {
		return scdv1.Volume4D{}, err
	}
	if volume.BufferMeters != nil {
		bounds.expand(*volume.BufferMeters)
	}
	if bounds.crossesAntimeridian() {
		return scdv1.Volume4D{}, fmt.Errorf("geometry crossing the antimeridian is not supported")
	}

	polygon := scdv1.Polygon{Vertices: []scdv1.LatLngPoint{
		{Lat: bounds.minLat, Lng: bounds.minLon},
		{Lat: bounds.minLat, Lng: bounds.maxLon},
		{Lat: bounds.maxLat, Lng: bounds.maxLon},
		{Lat: bounds.maxLat, Lng: bounds.minLon},
	}}
	lower := &scdv1.Volume3D_AltitudeLower{}
	upper := &scdv1.Volume3D_AltitudeUpper{}
	outline := &scdv1.Volume3D_OutlinePolygon{}
	start := &scdv1.Volume4D_TimeStart{}
	end := &scdv1.Volume4D_TimeEnd{}
	if err := lower.FromAltitude(scdv1.Altitude{Reference: scdv1.W84, Units: scdv1.AltitudeUnitsM, Value: volume.MinAltitudeM}); err != nil {
		return scdv1.Volume4D{}, err
	}
	if err := upper.FromAltitude(scdv1.Altitude{Reference: scdv1.W84, Units: scdv1.AltitudeUnitsM, Value: volume.MaxAltitudeM}); err != nil {
		return scdv1.Volume4D{}, err
	}
	if err := outline.FromPolygon(polygon); err != nil {
		return scdv1.Volume4D{}, err
	}
	if err := start.FromTime(scdv1.Time{Format: scdv1.RFC3339, Value: volume.StartsAt.UTC()}); err != nil {
		return scdv1.Volume4D{}, err
	}
	if err := end.FromTime(scdv1.Time{Format: scdv1.RFC3339, Value: volume.EndsAt.UTC()}); err != nil {
		return scdv1.Volume4D{}, err
	}
	return scdv1.Volume4D{
		TimeStart: start,
		TimeEnd:   end,
		Volume: scdv1.Volume3D{
			AltitudeLower:  lower,
			AltitudeUpper:  upper,
			OutlinePolygon: outline,
		},
	}, nil
}

func fromSCDIntent(reference scdv1.OperationalIntentReference, intent scdv1.OperationalIntent) (airspaceprovider.OperationalIntent, error) {
	if err := validateReturnedReference(reference, intent.Reference); err != nil {
		return airspaceprovider.OperationalIntent{}, err
	}
	id, err := referenceID(reference)
	if err != nil {
		return airspaceprovider.OperationalIntent{}, err
	}
	baseURL, err := reference.UssBaseUrl.AsUssBaseURL()
	if err != nil {
		return airspaceprovider.OperationalIntent{}, fmt.Errorf("read USS base URL: %w", err)
	}
	version := int(reference.Version)
	record := airspaceprovider.OperationalIntent{
		Source: airspaceprovider.Source{
			ReferenceID: id,
			Manager:     reference.Manager,
			USSBaseURL:  baseURL,
			Version:     version,
		},
		Intent: domain.OperationalIntent{
			ID:      id,
			Version: version,
			Status:  intentStatus(reference.State),
		},
	}
	if intent.Details.Volumes != nil {
		record.Volumes, err = fromSCDVolumes(id, version, "nominal", *intent.Details.Volumes)
		if err != nil {
			return airspaceprovider.OperationalIntent{}, err
		}
	}
	if intent.Details.OffNominalVolumes != nil {
		record.OffNominalVolumes, err = fromSCDVolumes(id, version, "off-nominal", *intent.Details.OffNominalVolumes)
		if err != nil {
			return airspaceprovider.OperationalIntent{}, err
		}
	}
	if err := validateVolumeSets(reference.State, len(record.Volumes), len(record.OffNominalVolumes)); err != nil {
		return airspaceprovider.OperationalIntent{}, err
	}
	return record, nil
}

func validateReturnedReference(expected, actual scdv1.OperationalIntentReference) error {
	expectedID, err := referenceID(expected)
	if err != nil {
		return err
	}
	actualID, err := referenceID(actual)
	if err != nil {
		return fmt.Errorf("read returned entity ID: %w", err)
	}
	if actualID != expectedID || actual.Version != expected.Version ||
		actual.Manager != expected.Manager || actual.State != expected.State {
		return fmt.Errorf(
			"peer returned mismatched reference: got %s v%d manager %q state %q, want %s v%d manager %q state %q",
			actualID, actual.Version, actual.Manager, actual.State,
			expectedID, expected.Version, expected.Manager, expected.State,
		)
	}
	return nil
}

func validateVolumeSets(state scdv1.OperationalIntentState, nominal, offNominal int) error {
	switch state {
	case scdv1.Accepted, scdv1.Activated:
		if nominal == 0 || offNominal != 0 {
			return fmt.Errorf("%s intent requires nominal volumes and no off-nominal volumes", state)
		}
	case scdv1.Nonconforming:
		if nominal == 0 || offNominal == 0 {
			return fmt.Errorf("nonconforming intent requires nominal and off-nominal volumes")
		}
	case scdv1.Contingent:
		if nominal != 0 || offNominal == 0 {
			return fmt.Errorf("contingent intent requires off-nominal volumes and no nominal volumes")
		}
	default:
		return fmt.Errorf("operational intent state %q is not supported", state)
	}
	return nil
}

func fromSCDVolumes(intentID string, version int, kind string, volumes []scdv1.Volume4D) ([]domain.OperationalVolume, error) {
	converted := make([]domain.OperationalVolume, 0, len(volumes))
	for index, volume := range volumes {
		item, err := fromSCDVolume(volume)
		if err != nil {
			return nil, fmt.Errorf("%s volume %d: %w", kind, index, err)
		}
		item.ID = fmt.Sprintf("%s-v%d-%s-%d", intentID, version, kind, index)
		item.IntentID = intentID
		item.IntentVersion = version
		item.Sequence = index
		if kind == "off-nominal" {
			item.VolumeType = domain.OperationalVolumeContingency
		} else {
			item.VolumeType = domain.OperationalVolumeRoute
		}
		converted = append(converted, item)
	}
	return converted, nil
}

func fromSCDVolume(volume scdv1.Volume4D) (domain.OperationalVolume, error) {
	if volume.TimeStart == nil || volume.TimeEnd == nil ||
		volume.Volume.AltitudeLower == nil || volume.Volume.AltitudeUpper == nil {
		return domain.OperationalVolume{}, fmt.Errorf("time and altitude bounds are required")
	}
	start, err := volume.TimeStart.AsTime()
	if err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("read start time: %w", err)
	}
	end, err := volume.TimeEnd.AsTime()
	if err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("read end time: %w", err)
	}
	lower, err := volume.Volume.AltitudeLower.AsAltitude()
	if err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("read lower altitude: %w", err)
	}
	upper, err := volume.Volume.AltitudeUpper.AsAltitude()
	if err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("read upper altitude: %w", err)
	}
	if lower.Reference != scdv1.W84 || upper.Reference != scdv1.W84 ||
		lower.Units != scdv1.AltitudeUnitsM || upper.Units != scdv1.AltitudeUnitsM {
		return domain.OperationalVolume{}, fmt.Errorf("only W84 altitudes in meters are supported")
	}

	geoJSON, err := scdGeometryGeoJSON(volume.Volume)
	if err != nil {
		return domain.OperationalVolume{}, err
	}
	return domain.OperationalVolume{
		GeoJSON:      geoJSON,
		MinAltitudeM: lower.Value,
		MaxAltitudeM: upper.Value,
		AltitudeRef:  domain.AltitudeReferenceWGS84,
		StartsAt:     start.Value.UTC(),
		EndsAt:       end.Value.UTC(),
	}, nil
}

func scdGeometryGeoJSON(volume scdv1.Volume3D) (string, error) {
	switch {
	case volume.OutlinePolygon != nil && volume.OutlineCircle != nil:
		return "", fmt.Errorf("volume contains both polygon and circle outlines")
	case volume.OutlinePolygon != nil:
		polygon, err := volume.OutlinePolygon.AsPolygon()
		if err != nil {
			return "", fmt.Errorf("read polygon: %w", err)
		}
		if len(polygon.Vertices) < 3 {
			return "", fmt.Errorf("polygon has fewer than three vertices")
		}
		positions := make([][]float64, 0, len(polygon.Vertices)+1)
		bounds := geographicBounds{
			minLat: math.MaxFloat64,
			maxLat: -math.MaxFloat64,
			minLon: math.MaxFloat64,
			maxLon: -math.MaxFloat64,
		}
		for _, vertex := range polygon.Vertices {
			if !finite(vertex.Lat) || !finite(vertex.Lng) ||
				vertex.Lat < -90 || vertex.Lat > 90 ||
				vertex.Lng < -180 || vertex.Lng > 180 {
				return "", fmt.Errorf("polygon contains an invalid coordinate")
			}
			positions = append(positions, []float64{vertex.Lng, vertex.Lat})
			bounds.minLat = math.Min(bounds.minLat, vertex.Lat)
			bounds.maxLat = math.Max(bounds.maxLat, vertex.Lat)
			bounds.minLon = math.Min(bounds.minLon, vertex.Lng)
			bounds.maxLon = math.Max(bounds.maxLon, vertex.Lng)
		}
		if bounds.crossesAntimeridian() {
			return "", fmt.Errorf("polygon crossing the antimeridian is not supported")
		}
		positions = append(positions, positions[0])
		return marshalPolygon(positions)
	case volume.OutlineCircle != nil:
		circle, err := volume.OutlineCircle.AsCircle()
		if err != nil {
			return "", fmt.Errorf("read circle: %w", err)
		}
		if circle.Center == nil || circle.Radius == nil ||
			circle.Radius.Units != scdv1.RadiusUnitsM || circle.Radius.Value <= 0 {
			return "", fmt.Errorf("circle center and positive meter radius are required")
		}
		bounds := geographicBounds{
			minLat: circle.Center.Lat,
			maxLat: circle.Center.Lat,
			minLon: circle.Center.Lng,
			maxLon: circle.Center.Lng,
		}
		bounds.expand(float64(circle.Radius.Value))
		if bounds.crossesAntimeridian() {
			return "", fmt.Errorf("circle crossing the antimeridian is not supported")
		}
		positions := [][]float64{
			{bounds.minLon, bounds.minLat},
			{bounds.maxLon, bounds.minLat},
			{bounds.maxLon, bounds.maxLat},
			{bounds.minLon, bounds.maxLat},
			{bounds.minLon, bounds.minLat},
		}
		return marshalPolygon(positions)
	default:
		return "", fmt.Errorf("volume has no geographic outline")
	}
}

func marshalPolygon(positions [][]float64) (string, error) {
	document, err := json.Marshal(struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}{Type: "Polygon", Coordinates: [][][]float64{positions}})
	if err != nil {
		return "", fmt.Errorf("encode polygon: %w", err)
	}
	return string(document), nil
}

func referenceKey(reference scdv1.OperationalIntentReference) (string, error) {
	id, err := referenceID(reference)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", id, reference.Version), nil
}

func referenceID(reference scdv1.OperationalIntentReference) (string, error) {
	id, err := reference.Id.AsUUIDv4Format()
	if err != nil {
		return "", fmt.Errorf("read entity ID: %w", err)
	}
	return id.String(), nil
}

func intentStatus(state scdv1.OperationalIntentState) domain.IntentStatus {
	if state == scdv1.Accepted {
		return domain.IntentStatusAccepted
	}
	return domain.IntentStatusActive
}

type geographicBounds struct {
	minLat float64
	maxLat float64
	minLon float64
	maxLon float64
}

func (bounds *geographicBounds) expand(meters float64) {
	if meters <= 0 {
		return
	}
	latitudeDelta := meters / metersPerDegree
	maxAbsLatitude := math.Max(math.Abs(bounds.minLat), math.Abs(bounds.maxLat))
	cosLatitude := math.Cos((maxAbsLatitude + latitudeDelta) * math.Pi / 180)
	if cosLatitude < 0.01 {
		cosLatitude = 0.01
	}
	longitudeDelta := meters / (metersPerDegree * cosLatitude)
	bounds.minLat = math.Max(-90, bounds.minLat-latitudeDelta)
	bounds.maxLat = math.Min(90, bounds.maxLat+latitudeDelta)
	bounds.minLon -= longitudeDelta
	bounds.maxLon += longitudeDelta
}

func (bounds geographicBounds) crossesAntimeridian() bool {
	return bounds.minLon < -180 || bounds.maxLon > 180 || bounds.maxLon-bounds.minLon > 180
}

func geoJSONBounds(raw string) (geographicBounds, error) {
	if strings.TrimSpace(raw) == "" {
		return geographicBounds{}, fmt.Errorf("inline GeoJSON polygon is required for DSS discovery")
	}
	var geometry struct {
		Type        string           `json:"type"`
		Geometry    *json.RawMessage `json:"geometry"`
		Coordinates [][][]float64    `json:"coordinates"`
	}
	if err := json.Unmarshal([]byte(raw), &geometry); err != nil {
		return geographicBounds{}, fmt.Errorf("decode GeoJSON: %w", err)
	}
	if geometry.Type == "Feature" {
		if geometry.Geometry == nil {
			return geographicBounds{}, fmt.Errorf("GeoJSON feature has no geometry")
		}
		return geoJSONBounds(string(*geometry.Geometry))
	}
	if geometry.Type != "Polygon" || len(geometry.Coordinates) == 0 || len(geometry.Coordinates[0]) < 4 {
		return geographicBounds{}, fmt.Errorf("GeoJSON must contain a polygon with a closed exterior ring")
	}
	ring := geometry.Coordinates[0]
	first, last := ring[0], ring[len(ring)-1]
	if len(first) < 2 || len(last) < 2 || first[0] != last[0] || first[1] != last[1] {
		return geographicBounds{}, fmt.Errorf("GeoJSON polygon exterior ring is not closed")
	}
	bounds := geographicBounds{
		minLat: math.MaxFloat64,
		maxLat: -math.MaxFloat64,
		minLon: math.MaxFloat64,
		maxLon: -math.MaxFloat64,
	}
	for _, position := range ring {
		if len(position) < 2 || !finite(position[0]) || !finite(position[1]) ||
			position[0] < -180 || position[0] > 180 ||
			position[1] < -90 || position[1] > 90 {
			return geographicBounds{}, fmt.Errorf("GeoJSON polygon contains an invalid coordinate")
		}
		bounds.minLon = math.Min(bounds.minLon, position[0])
		bounds.maxLon = math.Max(bounds.maxLon, position[0])
		bounds.minLat = math.Min(bounds.minLat, position[1])
		bounds.maxLat = math.Max(bounds.maxLat, position[1])
	}
	return bounds, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
