package interussprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	dss "github.com/Aero-Arc/dss-clients/interuss"
	"github.com/Aero-Arc/dss-clients/interuss/gen/scdv1"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func (p *Provider) PublicationEnabled() bool { return p.dssClient != nil && p.ussBaseURL != "" }

func (p *Provider) CreateOperationalIntent(ctx context.Context, request airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	if p.dssClient == nil || p.ussBaseURL == "" {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("InterUSS publication is not configured")
	}
	id, err := dss.SCDEntityID(request.Intent.ID)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("parse operational intent UUID: %w", err)
	}
	body, err := p.publicationParameters(request)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	response, err := p.dssClient.SCDv1.CreateOperationalIntentReferenceWithResponse(ctx, id, body)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	if response.StatusCode() != http.StatusCreated || response.JSON201 == nil {
		return airspaceprovider.PublicationReceipt{}, responseError(response.StatusCode(), response.Status(), response.Body)
	}
	return publicationReceipt(*response.JSON201)
}

func (p *Provider) UpdateOperationalIntent(ctx context.Context, request airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	if p.dssClient == nil || p.ussBaseURL == "" {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("InterUSS publication is not configured")
	}
	id, err := dss.SCDEntityID(request.Intent.ID)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("parse operational intent UUID: %w", err)
	}
	ovn := scdv1.EntityOVN(request.OVN)
	body, err := p.publicationParameters(request)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	response, err := p.dssClient.SCDv1.UpdateOperationalIntentReferenceWithResponse(ctx, id, ovn, body)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return airspaceprovider.PublicationReceipt{}, responseError(response.StatusCode(), response.Status(), response.Body)
	}
	return publicationReceipt(*response.JSON200)
}

func (p *Provider) DeleteOperationalIntent(ctx context.Context, intentID, ovn string) (airspaceprovider.PublicationReceipt, error) {
	if p.dssClient == nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("InterUSS publication is not configured")
	}
	id, err := dss.SCDEntityID(intentID)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("parse operational intent UUID: %w", err)
	}
	response, err := p.dssClient.SCDv1.DeleteOperationalIntentReferenceWithResponse(ctx, id, scdv1.EntityOVN(ovn))
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return airspaceprovider.PublicationReceipt{}, responseError(response.StatusCode(), response.Status(), response.Body)
	}
	return publicationReceipt(*response.JSON200)
}

func (p *Provider) GetOperationalIntentReference(ctx context.Context, intentID string) (airspaceprovider.PublicationReceipt, error) {
	if p.dssClient == nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("InterUSS publication is not configured")
	}
	id, err := dss.SCDEntityID(intentID)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("parse operational intent UUID: %w", err)
	}
	response, err := p.dssClient.SCDv1.GetOperationalIntentReferenceWithResponse(ctx, id)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return airspaceprovider.PublicationReceipt{}, responseError(response.StatusCode(), response.Status(), response.Body)
	}
	return referenceReceipt(response.JSON200.OperationalIntentReference)
}

func (p *Provider) publicationParameters(request airspaceprovider.PublicationRequest) (scdv1.PutOperationalIntentReferenceParameters, error) {
	extents := make([]scdv1.Volume4D, 0, len(request.Volumes))
	for _, volume := range request.Volumes {
		extent, err := toSCDVolume(volume)
		if err != nil {
			return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert volume %q: %w", volume.ID, err)
		}
		extents = append(extents, extent)
	}
	var baseURL scdv1.OperationalIntentUssBaseURL
	if err := baseURL.FromUssBaseURL(p.ussBaseURL); err != nil {
		return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert USS base URL: %w", err)
	}
	params := scdv1.PutOperationalIntentReferenceParameters{
		Extents:    extents,
		State:      scdv1.OperationalIntentState(request.State),
		UssBaseUrl: baseURL,
	}
	if len(request.Key) > 0 {
		key := make(scdv1.Key, len(request.Key))
		for index, ovn := range request.Key {
			key[index] = scdv1.EntityOVN(ovn)
		}
		params.Key = &scdv1.PutOperationalIntentReferenceParameters_Key{}
		if err := params.Key.FromKey(key); err != nil {
			return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert DSS airspace key: %w", err)
		}
	}
	if request.State == domain.OperationalIntentExternalStateActivated {
		if request.SubscriptionID != "" {
			id, err := dss.SCDEntityID(request.SubscriptionID)
			if err != nil {
				return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("parse subscription UUID: %w", err)
			}
			params.SubscriptionId = &scdv1.PutOperationalIntentReferenceParameters_SubscriptionId{}
			if err := params.SubscriptionId.FromEntityID(id); err != nil {
				return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert subscription UUID: %w", err)
			}
		} else {
			var baseSubscriptionURL scdv1.SubscriptionUssBaseURL
			if err := baseSubscriptionURL.FromUssBaseURL(p.ussBaseURL); err != nil {
				return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert notification URL: %w", err)
			}
			var notificationURL scdv1.ImplicitSubscriptionParameters_UssBaseUrl
			if err := notificationURL.FromSubscriptionUssBaseURL(baseSubscriptionURL); err != nil {
				return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert notification URL: %w", err)
			}
			notifyConstraints := false
			params.NewSubscription = &scdv1.PutOperationalIntentReferenceParameters_NewSubscription{}
			if err := params.NewSubscription.FromImplicitSubscriptionParameters(scdv1.ImplicitSubscriptionParameters{
				NotifyForConstraints: &notifyConstraints,
				UssBaseUrl:           notificationURL,
			}); err != nil {
				return scdv1.PutOperationalIntentReferenceParameters{}, fmt.Errorf("convert implicit subscription: %w", err)
			}
		}
	}
	return params, nil
}

func publicationReceipt(response scdv1.ChangeOperationalIntentReferenceResponse) (airspaceprovider.PublicationReceipt, error) {
	receipt, err := referenceReceipt(response.OperationalIntentReference)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, err
	}
	receipt.Subscribers = make([]airspaceprovider.Subscriber, 0, len(response.Subscribers))
	for _, subscriber := range response.Subscribers {
		url, err := subscriber.UssBaseUrl.AsUssBaseURL()
		if err != nil {
			return airspaceprovider.PublicationReceipt{}, fmt.Errorf("read subscriber URL: %w", err)
		}
		item := airspaceprovider.Subscriber{USSBaseURL: url, Subscriptions: make([]airspaceprovider.SubscriptionState, 0, len(subscriber.Subscriptions))}
		for _, subscription := range subscriber.Subscriptions {
			uuid, err := subscription.SubscriptionId.AsUUIDv4Format()
			if err != nil {
				return airspaceprovider.PublicationReceipt{}, fmt.Errorf("convert subscriber subscription ID: %w", err)
			}
			item.Subscriptions = append(item.Subscriptions, airspaceprovider.SubscriptionState{ID: uuid.String(), NotificationIndex: int(subscription.NotificationIndex)})
		}
		receipt.Subscribers = append(receipt.Subscribers, item)
	}
	return receipt, nil
}

func referenceReceipt(reference scdv1.OperationalIntentReference) (airspaceprovider.PublicationReceipt, error) {
	baseURL, err := reference.UssBaseUrl.AsUssBaseURL()
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("read returned USS base URL: %w", err)
	}
	if reference.Ovn == nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("DSS response omitted manager OVN")
	}
	ovn, err := reference.Ovn.AsEntityOVN()
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("read returned OVN: %w", err)
	}
	subscriptionID, err := reference.SubscriptionId.AsSubscriptionID()
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("read returned subscription ID: %w", err)
	}
	raw, err := json.Marshal(reference)
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("encode returned reference: %w", err)
	}
	subscriptionUUID, err := subscriptionID.AsUUIDv4Format()
	if err != nil {
		return airspaceprovider.PublicationReceipt{}, fmt.Errorf("convert returned subscription ID: %w", err)
	}
	subscriptionValue := subscriptionUUID.String()
	if subscriptionValue == "00000000-0000-4000-8000-000000000000" {
		subscriptionValue = ""
	}
	receipt := airspaceprovider.PublicationReceipt{
		Manager: reference.Manager, Version: int(reference.Version), OVN: string(ovn),
		SubscriptionID: subscriptionValue, USSBaseURL: baseURL, ReferenceJSON: raw,
		State: domain.OperationalIntentExternalState(reference.State),
	}
	return receipt, nil
}

func responseError(statusCode int, status string, body []byte) error {
	return &dss.SCDResponseError{StatusCode: statusCode, Status: status, Body: body}
}

func (p *Provider) BuildPeerNotification(request airspaceprovider.PublicationRequest, receipt airspaceprovider.PublicationReceipt, subscriber airspaceprovider.Subscriber, deleted bool) ([]byte, error) {
	entityID, err := dss.SCDEntityID(request.Intent.ID)
	if err != nil {
		return nil, err
	}
	var wrappedID scdv1.PutOperationalIntentDetailsParameters_OperationalIntentId
	if err := wrappedID.FromEntityID(entityID); err != nil {
		return nil, err
	}
	body := scdv1.PutOperationalIntentDetailsParameters{
		OperationalIntentId: wrappedID,
		Subscriptions:       make([]scdv1.SubscriptionState, 0, len(subscriber.Subscriptions)),
	}
	for _, subscription := range subscriber.Subscriptions {
		id, err := dss.SCDSubscriptionID(subscription.ID)
		if err != nil {
			return nil, fmt.Errorf("parse subscriber subscription ID: %w", err)
		}
		body.Subscriptions = append(body.Subscriptions, scdv1.SubscriptionState{
			SubscriptionId: id, NotificationIndex: scdv1.SubscriptionNotificationIndex(subscription.NotificationIndex),
		})
	}
	if !deleted {
		intent, err := PublishedOperationalIntent(receipt.ReferenceJSON, request.Volumes)
		if err != nil {
			return nil, err
		}
		body.OperationalIntent = &scdv1.PutOperationalIntentDetailsParameters_OperationalIntent{}
		if err := body.OperationalIntent.FromOperationalIntent(intent); err != nil {
			return nil, fmt.Errorf("encode notified operational intent: %w", err)
		}
	}
	return json.Marshal(body)
}

func (p *Provider) DeliverPeerNotification(ctx context.Context, baseURL string, payload []byte) error {
	if err := validatePeerURL(baseURL, p.allowInsecurePeerURLs); err != nil {
		return err
	}
	peer, err := p.peerClient.NewSCDPeer(baseURL)
	if err != nil {
		return err
	}
	var body scdv1.NotifyOperationalIntentDetailsChangedJSONRequestBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("decode queued peer notification: %w", err)
	}
	response, err := peer.NotifyOperationalIntentDetailsChangedWithResponse(ctx, body)
	if err != nil {
		return err
	}
	if response.StatusCode() != http.StatusNoContent {
		return responseError(response.StatusCode(), response.Status(), response.Body)
	}
	return nil
}
